import { afterEach, describe, expect, it, vi } from 'vitest';
import * as vscode from 'vscode';
import { AgrProvider } from '../src/provider';
import { activate } from '../src/extension';
import { settings, configChanged, registeredCommands, providers } from './vscode';

afterEach(() => { vi.unstubAllGlobals(); for (const key of Object.keys(settings)) delete settings[key]; });
const token = () => new vscode.CancellationTokenSource().token;
const modelResponse = () => new Response(JSON.stringify({ data: [{ id: 'p/org/m', owned_by: 'p' }, { id: 'q/m', owned_by: 'q' }] }));
const message = (content: unknown[], role = vscode.LanguageModelChatMessageRole.User) => ({ role, content, name: undefined });

describe('VS Code provider', () => {
  it('discovers models silently, merges overrides and refreshes metadata', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => modelResponse()));
    settings.models = { 'p/org/m': { name: 'My model', api: 'messages', maxInputTokens: 10000, maxOutputTokens: 1000, toolCalling: false }, 'q/m': { enabled: false } };
    const provider = new AgrProvider();
    const models = await provider.provideLanguageModelChatInformation({ silent: true }, token());
    expect(models).toHaveLength(1);
    expect(models[0]).toMatchObject({ id: 'p/org/m', name: 'My model', api: 'messages', maxInputTokens: 10000, maxOutputTokens: 1000, capabilities: { toolCalling: false, imageInput: false } });
    const changed = vi.fn();
    provider.onDidChangeLanguageModelChatInformation(changed);
    provider.refresh();
    expect(changed).toHaveBeenCalledOnce();
    provider.dispose();
  });
  it('keeps silent discovery quiet when the gateway is offline, but reports explicit discovery errors', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => { throw new TypeError('fetch failed'); }));
    const provider = new AgrProvider();
    expect(await provider.provideLanguageModelChatInformation({ silent: true }, token())).toEqual([]);
    await expect(provider.provideLanguageModelChatInformation({ silent: false }, token())).rejects.toThrow(/agr/i);
    provider.dispose();
  });
  it.each([{ api: 'invalid' }, { maxInputTokens: 0 }, { maxOutputTokens: 1.5 }, { toolCalling: 'yes' }])('rejects invalid model overrides %j', async override => {
    vi.stubGlobal('fetch', vi.fn(async () => modelResponse()));
    settings.models = { 'p/org/m': override };
    const p = new AgrProvider();
    await expect(p.provideLanguageModelChatInformation({ silent: false }, token())).rejects.toThrow(/agr.models/);
    p.dispose();
  });
  it('converts text and tool history and reports native VS Code response parts', async () => {
    const requests: any[] = [];
    vi.stubGlobal('fetch', vi.fn(async (url, init) => {
      if (String(url).endsWith('/models')) return modelResponse();
      requests.push(JSON.parse(init.body));
      return new Response('data: {"choices":[{"delta":{"content":"Hi"}}]}\n\ndata: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c2","function":{"name":"search","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}\n\ndata: [DONE]\n\n', { headers: { 'content-type': 'text/event-stream' } });
    }));
    const p = new AgrProvider();
    const [model] = await p.provideLanguageModelChatInformation({ silent: false }, token());
    const parts: unknown[] = [];
    await p.provideLanguageModelChatResponse(model, [
      message([new vscode.LanguageModelTextPart('hello')]),
      message([new vscode.LanguageModelToolCallPart('c1', 'search', {})], vscode.LanguageModelChatMessageRole.Assistant),
      message([new vscode.LanguageModelToolResultPart('c1', [new vscode.LanguageModelTextPart('done')])])
    ], { toolMode: vscode.LanguageModelChatToolMode.Required, tools: [{ name: 'search', description: 'Search' }] }, { report: part => parts.push(part) }, token());
    expect(parts).toEqual([new vscode.LanguageModelTextPart('Hi'), new vscode.LanguageModelToolCallPart('c2', 'search', {})]);
    expect(requests[0]).toMatchObject({ model: 'p/org/m', tool_choice: 'required' });
    expect(requests[0].messages[2]).toEqual({ role: 'tool', tool_call_id: 'c1', content: 'done' });
    p.dispose();
  });
  it('uses upstream counts for strings and tool messages, with no fallback estimate', async () => {
    vi.stubGlobal('fetch', vi.fn(async url => String(url).endsWith('/models') ? modelResponse() : new Response('{"input_tokens":9}')));
    const p = new AgrProvider();
    const [model] = await p.provideLanguageModelChatInformation({ silent: true }, token());
    expect(await p.provideTokenCount(model, 'hello', token())).toBe(9);
    expect(await p.provideTokenCount(model, message([new vscode.LanguageModelToolResultPart('c1', [new vscode.LanguageModelTextPart('done')])]), token())).toBe(9);
    vi.stubGlobal('fetch', vi.fn(async () => new Response('', { status: 404 })));
    await expect(p.provideTokenCount(model, 'hello', token())).rejects.toThrow(/count_tokens/);
    p.dispose();
  });
  it('rejects unsupported parts and honors pre-cancellation', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => modelResponse()));
    const p = new AgrProvider();
    const [model] = await p.provideLanguageModelChatInformation({ silent: true }, token());
    await expect(p.provideTokenCount(model, message([{ mimeType: 'image/png', data: new Uint8Array() }]), token())).rejects.toThrow(/unsupported/i);
    const cancelled = new vscode.CancellationTokenSource();
    cancelled.cancel();
    await expect(p.provideTokenCount(model, 'hello', cancelled.token)).rejects.toThrow(/cancel/i);
    p.dispose();
  });
  it('aborts an in-flight HTTP request when VS Code cancels', async () => {
    const source = new vscode.CancellationTokenSource();
    vi.stubGlobal('fetch', vi.fn((_, init) => new Promise((_, reject) => {
      init.signal.addEventListener('abort', () => reject(new Error('abort')));
      source.cancel();
    })));
    const p = new AgrProvider();
    await expect(p.provideLanguageModelChatInformation({ silent: false }, source.token)).rejects.toThrow(/cancel/i);
    p.dispose();
  });
  it('registers provider, settings management, refresh and disposal', async () => {
    const context = { subscriptions: [] as vscode.Disposable[] };
    activate(context as vscode.ExtensionContext);
    const p = providers.get('agr') as AgrProvider;
    const change = vi.fn();
    p.onDidChangeLanguageModelChatInformation(change);
    configChanged.fire({ affectsConfiguration: key => key === 'agr' });
    configChanged.fire({ affectsConfiguration: () => false });
    await registeredCommands.get('agr.refreshModels')!();
    expect(change).toHaveBeenCalledTimes(2);
    expect(await registeredCommands.get('agr.configure')!()).toEqual(['workbench.action.openSettings', '@ext:rainke.agr-model-provider']);
    for (const d of context.subscriptions) d.dispose();
    expect(providers.has('agr')).toBe(false);
  });
});
