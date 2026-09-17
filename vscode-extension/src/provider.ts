import * as vscode from 'vscode';
import { AgrClient, type ModelEntry } from './client';
import { type Api, type InputPart, type Message } from './protocol';

interface ModelOverride { name?: string; api?: Api; maxInputTokens?: number; maxOutputTokens?: number; toolCalling?: boolean; enabled?: boolean }
export interface AgrModel extends vscode.LanguageModelChatInformation { readonly api: Api }

function modelInfo(entry: ModelEntry, override: ModelOverride): AgrModel {
  const api = override.api ?? 'chat-completions';
  const maxInputTokens = override.maxInputTokens ?? 32768;
  const maxOutputTokens = override.maxOutputTokens ?? 4096;
  if (!['chat-completions', 'messages', 'responses'].includes(api)
    || ![maxInputTokens, maxOutputTokens].every(n => Number.isSafeInteger(n) && n > 0)
    || (override.toolCalling !== undefined && typeof override.toolCalling !== 'boolean')
    || (override.name !== undefined && (typeof override.name !== 'string' || !override.name.trim()))
    || (override.enabled !== undefined && typeof override.enabled !== 'boolean')) {
    throw new Error(`Invalid agr.models configuration for ${entry.id}.`);
  }
  return {
    id: entry.id, name: override.name ?? entry.id, family: entry.id, version: '1.0.0', api,
    maxInputTokens, maxOutputTokens,
    detail: `agr · ${api}`, tooltip: entry.id,
    capabilities: { toolCalling: override.toolCalling ?? true, imageInput: false }
  };
}

function convertMessage(message: vscode.LanguageModelChatRequestMessage): Message {
  const content: InputPart[] = message.content.map(part => {
    if (part instanceof vscode.LanguageModelTextPart) return { type: 'text', text: part.value };
    if (part instanceof vscode.LanguageModelToolCallPart) return { type: 'call', id: part.callId, name: part.name, input: part.input };
    if (part instanceof vscode.LanguageModelToolResultPart) {
      const text = part.content.map(item => {
        if (!(item instanceof vscode.LanguageModelTextPart)) throw new Error('agr: unsupported tool result part; only text is supported.');
        return item.value;
      }).join('\n');
      return { type: 'result', id: part.callId, text };
    }
    throw new Error('agr: unsupported message part; this provider supports text and tool calls.');
  });
  return { role: message.role === vscode.LanguageModelChatMessageRole.Assistant ? 'assistant' : 'user', content };
}

async function cancellable<T>(token: vscode.CancellationToken, run: (signal: AbortSignal) => Promise<T>): Promise<T> {
  if (token.isCancellationRequested) throw new vscode.CancellationError();
  const controller = new AbortController();
  const listener = token.onCancellationRequested(() => controller.abort());
  try { return await run(controller.signal); }
  catch (error) { if (token.isCancellationRequested) throw new vscode.CancellationError(); throw error; }
  finally { listener.dispose(); }
}

export class AgrProvider implements vscode.LanguageModelChatProvider<AgrModel>, vscode.Disposable {
  private readonly changed = new vscode.EventEmitter<void>();
  readonly onDidChangeLanguageModelChatInformation = this.changed.event;

  refresh(): void { this.changed.fire(); }
  dispose(): void { this.changed.dispose(); }
  private client(): AgrClient { return new AgrClient(vscode.workspace.getConfiguration('agr').get('baseUrl', 'http://localhost:9999')); }

  async provideLanguageModelChatInformation(options: vscode.PrepareLanguageModelChatModelOptions, token: vscode.CancellationToken): Promise<AgrModel[]> {
    return cancellable(token, async signal => {
      let entries: ModelEntry[];
      try { entries = await this.client().models(signal); }
      catch (error) { if (options.silent && !signal.aborted) return []; throw error; }
      const overrides = vscode.workspace.getConfiguration('agr').get<Record<string, ModelOverride>>('models', {});
      return entries.filter(entry => overrides[entry.id]?.enabled !== false).map(entry => modelInfo(entry, overrides[entry.id] ?? {}));
    });
  }

  async provideLanguageModelChatResponse(model: AgrModel, messages: readonly vscode.LanguageModelChatRequestMessage[], options: vscode.ProvideLanguageModelChatResponseOptions, progress: vscode.Progress<vscode.LanguageModelResponsePart>, token: vscode.CancellationToken): Promise<void> {
    return cancellable(token, async signal => {
      if (options.tools?.length && !model.capabilities.toolCalling) throw new Error('agr: tool calling is disabled for this model.');
      const stream = this.client().chat(model.api, model.id, messages.map(convertMessage), model.maxOutputTokens, options.tools ?? [], options.toolMode === vscode.LanguageModelChatToolMode.Required, signal);
      for await (const part of stream) {
        progress.report(part.type === 'text' ? new vscode.LanguageModelTextPart(part.text) : new vscode.LanguageModelToolCallPart(part.id, part.name, part.input));
      }
    });
  }

  async provideTokenCount(model: AgrModel, text: string | vscode.LanguageModelChatRequestMessage, token: vscode.CancellationToken): Promise<number> {
    return cancellable(token, signal => this.client().countTokens(model.id, [typeof text === 'string'
      ? { role: 'user', content: [{ type: 'text', text }] }
      : convertMessage(text)], signal));
  }
}
