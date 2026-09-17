export enum LanguageModelChatMessageRole { User = 1, Assistant = 2 }
export enum LanguageModelChatToolMode { Auto = 1, Required = 2 }
export class LanguageModelTextPart { constructor(public value: string) {} }
export class LanguageModelToolCallPart { constructor(public callId: string, public name: string, public input: object) {} }
export class LanguageModelToolResultPart { constructor(public callId: string, public content: unknown[]) {} }
export class CancellationError extends Error { constructor() { super('Canceled'); } }
export class EventEmitter<T> {
  private listeners = new Set<(event: T) => void>();
  event = (listener: (event: T) => void) => { this.listeners.add(listener); return { dispose: () => this.listeners.delete(listener) }; };
  fire(value: T) { for (const listener of this.listeners) listener(value); }
  dispose() { this.listeners.clear(); }
}
export class CancellationTokenSource {
  private emitter = new EventEmitter<void>();
  token = { isCancellationRequested: false, onCancellationRequested: this.emitter.event };
  cancel() { this.token.isCancellationRequested = true; this.emitter.fire(); }
  dispose() { this.emitter.dispose(); }
}
export const settings: Record<string, unknown> = {};
export const configChanged = new EventEmitter<{ affectsConfiguration(key: string): boolean }>();
export const workspace = {
  getConfiguration: () => ({ get: (key: string, fallback: unknown) => settings[key] ?? fallback }),
  onDidChangeConfiguration: configChanged.event
};
export const registeredCommands = new Map<string, (...args: unknown[]) => unknown>();
export const commands = {
  registerCommand: (id: string, fn: (...args: unknown[]) => unknown) => { registeredCommands.set(id, fn); return { dispose: () => registeredCommands.delete(id) }; },
  executeCommand: async (...args: unknown[]) => args
};
export const providers = new Map<string, unknown>();
export const lm = {
  registerLanguageModelChatProvider: (id: string, provider: unknown) => { providers.set(id, provider); return { dispose: () => providers.delete(id) }; }
};
