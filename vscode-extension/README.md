# agr Model Provider

将 agr 网关中的模型加入 VS Code Chat 的模型选择器。通过 `GET /v1/models` 自动发现模型，完整保留 `provider/model` 路由名；支持 Chat Completions、Messages、Responses 的文本流式响应和工具调用。

## 安装和使用

需要 VS Code **1.104+**、可用的 Chat 界面，以及已经启动的 agr 网关。

```sh
agr start -d
code --install-extension agr-model-provider.vsix
```

打开 Chat 的模型选择器，进入 **Manage Models / Manage Language Models**，选择 **agr** 中的模型。也可以运行命令 **Chat: Manage Language Models**。如果尚未显示模型，运行 **agr: Refresh Models**。

默认连接 `http://localhost:9999`，所有发现的模型默认使用 `/v1/chat/completions`。通过 **agr: Configure Gateway** 修改地址或模型配置。API Key 由 agr 管理，插件无需读取 `~/.agr/config.toml`，也不保存上游凭据。

插件在本地 UI 扩展宿主运行：SSH / Dev Container 窗口中的 `localhost` 仍指运行 VS Code 的本机。若网关在另一台机器，修改地址或使用端口转发。

## 模型配置

在 VS Code 的**用户设置** JSON 中配置；这些设置不接受工作区覆盖：

```json
{
  "agr.baseUrl": "http://localhost:9999/v1",
  "agr.models": {
    "deepseek/deepseek-chat": {
      "api": "chat-completions",
      "maxInputTokens": 32768,
      "maxOutputTokens": 4096,
      "toolCalling": true
    },
    "mimo-anthropic/mimo-v2.5-pro": {
      "name": "Mimo Messages",
      "api": "messages"
    },
    "freemodel/gpt-5.5": {
      "api": "responses"
    },
    "unused/model": {
      "enabled": false
    }
  }
}
```

模型仍必须出现在 agr 的 `/v1/models` 列表中。`agr.models` 只覆盖元数据，不新增路由。agr 没有发布协议、上下文或工具能力元数据，因此插件不猜测具体模型能力：按上游实际支持的协议设置 `api`，不会在报错后自动切换协议或重试生成。

默认输入预算为 32768 tokens，输出预算为 4096 tokens，工具调用默认开启；请按所用上游的限制调整。`maxInputTokens` 是可用输入预算，应预留输出空间。非工具模型应配置 `toolCalling: false`。本版只支持文本和文本工具结果，不声明图片输入能力，也不提供行内代码补全。

### Token 计数要求

VS Code 的 Model Provider API 要求实现 token 计数。插件将计数请求发送到 agr 的 **`/v1/messages/count_tokens`**，使用同一个 `provider/model`，不会做本地估算或返回伪造计数。

**即使模型使用 Chat Completions 或 Responses，上游也必须支持该 Messages 计数接口，才能完整支持 VS Code 的上下文预算功能。** 上游返回 404/不支持时，插件会明确报错；仅有 Chat Completions/Responses 的上游目前不具备完整兼容性。请先确认所选路由可用：

```sh
curl http://localhost:9999/v1/messages/count_tokens \
  -H 'Content-Type: application/json' \
  -H 'anthropic-version: 2023-06-01' \
  -d '{"model":"provider/model","messages":[{"role":"user","content":"hello"}]}'
```

## 行为与限制

- 工具调用和工具结果由插件映射到所选原生 API；agr 继续原样代理该 API。
- 工具参数分片先拼接并校验，再交给 VS Code；截断流、无效 JSON 和上游错误会报错。
- 用户取消会中止 HTTP 请求；模型发现/计数超时 30 秒，生成请求超时 10 分钟。
- 单个 SSE 事件、JSON 响应和工具参数有 4 MiB 缓冲限制。
- reasoning/thinking 流不会混入正文。本版不回传 thinking 签名、Responses reasoning items 或提供商专有 reasoning 字段；要求保留这些字段的推理模型多轮工具流程不在本版支持范围内。
- 不透传任意 `modelOptions`；输出上限来自上述模型设置。HTTP 错误仅显示状态和路径，不显示上游敏感响应体。
- 请求不跟随重定向、不自动重试。Responses 请求使用 `store: false`。

## 开发和打包

在 `vscode-extension/` 下执行，构建需要 Node.js 22+：

```sh
npm ci
npm test
npm run compile
npm run package
```

产物为 `agr-model-provider.vsix`。在 VS Code 打开本目录后按 F5 可启动扩展开发宿主。

```sh
npm run test:host
```

宿主测试需要 Go 和 `code` CLI（也可通过 `VSCODE_EXECUTABLE` 指定），使用临时用户目录、独立扩展目录、本地模拟上游和真实 agr 网关，不依赖个人配置或真实 API Key。测试模型注册、三种原生 API、工具调用往返、计数及错误路径。单元/HTTP 集成测试对行、语句、函数和分支覆盖率均设置 80% 门槛。

API 依据：[VS Code Language Model Chat Provider](https://code.visualstudio.com/api/extension-guides/ai/language-model-chat-provider)、[Messages token counting](https://platform.claude.com/docs/en/api/messages/count_tokens)。
