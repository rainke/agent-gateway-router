# agr — AI 网关路由器

**agr** 是一个用 Go 编写的轻量级本地 AI 网关代理。它作为后台守护进程运行，位于本地 AI 客户端（Claude Code、Codex、VS Code Copilot）和上游 LLM 提供商之间，处理协议适配、模型路由、流式响应转发以及请求/响应转换。

## 架构

```
AI 客户端 (Claude Code / Codex / Copilot)
        │
        ▼
   localhost:9999
   ┌──────────────────────┐
   │       agr            │
   │                      │
   │  ┌──────┐ ┌───────┐  │
   │  │路由器│ │转换器│  │
   │  │      │ │ 链   │  │
   │  └──┬───┘ └───┬───┘  │
   │     │         │      │
   └─────┼─────────┼──────┘
         │         │
         ▼         ▼
   提供商 A   提供商 B   ...
```

当客户端发送请求时，agr 提取模型名称，通过路由器将其路由到配置的上游提供商，通过可配置的转换器链转换请求，将其转发到上游，然后转换响应并流式传输回客户端。

## 功能特性

- **多协议支持** — 代理 Claude Code（`/v1/messages`）和 Codex（`/v1/responses`）协议并进行协议转换
- **模型路由** — 将客户端请求的模型路由到不同的上游提供商。支持精确匹配并回退到默认值
- **转换器链** — 可配置的有序转换器管道（例如 `["openai", "deepseek"]`）用于请求/响应适配
- **流式传输** — SSE 流式响应转发，支持实时逐块转换
- **DeepSeek 集成** — 专用转换器，将 Anthropic thinking 块映射到 DeepSeek reasoning_content，反之亦然
- **守护进程管理** — `start`、`stop`、`restart` 命令，支持 PID 文件和优雅关闭（进行中的流有 30 秒超时）
- **TOML 配置** — 单一配置文件，启动时进行验证

## 快速开始

```bash
# 构建
go build -o agr .

# 前台启动
go run . start -c config.toml

# 作为守护进程启动
go run . start -c config.toml -d

# 覆盖端口
go run . start -c config.toml -p 9998

# 停止守护进程
go run . stop -c config.toml

# 重启
go run . restart -c config.toml
```

## 配置

```toml
[server]
port = 9999
log_level = "debug"
pid_file = "~/.agr/agr.pid"

[[providers]]
name = "deepseek"
api_base_url = "https://api.deepseek.com/chat/completions"
api_key = "sk-xxx"
models = ["deepseek-chat", "deepseek-coder"]
transformer = ["openai", "deepseek"]

[[providers]]
name = "eaichat"
api_base_url = "https://eaichat.ctyun.cn/ai/platform/v2/cp/chat/completions"
api_key = "sk-xxx"
models = ["glm-5-oc"]
transformer = ["openai"]

[router]
default = "deepseek,deepseek-chat"
"claude-3-5-sonnet" = "eaichat,glm-5-oc"
```

### 路由映射

格式：`client_model = "provider_name,upstream_model"`

- 先精确匹配，然后回退到 `router.default`
- 提供商和模型必须存在于 `[[providers]]` 部分

### 转换器链

内置转换器：

| 名称 | 用途 |
|------|------|
| `openai` | 在 Claude/Codex/OpenAI 协议和上游格式之间转换 |
| `deepseek` | 处理 DeepSeek 特有的 `reasoning_content` ↔ Anthropic thinking 映射 |

转换器按顺序执行请求处理，按逆序执行响应处理。

## 端点

| 路径 | 客户端 | 阶段 |
|------|--------|-------|
| `/v1/messages` | Claude Code | 1 |
| `/v1/responses` | Codex | 1 |
| `/api/chat` | VS Code Copilot (Ollama) | 2（计划中） |
| `/api/generate` | VS Code Copilot (Ollama) | 2（计划中） |
| `/api/tags` | VS Code Copilot (Ollama) | 2（计划中） |
| `/health` | 健康检查 | 1 |

第 2 阶段端点在当前版本中返回 `501 Not Implemented`。

## 项目结构

```
├── main.go              # 入口点
├── cmd/                 # Cobra 命令（start、stop、restart）
├── config/              # TOML 配置加载和验证
├── process/             # PID 文件管理和进程信号
├── server/              # HTTP 服务器
├── router/              # 模型到提供商的路由
├── proxy/               # 请求转发和流式传输
└── transformer/         # 协议适配转换器
```

## 开发

```bash
# 运行测试
go test ./...

# 聚焦测试
go test ./transformer -run TestDeepSeek

# 代码格式化
gofmt -l -w .
```

## 许可证
 
 ## 路线图
 
 | 上游 API | Claude Code | Codex | VS Code Copilot |
 |----------|:-----------:|:-----:|:---------------:|
 | OpenAI `/v1/chat/completions` | ✅ | ✅ | 🔲 |
 | Anthropic `/v1/messages` | — | 🔲 | 🔲 |
 
 > **说明：** Claude Code 原生使用 Anthropic `/v1/messages` API，因此无需将其列为该上游的客户端目标。

MIT
