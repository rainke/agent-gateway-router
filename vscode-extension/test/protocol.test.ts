import { describe, expect, it } from 'vitest';
import { buildRequest, StreamDecoder, type Api, type Message } from '../src/protocol';

const messages: Message[] = [
  { role: 'user', content: [{ type: 'text', text: 'Find it' }] },
  { role: 'assistant', content: [{ type: 'text', text: 'Looking' }, { type: 'call', id: 'c1', name: 'search', input: { q: 'agr' } }] },
  { role: 'user', content: [{ type: 'result', id: 'c1', text: 'found' }, { type: 'text', text: 'Explain' }] }
];
const tools = [{ name: 'search', description: 'Search', inputSchema: { type: 'object' } }];

describe('native requests', () => {
  it('preserves routing IDs, tool history and required tool choice for Chat Completions', () => {
    const r = buildRequest('chat-completions', 'provider/org/model', messages, 4096, tools, true);
    expect(r.path).toBe('chat/completions');
    expect(r.body).toMatchObject({ model: 'provider/org/model', stream: true, max_tokens: 4096, tool_choice: 'required' });
    expect(r.body.messages).toEqual([
      { role: 'user', content: 'Find it' },
      { role: 'assistant', content: 'Looking', tool_calls: [{ id: 'c1', type: 'function', function: { name: 'search', arguments: '{"q":"agr"}' } }] },
      { role: 'tool', tool_call_id: 'c1', content: 'found' },
      { role: 'user', content: 'Explain' }
    ]);
  });
  it('uses Messages tool_use / tool_result and input_schema', () => {
    const { body, path } = buildRequest('messages', 'p/m', messages, 123, tools, true);
    expect(path).toBe('messages');
    expect(body).toMatchObject({ max_tokens: 123, tool_choice: { type: 'any' }, tools: [{ name: 'search', input_schema: { type: 'object' } }] });
    expect(body.messages[1].content[1]).toEqual({ type: 'tool_use', id: 'c1', name: 'search', input: { q: 'agr' } });
    expect(body.messages[2].content[0]).toEqual({ type: 'tool_result', tool_use_id: 'c1', content: 'found' });
  });
  it('uses Responses function_call / function_call_output and disables persistence', () => {
    const { body } = buildRequest('responses', 'p/m', messages, 200, tools, true);
    expect(body).toMatchObject({ store: false, max_output_tokens: 200, tool_choice: 'required' });
    expect(body.input).toEqual([
      { role: 'user', content: 'Find it' }, { role: 'assistant', content: 'Looking' },
      { type: 'function_call', call_id: 'c1', name: 'search', arguments: '{"q":"agr"}' },
      { type: 'function_call_output', call_id: 'c1', output: 'found' }, { role: 'user', content: 'Explain' }
    ]);
  });
  it.each<Api>(['chat-completions', 'messages', 'responses'])('omits tools when absent for %s', api => {
    const { body } = buildRequest(api, 'p/m', [messages[0]], 1, [], false);
    expect(body.tools).toBeUndefined();
    expect(body.tool_choice).toBeUndefined();
  });
});

describe('native stream decoding', () => {
  it('assembles interleaved tool arguments and emits text immediately', () => {
    const d = new StreamDecoder('chat-completions');
    expect(d.push({ choices: [{ delta: { content: '你好', tool_calls: [{ index: 0, id: 'c1', function: { name: 'search', arguments: '{"q":' } }] } }] })).toEqual([{ type: 'text', text: '你好' }]);
    d.push({ choices: [{ delta: { tool_calls: [{ index: 1, id: 'c2', function: { name: 'read', arguments: '{}' } }, { index: 0, function: { arguments: '"agr"}' } }] } }] });
    expect(d.push({ choices: [{ delta: {}, finish_reason: 'tool_calls' }] })).toEqual([
      { type: 'call', id: 'c1', name: 'search', input: { q: 'agr' } }, { type: 'call', id: 'c2', name: 'read', input: {} }
    ]);
    expect(d.finish()).toEqual([]);
  });
  it('reads Messages text and input_json_delta', () => {
    const d = new StreamDecoder('messages');
    expect(d.push({ type: 'content_block_start', index: 0, content_block: { type: 'text', text: 'Hi' } })).toEqual([{ type: 'text', text: 'Hi' }]);
    expect(d.push({ type: 'content_block_delta', index: 0, delta: { type: 'text_delta', text: '!' } })).toEqual([{ type: 'text', text: '!' }]);
    d.push({ type: 'content_block_start', index: 1, content_block: { type: 'tool_use', id: 'c1', name: 'search', input: {} } });
    d.push({ type: 'content_block_delta', index: 1, delta: { type: 'input_json_delta', partial_json: '{"q":"agr"}' } });
    expect(d.push({ type: 'content_block_stop', index: 1 })).toEqual([{ type: 'call', id: 'c1', name: 'search', input: { q: 'agr' } }]);
    d.push({ type: 'message_stop' });
    expect(d.finish()).toEqual([]);
  });
  it('reads Responses text and complete function calls without double emission', () => {
    const d = new StreamDecoder('responses');
    expect(d.push({ type: 'response.output_text.delta', delta: 'Hi' })).toEqual([{ type: 'text', text: 'Hi' }]);
    const item = { type: 'function_call', id: 'item1', call_id: 'c1', name: 'search', arguments: '{"q":"agr"}' };
    expect(d.push({ type: 'response.output_item.done', item })).toEqual([{ type: 'call', id: 'c1', name: 'search', input: { q: 'agr' } }]);
    d.push({ type: 'response.completed', response: { output: [item] } });
    expect(d.finish()).toEqual([]);
  });
  it.each<Api>(['chat-completions', 'messages', 'responses'])('rejects truncated %s streams and hides upstream error bodies', api => {
    expect(() => new StreamDecoder(api).finish()).toThrow(/incomplete/i);
    expect(() => new StreamDecoder(api).push({ error: { message: 'secret prompt' } })).toThrow(/upstream/i);
    expect(() => new StreamDecoder(api).push({ error: { message: 'secret prompt' } })).not.toThrow(/secret prompt/);
  });
  it('rejects invalid tool arguments instead of executing a damaged call', () => {
    const d = new StreamDecoder('chat-completions');
    d.push({ choices: [{ delta: { tool_calls: [{ index: 0, id: 'c1', function: { name: 'search', arguments: '{bad' } }] } }] });
    expect(() => d.push({ choices: [{ finish_reason: 'tool_calls' }] })).toThrow(/arguments/i);
  });
});
