import { buildRequest, messagesBody, StreamDecoder, type Api, type Message, type Part, type Tool } from './protocol';

const MAX_EVENT = 4 * 1024 * 1024;
export interface ModelEntry { id: string; owned_by?: string }

export function apiUrl(base: string, path: string): string {
  let url: URL;
  try { url = new URL(base); } catch { throw new Error('agr.baseUrl must be a valid HTTP(S) URL.'); }
  if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.search || url.hash) {
    throw new Error('agr.baseUrl must be an HTTP(S) URL without credentials, query or fragment.');
  }
  let prefix = url.pathname.replace(/\/+$/, '');
  if (!prefix.endsWith('/v1')) prefix += '/v1';
  url.pathname = prefix + '/' + path;
  return url.toString();
}

export async function* readSSE(body: ReadableStream<Uint8Array>): AsyncGenerator<string> {
  const reader = body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';
  let data: string[] = [];
  let size = 0;
  try {
    while (true) {
      const { value, done } = await reader.read();
      buffer += decoder.decode(value, { stream: !done });
      let match: RegExpExecArray | null;
      while ((match = /\r\n|\r|\n/.exec(buffer))) {
        // A CR at a chunk boundary might be followed by LF in the next chunk.
        if (!done && match[0] === '\r' && match.index === buffer.length - 1) break;
        const line = buffer.slice(0, match.index);
        buffer = buffer.slice(match.index + match[0].length);
        if (line.length > MAX_EVENT) throw new Error('agr: SSE event is too large.');
        if (!line) {
          if (data.length) yield data.join('\n');
          data = []; size = 0;
        } else if (line.startsWith('data:') || line === 'data') {
          const value = line === 'data' ? '' : line.slice(5).replace(/^ /, '');
          size += value.length + 1;
          if (size > MAX_EVENT) throw new Error('agr: SSE event is too large.');
          data.push(value);
        }
      }
      if (buffer.length + size > MAX_EVENT) throw new Error('agr: SSE event is too large.');
      if (done) break;
    }
    if (buffer.trim() || data.length) throw new Error('agr: incomplete SSE event.');
  } finally {
    await reader.cancel().catch(() => {});
    reader.releaseLock();
  }
}

export class AgrClient {
  constructor(private readonly baseUrl: string) {}

  private async request(path: string, signal: AbortSignal, body?: object): Promise<Response> {
    const url = apiUrl(this.baseUrl, path);
    let response: Response;
    try {
      response = await fetch(url, {
        method: body ? 'POST' : 'GET', redirect: 'manual',
        headers: { 'Content-Type': 'application/json', Accept: body && 'stream' in body ? 'text/event-stream' : 'application/json', 'anthropic-version': '2023-06-01' },
        body: body ? JSON.stringify(body) : undefined,
        signal: AbortSignal.any([signal, AbortSignal.timeout(body && 'stream' in body ? 600_000 : 30_000)])
      });
    } catch {
      signal.throwIfAborted();
      throw new Error('Cannot reach agr gateway (or request timed out). Start agr and check agr.baseUrl.');
    }
    if (!response.ok) {
      await response.body?.cancel();
      throw new Error(`agr /v1/${path}: HTTP ${response.status}. Check the gateway and upstream API support.`);
    }
    return response;
  }

  private async json(path: string, signal: AbortSignal, body?: object): Promise<any> {
    const response = await this.request(path, signal, body);
    const reader = response.body?.getReader();
    if (!reader) throw new Error(`agr: empty /v1/${path} response.`);
    let size = 0;
    const chunks: Uint8Array[] = [];
    try {
      while (true) {
        const { value, done } = await reader.read();
        if (done) break;
        size += value.length;
        if (size > MAX_EVENT) throw new Error('agr: JSON response is too large.');
        chunks.push(value);
      }
    } finally { await reader.cancel().catch(() => {}); reader.releaseLock(); }
    try { return JSON.parse(Buffer.concat(chunks).toString('utf8')); }
    catch { throw new Error(`agr: invalid JSON from /v1/${path}.`); }
  }

  async models(signal: AbortSignal): Promise<ModelEntry[]> {
    const body = await this.json('models', signal);
    if (!Array.isArray(body?.data) || body.data.some((m: ModelEntry) => !m || typeof m.id !== 'string' || !/^[^/]+\/.+/.test(m.id))) {
      throw new Error('agr: invalid model list from /v1/models.');
    }
    const models = new Map<string, ModelEntry>();
    for (const entry of body.data as ModelEntry[]) {
      if (!models.has(entry.id)) models.set(entry.id, entry);
    }
    return [...models.values()];
  }

  async countTokens(model: string, messages: Message[], signal: AbortSignal): Promise<number> {
    const body = await this.json('messages/count_tokens', signal, { model, messages: messagesBody(messages) });
    if (!Number.isSafeInteger(body?.input_tokens) || body.input_tokens < 0) throw new Error('agr: invalid token count from /v1/messages/count_tokens.');
    return body.input_tokens;
  }

  async *chat(api: Api, model: string, messages: Message[], maxOutput: number, tools: readonly Tool[], required: boolean, signal: AbortSignal): AsyncGenerator<Part> {
    const request = buildRequest(api, model, messages, maxOutput, tools, required);
    const response = await this.request(request.path, signal, request.body);
    if (!response.body || !response.headers.get('content-type')?.toLowerCase().includes('text/event-stream')) {
      await response.body?.cancel();
      throw new Error('agr: expected a text/event-stream response from the upstream.');
    }
    const decoder = new StreamDecoder(api);
    for await (const data of readSSE(response.body)) {
      signal.throwIfAborted();
      if (data === '[DONE]') break;
      let event: unknown;
      try { event = JSON.parse(data); } catch { throw new Error('agr: invalid JSON in upstream SSE event.'); }
      if (!event || typeof event !== 'object') throw new Error('agr: invalid upstream SSE event.');
      yield* decoder.push(event);
    }
    yield* decoder.finish();
  }
}
