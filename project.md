# 多模型智能路由代理工具 agr 技术方案与需求文档

## 一、需求概述

agr（AI Gateway Router）是一个基于 Go 语言开发的轻量级本地 CLI 运维网关。它常驻本地后台，面向 Claude Code、Codex、VS Code Copilot 等本地 AI 客户端，统一处理它们与多种后端大模型供应商之间的协议适配、模型路由、流式响应转发和请求响应拦截。

本方案采用分期交付：

1. **一期：核心网关能力**。先完成本地 CLI、后台进程生命周期、TOML 配置、多 Provider 路由、Transformer 链、Claude Code 与 Codex 代理能力。
2. **二期：Ollama 协议伪装**。在一期稳定后，新增 Ollama 兼容端点，让 VS Code Copilot 等支持自定义 Ollama 后端的客户端接入 agr。

一期不实现 Ollama 相关接口，避免核心代理链路和二期协议兼容能力耦合。

---

## 二、分期交付说明

### 一期目标

一期目标是交付一个可用、可配置、可扩展的本地网关底座：

- 支持 `agr start`、`agr stop`、`agr restart`。
- 支持 TOML 配置文件。
- 支持多个上游 Provider。
- 支持按模型名路由到指定 Provider 与真实模型。
- 支持 Transformer 数组拦截链。
- 支持 Claude Code 的 `/v1/messages` 代理。
- 支持 Codex 的 `/v1/responses` 代理。
- 支持流式响应转发与优雅停机。

### 二期目标

二期目标是在一期架构上追加 Ollama 兼容层：

- 支持 `/api/tags` 模型列表伪装。
- 支持 `/api/chat` Ollama Chat 请求转换。
- 支持 `/api/generate` Ollama Generate 请求转换。
- 支持 VS Code Copilot 将 agr 配置为 Ollama Base URL。
- 支持 Ollama 格式流式响应包装。

---

## 三、一期：CLI 与进程生命周期

工具名称固定为 **agr**。agr 使用 PID 文件和系统信号管理后台进程，默认 PID 文件路径为 `~/.agr/agr.pid`。

### 1. `agr start`

**功能**：启动本地网关代理服务器。

**工作流**：

1. 解析命令行参数。
2. 加载并校验 `config.toml`。
3. 检查端口占用与 PID 文件状态。
4. 根据 `--daemon` 决定前台运行或后台常驻。
5. 启动 HTTP 代理服务。

**常用参数**：

| 参数 | 说明 |
| --- | --- |
| `-c, --config` | 指定 TOML 配置文件路径 |
| `-p, --port` | 覆盖配置文件中的监听端口 |
| `-d, --daemon` | 以后台进程方式运行 |

### 2. `agr stop`

**功能**：停止正在运行的本地网关。

`stop` 根据 PID 文件定位 agr 后台进程，并发送停止信号。服务收到信号后进入优雅停机流程：停止接收新请求，等待正在输出的流式响应完成或超时，然后关闭 HTTP 服务并退出进程。

### 3. `agr restart`

**功能**：串行执行 `stop` 与 `start`。

`restart` 使用物理进程重启，不做热重载。这样可以彻底断开旧上游连接句柄，并确保新的配置、路由和 Transformer 链完整生效。

---

## 四、一期：TOML 配置中心规范

一期全面采用 TOML 作为配置格式。TOML 支持注释，适合本地手工维护。

```toml
# agr 智能路由网关核心配置文件

[server]
port = 9999
log_level = "info"
pid_file = "~/.agr/agr.pid"

[[providers]]
name = "eaichat"
api_base_url = "https://eaichat.ctyun.cn/ai/platform/v2/cp/chat/completions"
api_key = "sk-eaichat-secret-token-xxx"
models = ["glm-5-oc"]
transformer = ["openai"]

[[providers]]
name = "opencode-go"
api_base_url = "https://opencode.ai/zen/go/v1/messages"
api_key = "sk-xxx"
models = ["minimax-m2.7"]
transformer = ["openai"]

[router]
default = "eaichat,glm-5-oc"
glm-5 = "eaichat,glm-5-oc"
claude-3-5-sonnet = "opencode-go,minimax-m2.7"
```

### 配置字段说明

| 字段 | 说明 |
| --- | --- |
| `server.port` | 本地监听端口，默认 `9999` |
| `server.log_level` | 日志级别，建议支持 `debug`、`info`、`warn`、`error` |
| `server.pid_file` | PID 文件路径 |
| `providers.name` | Provider 唯一名称 |
| `providers.api_base_url` | 上游接口地址 |
| `providers.api_key` | 上游鉴权密钥 |
| `providers.models` | 该 Provider 支持的真实模型列表 |
| `providers.transformer` | 请求和响应需要经过的 Transformer 链 |
| `router.default` | 未命中模型映射时使用的默认路由 |
| `router.<model>` | 客户端请求模型到 `provider,model` 的映射 |

### 配置校验规则

- `providers.name` 必须唯一。
- `router` 中引用的 Provider 必须存在。
- `router` 中引用的真实模型必须出现在对应 Provider 的 `models` 中。
- `server.port` 必须是合法端口。
- `transformer` 中的名称必须能在内置 Transformer 注册表中找到。
- 配置错误必须在启动阶段失败并输出明确错误信息。

---

## 五、一期：接口代理与客户端识别

一期默认监听 `http://localhost:9999`，只暴露核心代理端点。

| 本地端点 | 目标客户端 | 一期行为 |
| --- | --- | --- |
| `/v1/messages` | Claude Code | 接收 Claude/Anthropic 风格请求，经过 Transformer 链后转发到目标 Provider |
| `/v1/responses` | Codex | 接收 OpenAI Responses 风格请求，经过 Transformer 链后转发到目标 Provider |

一期不实现以下 Ollama 端点：

- `/api/chat`
- `/api/generate`
- `/api/tags`

如果一期版本收到上述 Ollama 请求，应返回明确错误，例如：

```json
{
  "error": {
    "code": "feature_not_implemented",
    "message": "Ollama compatibility is planned for phase 2."
  }
}
```

建议 HTTP 状态码使用 `501 Not Implemented`。

---

## 六、一期：路由与代理流程

一期请求处理流程如下：

1. 接收客户端请求。
2. 根据请求路径识别客户端协议类型。
3. 从请求体中提取客户端模型名。
4. 通过 `[router]` 查找目标 Provider 和真实模型。
5. 找不到精确映射时使用 `router.default`。
6. 加载目标 Provider 配置。
7. 按 Provider 配置的 `transformer` 数组顺序执行请求转换。
8. 将转换后的请求转发到上游。
9. 接收上游响应。
10. 按相反方向执行响应转换。
11. 将响应返回给客户端。

路由映射格式统一为：

```text
客户端模型名 = "provider_name,upstream_model_name"
```

例如：

```toml
[router]
claude-3-5-sonnet = "opencode-go,minimax-m2.7"
```

含义是：当客户端请求模型 `claude-3-5-sonnet` 时，agr 实际转发到 `opencode-go` Provider，并使用真实模型 `minimax-m2.7`。

---

## 七、一期：Transformer 链架构

Transformer 用于处理不同客户端协议和不同上游 Provider 协议之间的转换。一期要求支持数组链，形成顺序执行的拦截链。

```go
type Transformer interface {
	TransformRequest(ctx context.Context, body []byte) ([]byte, error)
	TransformResponse(ctx context.Context, body []byte) ([]byte, error)
	TransformStream(ctx context.Context, chunk []byte) ([]byte, error)
}
```

一期内置 `openai` Transformer，并采用双轨分流：

```go
type OpenAIToCustomTransformer struct{}

func (t *OpenAIToCustomTransformer) TransformRequest(ctx context.Context, clientBody []byte) ([]byte, error) {
	path, _ := ctx.Value("request_path").(string)

	switch {
	case strings.Contains(path, "/v1/messages"):
		return t.transformClaudeRequest(clientBody)
	case strings.Contains(path, "/v1/responses"):
		return t.transformCodexRequest(clientBody)
	default:
		return clientBody, nil
	}
}
```

### Transformer 执行规则

- 请求方向按配置顺序执行：`transformer[0] -> transformer[1] -> ...`。
- 响应方向按反向顺序执行：`... -> transformer[1] -> transformer[0]`。
- 任一 Transformer 返回错误时，中断代理并向客户端返回明确错误。
- 流式响应必须逐 chunk 转换，不能等待完整响应结束后再输出。
- Transformer 不负责选择 Provider，Provider 路由由 Router 层完成。

---

## 八、一期：技术选型

| 模块 | 技术选型 |
| --- | --- |
| CLI 骨架 | `github.com/spf13/cobra` |
| 配置中心 | `github.com/spf13/viper` |
| 配置格式 | TOML |
| HTTP 服务 | `github.com/valyala/fasthttp` |
| 进程管理 | PID 文件 + 系统信号 |
| 日志 | 标准库 `log/slog` 或兼容结构化日志库 |

HTTP 框架需要重点验证 SSE 和长连接转发能力。如果 Gin 的流式处理能力满足需求，一期优先选择 Gin，降低实现复杂度；如果后续性能或连接控制不足，再切换到 Fasthttp。

---

## 九、一期：验收标准

一期完成后应满足以下标准：

- `agr start` 可以按配置启动服务。
- `agr start -p 9998` 可以覆盖配置端口。
- `agr stop` 可以停止后台进程。
- `agr restart` 可以完成物理进程重启。
- PID 文件能正确创建、读取和清理。
- 非法 TOML、重复 Provider、非法路由映射能在启动阶段报错。
- `/v1/messages` 能按模型名路由到目标 Provider。
- `/v1/responses` 能按模型名路由到目标 Provider。
- 未命中模型时能使用 `router.default`。
- Transformer 链按配置顺序执行。
- 流式响应可以边接收边返回给客户端。
- 优雅停机时不主动截断已经开始输出的流式响应，除非超过关闭超时。
- `/api/chat`、`/api/generate`、`/api/tags` 在一期返回 `501 Not Implemented`。

---

## 十、二期：Ollama 协议伪装

二期在一期网关架构稳定后实现 Ollama 兼容层。目标是让 VS Code Copilot 等支持自定义 Ollama 后端的客户端，将 agr 配置为 Ollama Base URL 后能够发现模型、发起请求并接收兼容响应。

VS Code Copilot 的 Ollama Base URL 可配置为：

```text
http://localhost:9999
```

---

## 十一、二期：接口设计

二期新增以下端点：

| 本地端点 | 目标客户端 | 二期行为 |
| --- | --- | --- |
| `/api/tags` | Ollama 客户端握手 | 返回 agr 配置中声明的 Ollama 展示模型列表 |
| `/api/chat` | VS Code Copilot Chat | 接收 Ollama Chat 请求，路由后转发到目标 Provider |
| `/api/generate` | VS Code Copilot Inline Complete | 接收 Ollama Generate 请求，转换为目标上游支持的请求 |

### `/api/tags`

`/api/tags` 不请求任何上游。agr 根据配置生成 Ollama 兼容模型列表。

响应示例：

```json
{
  "models": [
    {
      "name": "qwen2.5-coder:7b",
      "modified_at": "2026-05-22T09:00:00Z",
      "size": 4700000000,
      "digest": "sha256:mock123456"
    },
    {
      "name": "deepseek-coder:16b",
      "modified_at": "2026-05-22T09:00:00Z",
      "size": 9000000000,
      "digest": "sha256:mock789101"
    }
  ]
}
```

模型列表不建议硬编码在业务逻辑中，应优先从配置生成。

### `/api/chat`

Ollama Chat 请求示例：

```json
{
  "model": "qwen2.5-coder:7b",
  "messages": [
    {
      "role": "user",
      "content": "写一个 Go 管道"
    }
  ],
  "stream": true
}
```

处理规则：

- 读取请求体中的 `model`。
- 使用 Router 找到目标 Provider 和真实模型。
- 将 Ollama 请求转换为目标上游可接受的请求格式。
- 如果上游返回流式 OpenAI chunk，则转换为 Ollama chunk。
- 未配置模型时返回明确错误。

### `/api/generate`

Ollama Generate 请求示例：

```json
{
  "model": "qwen2.5-coder:7b",
  "prompt": "Write a Go function that merges two channels.",
  "stream": true
}
```

处理规则：

- 读取 `model` 与 `prompt`。
- 将 `prompt` 转换为目标上游支持的消息或补全格式。
- 流式响应转换为 Ollama Generate chunk。
- 非流式响应返回完整文本。

---

## 十二、二期：Ollama 转换规则

二期将 `openai` Transformer 扩展为四轨分流：

```go
type OpenAIToCustomTransformer struct{}

func (t *OpenAIToCustomTransformer) TransformRequest(ctx context.Context, clientBody []byte) ([]byte, error) {
	path, _ := ctx.Value("request_path").(string)

	switch {
	case strings.Contains(path, "/v1/messages"):
		return t.transformClaudeRequest(clientBody)
	case strings.Contains(path, "/v1/responses"):
		return t.transformCodexRequest(clientBody)
	case strings.Contains(path, "/api/chat"):
		return t.transformOllamaChatRequest(clientBody)
	case strings.Contains(path, "/api/generate"):
		return t.transformOllamaGenerateRequest(clientBody)
	default:
		return clientBody, nil
	}
}
```

### 流式响应转换

OpenAI 风格流式响应通常包含：

```json
{
  "choices": [
    {
      "delta": {
        "content": "hello"
      }
    }
  ]
}
```

Ollama Chat 流式响应需要转换为：

```json
{
  "model": "qwen2.5-coder:7b",
  "message": {
    "role": "assistant",
    "content": "hello"
  },
  "done": false
}
```

流结束时输出：

```json
{
  "model": "qwen2.5-coder:7b",
  "done": true
}
```

---

## 十三、二期：配置扩展建议

二期建议增加独立 `[ollama]` 配置块，避免 Ollama 展示模型元信息与一期核心路由配置混杂。

```toml
[ollama]
enabled = true

[[ollama.models]]
name = "qwen2.5-coder:7b"
route = "eaichat,glm-5-oc"
size = 4700000000
digest = "sha256:mock123456"

[[ollama.models]]
name = "deepseek-coder:16b"
route = "opencode-go,minimax-m2.7"
size = 9000000000
digest = "sha256:mock789101"
```

二期实现时，Ollama 模型也可以同步注册到 Router，但配置层建议保留 Ollama 独立块，用于维护 `/api/tags` 所需的展示字段。

---

## 十四、二期：验收标准

二期完成后应满足以下标准：

- `/api/tags` 返回 Ollama 兼容模型列表。
- 模型列表来自配置，而不是硬编码。
- VS Code Copilot 能通过 `http://localhost:9999` 发现模型。
- `/api/chat` 支持流式与非流式请求。
- `/api/generate` 支持流式与非流式请求。
- Ollama 请求中的模型名能正确映射到真实 Provider 和模型。
- OpenAI 风格流式响应能转换为 Ollama Chat chunk。
- Generate 响应能转换为 Ollama Generate 格式。
- 未配置的 Ollama 模型返回明确错误。
- 二期新增能力不破坏一期 `/v1/messages` 与 `/v1/responses` 行为。

---

## 十五、风险与约束

- 不同客户端对流式响应格式要求不同，Transformer 必须按路径和客户端协议隔离处理。
- 上游 Provider 的接口不一定完全兼容 OpenAI 格式，需要通过 Transformer 层吸收差异。
- 优雅停机需要兼顾用户体验和进程退出时间，建议设置最大关闭超时。
- 二期 Ollama 兼容层需要实测 VS Code Copilot 的握手与请求行为，避免只按 Ollama 文档实现但无法被 Copilot 正确识别。
- API Key 属于敏感信息，日志中不得输出完整密钥。
- 配置文件中的 `~` 路径需要在加载阶段展开为用户目录。
