const assert = require('node:assert/strict');
const { createServer } = require('node:http');
const { writeFileSync } = require('node:fs');
const { join } = require('node:path');
const { spawn } = require('node:child_process');
const { setTimeout: delay } = require('node:timers/promises');
const vscode = require('vscode');
const { AgrProvider } = require('../dist/provider');

const listen = server => new Promise(resolve => server.listen(0, '127.0.0.1', () => resolve(server.address().port)));
const close = server => new Promise(resolve => { server.closeAllConnections(); server.close(resolve); });
const sse = events => events.map(event => `data: ${JSON.stringify(event)}\n\n`).join('');

exports.run = async function () {
  const requests = [];
  const upstream = createServer(async (req, res) => {
    let raw = '';
    for await (const chunk of req) raw += chunk;
    const body = JSON.parse(raw);
    requests.push({ path: req.url, body, authorization: req.headers.authorization });
    if (req.url === '/v1/messages/count_tokens') return res.end('{"input_tokens":12}');
    if (body.model === 'error') { res.writeHead(429); return res.end('private upstream error'); }
    res.writeHead(200, { 'content-type': 'text/event-stream' });
    if (body.model === 'chat') {
      res.end(sse([
        { choices: [{ delta: { content: 'Hello from agr' } }] },
        { choices: [{ delta: { tool_calls: [{ index: 0, id: 'call1', function: { name: 'lookup', arguments: '{"q":"agr"}' } }] }, finish_reason: 'tool_calls' }] }
      ]) + 'data: [DONE]\n\n');
    } else if (body.model === 'messages') {
      res.end(sse([
        { type: 'content_block_delta', index: 0, delta: { type: 'text_delta', text: 'Hello from agr' } },
        { type: 'content_block_start', index: 1, content_block: { type: 'tool_use', id: 'call1', name: 'lookup', input: {} } },
        { type: 'content_block_delta', index: 1, delta: { type: 'input_json_delta', partial_json: '{"q":"agr"}' } },
        { type: 'content_block_stop', index: 1 }, { type: 'message_stop' }
      ]));
    } else {
      res.end(sse([
        { type: 'response.output_text.delta', delta: 'Hello from agr' },
        { type: 'response.output_item.done', item: { type: 'function_call', call_id: 'call1', name: 'lookup', arguments: '{"q":"agr"}' } },
        { type: 'response.completed', response: { status: 'completed' } }
      ]));
    }
  });
  let gateway;
  const provider = new AgrProvider();
  const source = new vscode.CancellationTokenSource();
  try {
    const upstreamPort = await listen(upstream);
    const reserved = createServer();
    const port = await listen(reserved);
    await close(reserved);
    const config = join(process.env.AGR_TEST_DIRECTORY, 'config.toml');
    writeFileSync(config, `[server]\nport = ${port}\n[[providers]]\nname = "fixture"\napi_base_url = "http://127.0.0.1:${upstreamPort}/v1"\napi_key = "fixture-only"\nmodels = ["chat", "messages", "responses", "error"]\n`);
    gateway = spawn(process.env.AGR_TEST_BINARY, [config], { stdio: 'ignore' });
    gateway.on('error', error => { console.error(error); });
    const baseUrl = `http://127.0.0.1:${port}`;
    let ready = false;
    for (let i = 0; i < 100; i++) {
      try { ready = (await fetch(baseUrl + '/health')).ok; } catch {}
      if (ready) break;
      if (gateway.exitCode !== null) throw new Error('Gateway fixture exited before becoming ready.');
      await delay(50);
    }
    assert.ok(ready, 'gateway starts');
    const settings = vscode.workspace.getConfiguration('agr');
    await settings.update('baseUrl', baseUrl, vscode.ConfigurationTarget.Global);
    await settings.update('models', { 'fixture/messages': { api: 'messages' }, 'fixture/responses': { api: 'responses' } }, vscode.ConfigurationTarget.Global);
    const extension = vscode.extensions.getExtension('rainke.agr-model-provider');
    assert.ok(extension, 'extension is loaded');
    await extension.activate();
    const registered = await vscode.lm.selectChatModels({ vendor: 'agr' });
    assert.equal(registered.length, 4, 'VS Code registry discovers all gateway models');
    const models = await provider.provideLanguageModelChatInformation({ silent: false }, source.token);
    const options = { toolMode: vscode.LanguageModelChatToolMode.Required, tools: [{ name: 'lookup', description: 'Look up a term', inputSchema: { type: 'object', properties: { q: { type: 'string' } } } }] };
    for (const model of models.filter(model => model.id !== 'fixture/error')) {
      assert.equal(await provider.provideTokenCount(model, 'Hello', source.token), 12);
      const parts = [];
      await provider.provideLanguageModelChatResponse(model, [vscode.LanguageModelChatMessage.User('Hello')], options, { report: part => parts.push(part) }, source.token);
      assert.ok(parts[0] instanceof vscode.LanguageModelTextPart);
      assert.equal(parts[0].value, 'Hello from agr');
      assert.ok(parts[1] instanceof vscode.LanguageModelToolCallPart);
      assert.deepEqual(parts[1].input, { q: 'agr' });
      await provider.provideLanguageModelChatResponse(model, [
        vscode.LanguageModelChatMessage.User('Hello'),
        vscode.LanguageModelChatMessage.Assistant(parts),
        vscode.LanguageModelChatMessage.User([new vscode.LanguageModelToolResultPart('call1', [new vscode.LanguageModelTextPart('Found')])])
      ], options, { report() {} }, source.token);
      const actual = requests.at(-1);
      assert.equal(actual.body.model, model.id.slice('fixture/'.length), 'agr rewrites the model');
      assert.equal(actual.authorization, 'Bearer fixture-only', 'agr owns credentials');
      const expectedPath = model.api === 'chat-completions' ? '/v1/chat/completions' : '/v1/' + model.api;
      assert.equal(actual.path, expectedPath);
      assert.ok(JSON.stringify(actual.body).includes('Found'), 'tool result reaches the native upstream');
    }
    await assert.rejects(provider.provideLanguageModelChatResponse(models.find(model => model.id === 'fixture/error'), [vscode.LanguageModelChatMessage.User('Hello')], options, { report() {} }, source.token), /HTTP 429/);
    await vscode.commands.executeCommand('agr.refreshModels');
    const message = 'PASS: VS Code model registration → real agr → native upstream; all three APIs, tool round trips, token counts and HTTP errors.';
    console.log(message);
    writeFileSync(join(process.env.AGR_TEST_DIRECTORY, 'test-result.json'), JSON.stringify({ status: 'passed', message }));
  } finally {
    provider.dispose(); source.dispose();
    if (gateway && gateway.exitCode === null) {
      const exited = new Promise(resolve => gateway.once('exit', resolve));
      gateway.kill('SIGTERM');
      await exited;
    }
    await close(upstream);
  }
};
