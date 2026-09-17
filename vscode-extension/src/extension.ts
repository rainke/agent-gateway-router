import * as vscode from 'vscode';
import { AgrProvider } from './provider';

export function activate(context: vscode.ExtensionContext): void {
  const provider = new AgrProvider();
  context.subscriptions.push(
    provider,
    vscode.lm.registerLanguageModelChatProvider('agr', provider),
    vscode.commands.registerCommand('agr.configure', () => vscode.commands.executeCommand('workbench.action.openSettings', '@ext:rainke.agr-model-provider')),
    vscode.commands.registerCommand('agr.refreshModels', () => provider.refresh()),
    vscode.workspace.onDidChangeConfiguration(event => { if (event.affectsConfiguration('agr')) provider.refresh(); })
  );
}
