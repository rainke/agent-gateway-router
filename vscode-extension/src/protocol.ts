export type Api = 'chat-completions' | 'messages' | 'responses';
export type Part = { type: 'text'; text: string } | { type: 'call'; id: string; name: string; input: object };
export type InputPart = Part | { type: 'result'; id: string; text: string };
export interface Message { role: 'user' | 'assistant'; content: InputPart[] }
export interface Tool { name: string; description: string; inputSchema?: object }
// Native API payloads are deliberately kept at the transport boundary.
type Wire = Record<string, any>;

export function messagesBody(messages: Message[]): Wire[] {
  return messages.map(message => ({
    role: message.role,
    content: message.content.map(part => {
      switch (part.type) {
        case 'text': return { type: 'text', text: part.text };
        case 'call': return { type: 'tool_use', id: part.id, name: part.name, input: part.input };
        case 'result': return { type: 'tool_result', tool_use_id: part.id, content: part.text };
      }
    })
  }));
}

function chatMessages(messages: Message[]): Wire[] {
  return messages.flatMap(message => {
    const result: Wire[] = [];
    let current: Wire = { role: message.role, content: '' };
    const flush = () => {
      if (current.content || current.tool_calls) result.push(current);
      current = { role: message.role, content: '' };
    };
    for (const part of message.content) {
      if (part.type === 'text') current.content += part.text;
      if (part.type === 'call') {
        (current.tool_calls ??= []).push({ id: part.id, type: 'function', function: { name: part.name, arguments: JSON.stringify(part.input) } });
      }
      if (part.type === 'result') { flush(); result.push({ role: 'tool', tool_call_id: part.id, content: part.text }); }
    }
    flush();
    return result;
  });
}

function responsesInput(messages: Message[]): Wire[] {
  return messages.flatMap(message => message.content.map(part => {
    switch (part.type) {
      case 'text': return { role: message.role, content: part.text };
      case 'call': return { type: 'function_call', call_id: part.id, name: part.name, arguments: JSON.stringify(part.input) };
      case 'result': return { type: 'function_call_output', call_id: part.id, output: part.text };
    }
  }));
}

export function buildRequest(api: Api, model: string, messages: Message[], maxOutput: number, tools: readonly Tool[], required: boolean): { path: string; body: Wire } {
  const body: Wire = { model, stream: true };
  if (api === 'responses') {
    Object.assign(body, { input: responsesInput(messages), max_output_tokens: maxOutput, store: false });
  } else {
    Object.assign(body, { messages: api === 'messages' ? messagesBody(messages) : chatMessages(messages), max_tokens: maxOutput });
  }
  if (tools.length) {
    body.tools = tools.map(tool => {
      const schema = tool.inputSchema ?? { type: 'object', properties: {} };
      if (api === 'messages') return { name: tool.name, description: tool.description, input_schema: schema };
      const fn = { name: tool.name, description: tool.description, parameters: schema };
      return api === 'responses' ? { type: 'function', ...fn, strict: false } : { type: 'function', function: fn };
    });
    body.tool_choice = api === 'messages' ? { type: required ? 'any' : 'auto' } : required ? 'required' : 'auto';
  }
  return { path: api === 'chat-completions' ? 'chat/completions' : api, body };
}

interface PendingCall { id: string; name: string; arguments: string; input?: object }

function callPart(call: PendingCall): Part {
  let input: unknown;
  try { input = call.arguments ? JSON.parse(call.arguments) : call.input ?? {}; }
  catch { throw new Error('agr: invalid tool arguments in upstream response.'); }
  if (!call.id || !call.name || !input || typeof input !== 'object' || Array.isArray(input)) {
    throw new Error('agr: invalid tool call or arguments in upstream response.');
  }
  return { type: 'call', id: call.id, name: call.name, input };
}

export class StreamDecoder {
  private calls = new Map<number, PendingCall>();
  private complete = false;
  constructor(private readonly api: Api) {}

  push(event: Wire): Part[] {
    if (event.error || ['error', 'response.failed', 'response.incomplete'].includes(event.type)) {
      throw new Error('agr: upstream returned a stream error or incomplete response.');
    }
    const result: Part[] = [];
    const text = (value: unknown) => { if (typeof value === 'string' && value) result.push({ type: 'text', text: value }); };
    if (this.api === 'chat-completions') {
      const choice = event.choices?.find((c: Wire) => c.index === undefined || c.index === 0);
      if (!choice) return result;
      const delta = choice.delta ?? {};
      text(delta.content);
      // Reasoning is not rewritten into visible answer text.
      text(delta.refusal);
      for (const fragment of delta.tool_calls ?? []) {
        const call = this.calls.get(fragment.index) ?? { id: '', name: '', arguments: '' };
        if (fragment.id) call.id = fragment.id;
        call.name += fragment.function?.name ?? '';
        call.arguments += fragment.function?.arguments ?? '';
        this.calls.set(fragment.index, call);
        this.checkSize(call);
      }
      if (choice.finish_reason) {
        if (choice.finish_reason === 'length' && this.calls.size) throw new Error('agr: incomplete tool arguments (output limit).');
        for (const call of this.calls.values()) result.push(callPart(call));
        this.calls.clear();
        this.complete = true;
      }
    } else if (this.api === 'messages') {
      if (event.type === 'content_block_start') {
        const block = event.content_block;
        if (block.type === 'text') text(block.text);
        if (block.type === 'tool_use') this.calls.set(event.index, { id: block.id, name: block.name, arguments: '', input: block.input });
      }
      if (event.type === 'content_block_delta') {
        if (event.delta.type === 'text_delta') text(event.delta.text);
        if (event.delta.type === 'input_json_delta') {
          const call = this.calls.get(event.index);
          if (!call) throw new Error('agr: tool arguments arrived without a tool call.');
          call.arguments += event.delta.partial_json;
          this.checkSize(call);
        }
      }
      if (event.type === 'content_block_stop' && this.calls.has(event.index)) {
        result.push(callPart(this.calls.get(event.index)!));
        this.calls.delete(event.index);
      }
      if (event.type === 'message_stop') this.complete = true;
    } else {
      if (event.type === 'response.output_text.delta' || event.type === 'response.refusal.delta') text(event.delta);
      // output_item.done contains the complete arguments; no duplicate emission on arguments.done.
      if (event.type === 'response.output_item.done' && event.item.type === 'function_call') {
        result.push(callPart({ id: event.item.call_id, name: event.item.name, arguments: event.item.arguments }));
      }
      if (event.type === 'response.completed') this.complete = true;
    }
    return result;
  }

  finish(): Part[] {
    if (!this.complete || this.calls.size) throw new Error('agr: incomplete upstream stream; retry the request.');
    return [];
  }

  private checkSize(call: PendingCall): void {
    if (call.arguments.length + call.name.length > 4 * 1024 * 1024 || this.calls.size > 1024) {
      throw new Error('agr: upstream tool call is too large.');
    }
  }
}
