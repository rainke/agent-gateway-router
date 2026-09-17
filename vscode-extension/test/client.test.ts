import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { createServer, type Server, type IncomingMessage, type ServerResponse } from 'node:http';
import { AgrClient, apiUrl, readSSE } from '../src/client';

let server: Server;
let baseUrl: string;
let handler: (req: IncomingMessage, res: ServerResponse) => void;
beforeEach(async () => {
  server = createServer((req, res) => handler(req, res));
  await new Promise<void>(resolve => server.listen(0, '127.0.0.1', resolve));
  baseUrl = `http://127.0.0.1:${(server.address() as { port: number }).port}`;
});
afterEach(async () => { server.closeAllConnections(); await new Promise<void>(resolve => server.close(() => resolve())); });
const collect = async <T>(items: AsyncIterable<T>) => { const result: T[] = []; for await (const i of items) result.push(i); return result; };

describe('HTTP client', () => {
  it.each(['http://localhost:9999', 'http://localhost:9999/', 'http://localhost:9999/v1', 'http://localhost:9999/v1/'])('normalizes %s', url => {
    expect(apiUrl(url, 'models')).toBe('http://localhost:9999/v1/models');
  });
  it.each(['file:///tmp/a', 'http://user:secret@localhost:9999', 'http://localhost:9999/?key=secret', 'bad'])('rejects unsafe/malformed configuration %s', url => {
    expect(() => apiUrl(url, 'models')).toThrow(/URL/i);
  });
  it('discovers exact qualified model IDs and deduplicates', async () => {
    handler = (req, res) => { expect(req.url).toBe('/v1/models'); res.end(JSON.stringify({ data: [{ id: 'p/org/m', owned_by: 'p' }, { id: 'p/org/m' }, { id: 'q/m' }] })); };
    expect(await new AgrClient(baseUrl).models(new AbortController().signal)).toEqual([{ id: 'p/org/m', owned_by: 'p' }, { id: 'q/m' }]);
  });
  it('uses upstream token counting and preserves model ID', async () => {
    handler = async (req, res) => { let raw = ''; for await (const c of req) raw += c; expect(req.url).toBe('/v1/messages/count_tokens'); expect(JSON.parse(raw)).toMatchObject({ model: 'p/m', messages: [{ role: 'user', content: [{ type: 'text', text: '你好' }] }] }); res.end('{"input_tokens":7}'); };
    expect(await new AgrClient(baseUrl).countTokens('p/m', [{ role: 'user', content: [{ type: 'text', text: '你好' }] }], new AbortController().signal)).toBe(7);
  });
  it('reports HTTP errors without revealing response body', async () => {
    handler = (_, res) => { res.writeHead(401); res.end('secret key'); };
    await expect(new AgrClient(baseUrl).models(new AbortController().signal)).rejects.toThrow(/HTTP 401/);
  });
  it('does not follow upstream redirects', async () => {
    handler = (_, res) => { res.writeHead(302, { location: '/elsewhere' }); res.end(); };
    await expect(new AgrClient(baseUrl).models(new AbortController().signal)).rejects.toThrow(/HTTP 302/);
  });
  it('rejects malformed discovery and token responses', async () => {
    handler = (_, res) => res.end('{}');
    await expect(new AgrClient(baseUrl).models(new AbortController().signal)).rejects.toThrow(/model/i);
    await expect(new AgrClient(baseUrl).countTokens('p/m', [], new AbortController().signal)).rejects.toThrow(/token/i);
  });
  it('streams chunks without waiting for completion and cancels the connection', async () => {
    handler = (_, res) => { res.writeHead(200, { 'content-type': 'text/event-stream' }); res.write('data: {"choices":[{"delta":{"content":"Hi"}}]}\n\n'); };
    const controller = new AbortController();
    const stream = new AgrClient(baseUrl).chat('chat-completions', 'p/m', [], 8, [], false, controller.signal)[Symbol.asyncIterator]();
    expect((await stream.next()).value).toEqual({ type: 'text', text: 'Hi' });
    controller.abort();
    await expect(stream.next()).rejects.toThrow();
  });
});

describe('SSE parser', () => {
  it('handles CRLF, comments, multiline data and split UTF-8', async () => {
    const bytes = new TextEncoder().encode(': ping\r\nevent: x\r\ndata: {"text":\r\ndata: "你好"}\r\n\r\ndata: [DONE]\n\n');
    const stream = new ReadableStream<Uint8Array>({ start(c) { for (const byte of bytes) c.enqueue(new Uint8Array([byte])); c.close(); } });
    expect(await collect(readSSE(stream))).toEqual(['{"text":\n"你好"}', '[DONE]']);
  });
  it('bounds incomplete events', async () => {
    const stream = new ReadableStream<Uint8Array>({ start(c) { c.enqueue(new TextEncoder().encode('data: ' + 'x'.repeat(4 * 1024 * 1024 + 1))); c.close(); } });
    await expect(collect(readSSE(stream))).rejects.toThrow(/large/i);
  });
});
