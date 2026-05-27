# agr — AI Gateway Router

**agr** 是一个用 Go 编写的轻量级本地 AI 网关代理。它作为后台守护进程运行，位于本地 AI 客户端（Claude Code、Codex、VS Code Copilot）和上游 LLM 提供商之间，处理协议适配、模型路由、流式响应转发以及请求/响应转换。

## 为什么需要 agr？

| 痛点 | agr 的解决方案 |
|------|---------------|
| 不同 AI 客户端使用不同协议（Anthropic Messages vs OpenAI Responses vs OpenAI Chat） | 自动协议转换，同一端口同时服务 Claude Code 和 Codex |
| 想把 Claude Code 的请求转发到 DeepSeek / GLM / Mimo 等国产模型 | 声明式路由配置，一条映射搞定 |
| 每个客户端都要单独配置 API Key 和 Base URL | 统一网关入口，客户端只需指向 `localhost:9999` |
| DeepSeek 的 thinking 格式与 Anthropic 不兼容 | 转换器链自动处理 `reasoning_content` ↔ `thinking` 映射 |
| 需要在多个提供商之间切换或做 fallback | 按模型名精确路由，未命中时回退到默认提供商 |

## 架构总览

```
┌─────────────────┐  ┌──────────────┐  ┌─────────────────┐
│   Claude Code   │  │    Codex     │  │  VS Code Copilot │
│  (/v1/messages) │  │(/v1/responses)│  │  (Phase 2)       │
└────────┬────────┘  └──────┬───────┘  └────────┬─────────┘
         │                  │                    │
         ▼                  ▼                    ▼
    ┌──────────────────────────────────────────────────┐
    │                  localhost:9999                   │
    │                                                  │
    │   ┌────────┐   ┌─────────┐   ┌──────────────┐   │
    │   │ Router │──▶│Transform│──▶│   Proxy      │   │
    │   │        │   │  Chain  │   │  (SSE stream)│   │
    │   └────────┘   └─────────┘   └──────┬───────┘   │
    │                                     │           │
    └─────────────────────────────────────┼───────────┘
                                          │
                    ┌─────────────────────┼─────────────────────┐
                    ▼                     ▼                     ▼
            ┌──────────────┐    ┌──────────────┐    ┌──────────────┐
            │   DeepSeek   │    │  GLM / Mimo  │    │  FreeModel   │
            │  (OpenAI API) │    │ (OpenAI API) │    │(Responses API)│
            └──────────────┘    └──────────────┘    └──────────────┘
```

请求处理流程：
1. 客户端发送请求到 `localhost:9999`
2. agr 根据请求路径识别协议类型（Messages / Responses）
3. 从请求体中提取模型名称
4. Router 根据配置将 `client_model` 映射到 `provider,upstream_model`
5. Transformer 链按顺序转换请求格式
6. 转发到上游提供商，逐 chunk 流式转换响应并返回给客户端

## 功能特性

- **多协议支持** — 同时代理 Claude Code（Anthropic Messages API）和 Codex（OpenAI Responses API）
- **智能路由** — 按模型名精确路由到不同上游提供商，支持默认回退
- **转换器链** — 可配置的有序转换器管道，请求方向顺序执行，响应方向逆序执行
- **SSE 流式传输** — 实时逐 chunk 转换，不缓冲完整响应
- **DeepSeek thinking 映射** — 自动处理 Anthropic thinking ↔ DeepSeek reasoning_content
- **Codex 流式扩展** — `CodexStreamTransformer` 接口支持单个上游 chunk 映射多个下游 SSE 事件
- **守护进程管理** — `start`/`stop`/`restart`，PID 文件管理，优雅停机（30 秒超时）
- **TOML 配置** — 启动时严格校验，配置错误立即失败并给出明确提示

## 安装

### 一键安装（macOS / Linux）

```bash
curl -fsSL https://raw.githubusercontent.com/rainke/agent-gateway-router/main/install.sh | sh
```

### 手动安装

从 [GitHub Releases](https://github.com/rainke/agent-gateway-router/releases/latest) 下载对应平台的二进制文件：

```bash
chmod +x agr-*
sudo mv agr-* /usr/local/bin/agr

# macOS：如果被 Gatekeeper 拦截
xattr -d com.apple.quarantine /usr/local/bin/agr
```

### 从源码构建

```bash
git clone https://github.com/rainke/agent-gateway-router.git
cd agent-gateway-router
go build -o agr .
```

### go install

```bash
go install github.com/rainke/agent-gateway-router@latest
```

## 快速开始

### 1. 创建配置文件

```bash
mkdir -p ~/.agr
```

创建 `~/.agr/config.toml`：

```toml
[server]
port = 9999
log_level = "info"
pid_file = "~/.agr/agr.pid"

# 定义上游提供商
[[providers]]
name = "deepseek"
api_base_url = "https://api.deepseek.com/chat/completions"
api_key = "sk-your-key-here"
models = ["deepseek-chat"]
transformer = ["openai", "deepseek"]

# 路由：客户端请求任何模型时，都转发到 DeepSeek
[router]
default = "deepseek,deepseek-chat"
```

### 2. 启动网关

```bash
# 前台启动（便于调试）
agr start

# 后台守护进程模式
agr start -d

# 指定端口
agr start -p 8080

# 指定配置文件
agr start -c /path/to/config.toml
```

### 3. 配置客户端

将客户端的 API Base URL 指向 `http://localhost:9999`，详见下方客户端配置章节。

### 4. 管理服务

```bash
agr stop      # 停止
agr restart   # 重启
```

## 配置详解

### 完整配置示例

```toml
[server]
port = 9999
log_level = "info"              # debug | info | warn | error
pid_file = "~/.agr/agr.pid"
models_config = "models_config.json"  # 可选，Codex 模型元数据

# ── 提供商定义 ──────────────────────────────────────────────

# 提供商 1：智谱 GLM（OpenAI 兼容接口）
[[providers]]
name = "zhipu"
api_base_url = "https://api.zhipu.example.com/v1/chat/completions"
api_key = "your-zhipu-key"
models = ["glm-5-oc"]
transformer = ["openai"]

# 提供商 2：DeepSeek（需要 thinking 映射）
[[providers]]
name = "deepseek"
api_base_url = "https://api.deepseek.com/chat/completions"
api_key = "sk-your-deepseek-key"
models = ["deepseek-chat"]
transformer = ["openai", "deepseek"]

# 提供商 3：Mimo（OpenAI 兼容接口）
[[providers]]
name = "mimo"
api_base_url = "https://api.mimo.example.com/v1/chat/completions"
api_key = "your-mimo-key"
models = ["mimo-v2.5-pro"]
transformer = ["openai"]

# 提供商 4：Mimo（Anthropic 兼容接口，供 Claude Code 使用）
[[providers]]
name = "mimo-anthropic"
api_base_url = "https://api.mimo.example.com/anthropic/v1/messages"
api_key = "your-mimo-key"
models = ["mimo-v2.5-pro"]
transformer = ["anthropic"]

# 提供商 5：支持 Responses API 的提供商（供 Codex 使用）
[[providers]]
name = "freemodel"
api_base_url = "https://api.freemodel.example.com/responses"
api_key = "your-freemodel-key"
models = ["gpt-5.5", "gpt-5.3-codex"]
transformer = ["openai-responses"]

# ── 路由映射 ────────────────────────────────────────────────

[router]
# 格式：客户端模型名 = "提供商名,上游真实模型名"

# 默认路由：未匹配的模型走这条路
default = "zhipu,glm-5-oc"

# 按模型名精确路由
"glm-5"                    = "zhipu,glm-5-oc"
"mimo-v2.5-pro"            = "mimo,mimo-v2.5-pro"
"mimo-v2.5-pro-anthropic"  = "mimo-anthropic,mimo-v2.5-pro"
"gpt-5.5"                  = "freemodel,gpt-5.5"
```

### 配置字段说明

#### `[server]`

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `port` | int | `9999` | 本地监听端口 |
| `log_level` | string | `"info"` | 日志级别：`debug` / `info` / `warn` / `error` |
| `pid_file` | string | `"~/.agr/agr.pid"` | PID 文件路径 |
| `models_config` | string | — | 模型元数据配置文件路径（相对于 `~/.agr/`） |

#### `[[providers]]`

| 字段 | 类型 | 说明 |
|------|------|------|
| `name` | string | 提供商唯一名称，在路由中引用 |
| `api_base_url` | string | 上游 API 完整地址 |
| `api_key` | string | 上游 API 密钥 |
| `models` | []string | 该提供商支持的模型列表 |
| `transformer` | []string | 转换器链，按数组顺序执行 |

#### `[router]`

| 字段 | 格式 | 说明 |
|------|------|------|
| `default` | `"provider,model"` | 未匹配时的默认路由 |
| `<model_name>` | `"provider,model"` | 客户端请求该模型名时的精确路由 |

### 配置校验规则

agr 在启动时执行严格校验，以下情况会直接报错退出：

- `providers.name` 重复
- `router` 中引用的提供商不存在
- `router` 中引用的模型不在对应提供商的 `models` 列表中
- `transformer` 中的名称不在内置注册表中
- 端口号不合法

## 客户端集成

### Claude Code 配置

编辑 `~/.claude/settings.json`：

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "http://localhost:9999",
    "ANTHROPIC_AUTH_TOKEN": "your-auth-token"
  },
  "model": "glm-5"
}
```

> **提示**：Claude Code 走 `/v1/messages` 端点，使用 Anthropic Messages 协议。agr 会自动将其转换为上游提供商所需的格式。

如果上游提供商本身支持 Anthropic 协议（如 Mimo 的 Anthropic 兼容接口），使用 `transformer = ["anthropic"]` 配置，agr 会直接透传 Messages 请求：

```toml
[[providers]]
name = "mimo-anthropic"
api_base_url = "https://api.mimo.example.com/anthropic/v1/messages"
api_key = "your-key"
models = ["mimo-v2.5-pro"]
transformer = ["anthropic"]
```

### Codex 配置

编辑 `~/.codex/config.toml`，添加 agr 作为 model provider：

```toml
[model_providers.agr]
name = "AgentGateway"
base_url = "http://localhost:9999/v1"
wire_api = "responses"
requires_openai_auth = false
```

创建 `~/.codex/agr.config.toml`，保存 agr profile：

```toml
model = "mimo-v2.5-pro"
model_provider = "agr"
model_reasoning_effort = "medium"
```

启动 Codex 时指定 agr profile：

```bash
codex -p agr
```


> **提示**：Codex 0.134.0 起，`-p agr` 会在 `~/.codex/config.toml` 之上叠加读取 `~/.codex/agr.config.toml`，不再读取 `~/.codex/config.toml` 中的 `[profiles.agr]`。Codex 走 `/v1/responses` 端点，使用 OpenAI Responses API 协议。如果上游提供商支持 Responses API（如 FreeModel），使用 `transformer = ["openai-responses"]` 配置。

### Codex 模型元数据（models_config.json）

Codex 在启动时调用 `/v1/models` 发现可用模型。`models_config.json` 让你精确声明每个模型的能力，使 Codex 显示正确的推理级别、上下文窗口等控件。

创建 `~/.agr/models_config.json`：

```json
{
  "models": [
    {
      "slug": "glm-5",
      "display_name": "GLM-5-OC",
      "description": "智谱 GLM-5-OC 大语言模型，支持长上下文对话与工具调用",
      "supported_reasoning_levels": [
        { "effort": "low", "description": "Low effort thinking" },
        { "effort": "medium", "description": "Medium effort thinking" },
        { "effort": "high", "description": "High effort thinking" }
      ],
      "context_window": 204800,
      "max_context_window": 204800,
      "auto_compact_token_limit": 153600,
      "input_modalities": ["text"]
    },
    {
      "slug": "mimo-v2.5-pro",
      "display_name": "Mimo-V2.5-pro",
      "description": "小米旗舰模型",
      "supported_reasoning_levels": [
        { "effort": "low", "description": "Low effort thinking" },
        { "effort": "medium", "description": "Medium effort thinking" },
        { "effort": "high", "description": "High effort thinking" }
      ],
      "context_window": 1024000,
      "max_context_window": 1024000,
      "auto_compact_token_limit": 768000,
      "input_modalities": ["text"]
    },
    {
      "slug": "gpt-5.5",
      "display_name": "GPT-5.5",
      "description": "OpenAI 旗下最新模型",
      "supported_reasoning_levels": [
        { "effort": "none", "description": "no thinking" },
        { "effort": "low", "description": "Low effort thinking" },
        { "effort": "medium", "description": "Medium effort thinking" },
        { "effort": "high", "description": "High effort thinking" },
        { "effort": "xhigh", "description": "Extra High effort thinking" }
      ],
      "support_verbosity": true,
      "context_window": 272000,
      "max_context_window": 400000,
      "auto_compact_token_limit": 200000,
      "input_modalities": ["text", "image"]
    }
  ]
}
```

> **说明**：如果不提供 `models_config.json`，agr 会根据路由配置自动生成模型条目，但自动生成的元数据可能与实际上游能力不匹配。

## 转换器详解

转换器（Transformer）是 agr 的核心能力，负责在不同协议之间进行格式适配。

### 工作原理

每个提供商配置一个 `transformer` 数组。agr 按数组顺序执行请求转换，按逆序执行响应/流式转换：

```
请求方向：transformer[0] → transformer[1] → ... → 上游
响应方向：上游 → ... → transformer[1] → transformer[0] → 客户端
```

### 内置转换器

| 名称 | 用途 | 典型场景 |
|------|------|----------|
| `openai` | 核心协议转换器。根据请求路径分流：`/v1/messages` ↔ Anthropic Messages，`/v1/responses` ↔ OpenAI Responses，其他路径透传 | 大多数 OpenAI 兼容提供商的首选 |
| `deepseek` | 处理 DeepSeek 特有的 `reasoning_content` 格式。非 Claude 请求注入 `thinking: {disabled}`；Claude 请求将 thinking 块提取为 `reasoning_content` | 配合 `openai` 一起用于 DeepSeek |
| `anthropic` | 透传 Claude（Messages API）请求，拒绝 Codex（Responses API）请求 | 上游仅支持 Anthropic 协议时使用 |
| `openai-responses` | 透传 Codex（Responses API）请求，拒绝 Claude（Messages API）请求 | 上游仅支持 Responses API 时使用 |

### 转换器选择指南

```
上游支持 OpenAI Chat Completions API？
├── 是 → transformer = ["openai"]
│       └── 上游是 DeepSeek？
│           └── 是 → transformer = ["openai", "deepseek"]
└── 否
    ├── 上游支持 Anthropic Messages API？
    │   └── 是 → transformer = ["anthropic"]
    └── 上游支持 OpenAI Responses API？
        └── 是 → transformer = ["openai-responses"]
```

### 实际配置示例

**示例 1：通过 OpenAI 兼容接口使用智谱 GLM**

```toml
[[providers]]
name = "zhipu"
api_base_url = "https://api.zhipu.example.com/v1/chat/completions"
api_key = "your-key"
models = ["glm-5-oc"]
transformer = ["openai"]
```

**示例 2：使用 DeepSeek（需要 thinking 映射）**

```toml
[[providers]]
name = "deepseek"
api_base_url = "https://api.deepseek.com/chat/completions"
api_key = "sk-your-key"
models = ["deepseek-chat"]
transformer = ["openai", "deepseek"]
```

**示例 3：Anthropic 兼容接口（直接透传）**

```toml
[[providers]]
name = "mimo-anthropic"
api_base_url = "https://api.mimo.example.com/anthropic/v1/messages"
api_key = "your-key"
models = ["mimo-v2.5-pro"]
transformer = ["anthropic"]
```

**示例 4：Responses API 原生提供商（供 Codex 使用）**

```toml
[[providers]]
name = "freemodel"
api_base_url = "https://api.freemodel.example.com/responses"
api_key = "your-key"
models = ["gpt-5.5"]
transformer = ["openai-responses"]
```

## API 端点

| 端点 | 协议 | 目标客户端 | 状态 |
|------|------|-----------|------|
| `/v1/messages` | Anthropic Messages API | Claude Code | ✅ 已实现 |
| `/v1/responses` | OpenAI Responses API | Codex | ✅ 已实现 |
| `/v1/models` | OpenAI Models API | Codex 模型发现 | ✅ 已实现 |
| `/health` | — | 健康检查 | ✅ 已实现 |
| `/api/chat` | Ollama Chat | VS Code Copilot | 🚧 Phase 2 |
| `/api/generate` | Ollama Generate | VS Code Copilot | 🚧 Phase 2 |
| `/api/tags` | Ollama Tags | VS Code Copilot | 🚧 Phase 2 |

## 典型部署场景

### 场景 1：用国产模型驱动 Claude Code

```toml
[server]
port = 9999

[[providers]]
name = "mimo"
api_base_url = "https://api.mimo.example.com/v1/chat/completions"
api_key = "your-key"
models = ["mimo-v2.5-pro"]
transformer = ["openai"]

[router]
default = "mimo,mimo-v2.5-pro"
```

Claude Code `settings.json`：

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "http://localhost:9999"
  },
  "model": "mimo-v2.5-pro"
}
```

### 场景 2：同一网关同时服务 Claude Code 和 Codex

```toml
[server]
port = 9999
models_config = "models_config.json"

# Claude Code 走这个提供商（Anthropic 协议）
[[providers]]
name = "mimo-anthropic"
api_base_url = "https://api.mimo.example.com/anthropic/v1/messages"
api_key = "your-key"
models = ["mimo-v2.5-pro"]
transformer = ["anthropic"]

# Codex 走这个提供商（Responses 协议）
[[providers]]
name = "freemodel"
api_base_url = "https://api.freemodel.example.com/responses"
api_key = "your-key"
models = ["gpt-5.5"]
transformer = ["openai-responses"]

[router]
"mimo-v2.5-pro" = "mimo-anthropic,mimo-v2.5-pro"
"gpt-5.5"       = "freemodel,gpt-5.5"
```

### 场景 3：多提供商 + DeepSeek

```toml
[server]
port = 9999

[[providers]]
name = "zhipu"
api_base_url = "https://api.zhipu.example.com/v1/chat/completions"
api_key = "your-zhipu-key"
models = ["glm-5-oc"]
transformer = ["openai"]

[[providers]]
name = "opencode"
api_base_url = "https://opencode.ai/zen/go/v1/chat/completions"
api_key = "opencode-key"
models = ["glm-5.1"]
transformer = ["openai"]

[[providers]]
name = "deepseek"
api_base_url = "https://api.deepseek.com/chat/completions"
api_key = "sk-deepseek-key"
models = ["deepseek-chat"]
transformer = ["openai", "deepseek"]

[router]
default         = "zhipu,glm-5-oc"
"glm-5"         = "zhipu,glm-5-oc"
"glm-5.1"       = "opencode,glm-5.1"
"deepseek-chat" = "deepseek,deepseek-chat"
```

## 项目结构

```
agr/
├── main.go                  # 入口点
├── cmd/                     # Cobra CLI 命令
│   ├── root.go              # 根命令
│   ├── start.go             # agr start
│   ├── stop.go              # agr stop
│   ├── restart.go           # agr restart
│   ├── daemon_unix.go       # Unix 守护进程
│   └── daemon_windows.go    # Windows 守护进程
├── config/                  # TOML 配置加载与校验
├── server/                  # HTTP 服务器
├── router/                  # 模型 → 提供商路由
├── proxy/                   # 请求转发与 SSE 流式传输
├── transformer/             # 协议转换器
│   ├── transformer.go       # 转换器接口、Chain、注册表
│   ├── openai/              # openai 转换器（核心协议转换）
│   │   ├── openai.go        # 入口，按路径分流
│   │   ├── request_claude.go   # Anthropic → OpenAI 请求转换
│   │   ├── request_codex.go    # Responses → Chat 请求转换
│   │   ├── response_claude.go  # OpenAI → Anthropic 响应转换
│   │   ├── response_codex.go   # Chat → Responses 响应转换
│   │   ├── stream_claude.go    # 流式响应转换（Anthropic 方向）
│   │   └── stream_codex.go     # 流式响应转换（Codex 方向）
│   ├── deepseek.go          # DeepSeek thinking 映射
│   ├── anthropic.go         # Anthropic 协议透传/拦截
│   └── openai_responses.go  # Responses API 透传/拦截
├── process/                 # PID 文件与进程信号管理
├── models/                  # 模型元数据定义
├── version/                 # 版本信息
├── config.toml              # 示例配置
└── install.sh               # 一键安装脚本
```

## 开发

### 环境要求

- Go 1.25+

### 常用命令

```bash
# 运行全部测试
go test ./...

# 运行指定包的测试
go test ./transformer -run TestDeepSeek

# 构建
go build -o agr .

# 格式化代码
gofmt -l -w .

# 前台启动（开发调试）
go run . start

# 守护进程模式
go run . start -d

# 停止
go run . stop
```

### 添加新的转换器

1. 在 `transformer/` 下创建新文件，实现 `Transformer` 接口：

```go
type Transformer interface {
    TransformRequest(ctx context.Context, body []byte) ([]byte, error)
    TransformResponse(ctx context.Context, body []byte) ([]byte, error)
    TransformStream(ctx context.Context, chunk []byte) ([]byte, error)
}
```

2. 在 `transformer/transformer.go` 的 `registry` 中注册工厂函数
3. 在 `config/config.go` 的 `IsValidTransformer` 中添加名称
4. 如果一个上游 chunk 需要拆分为多个下游事件，额外实现 `CodexStreamTransformer` 接口

### 提交规范

使用 Conventional Commits：

```
feat: add Ollama compatibility endpoints
fix: handle empty streaming chunks from DeepSeek
test: add table-driven tests for transformer chain
refactor: extract request path detection into router
docs: update README with client configuration examples
```

## 安全注意事项

- **API Key 保密**：`config.toml` 中的 `api_key` 是敏感信息，不要提交到公开仓库
- **日志脱敏**：agr 不会记录完整的 Authorization 头或上游响应体
- **本地运行**：默认绑定 `localhost`，仅本机可访问
- **配置文件权限**：建议设置 `chmod 600 ~/.agr/config.toml`

## 常见问题

**Q: macOS 提示 "cannot be opened because the developer cannot be verified"**

```bash
xattr -d com.apple.quarantine /usr/local/bin/agr
```

**Q: 端口被占用怎么办？**

```bash
# 检查占用
lsof -i :9999
# 或者换一个端口
agr start -p 8080
```

**Q: 如何查看详细日志？**

在配置文件中将 `log_level` 改为 `"debug"`，重启服务。

**Q: Claude Code 连不上网关？**

确认 `ANTHROPIC_BASE_URL` 设置为 `http://localhost:9999`（注意没有 `/v1` 后缀）。

**Q: Codex 连不上网关？**

确认 `base_url` 设置为 `http://localhost:9999/v1`（注意有 `/v1` 后缀），并且 `wire_api = "responses"`。

**Q: 如何让 Claude Code 和 Codex 同时使用同一个网关？**

在路由配置中为不同模型设置不同提供商，Claude Code 请求走 Anthropic 协议端点，Codex 走 Responses 协议端点，agr 自动处理协议差异。参考上方"场景 2"。

## 路线图

- [x] Phase 1：核心网关能力（Claude Code + Codex 代理）
- [ ] Phase 2：Ollama 协议兼容（VS Code Copilot 支持）
- [ ] 多提供商 fallback
- [ ] 请求速率限制
- [ ] Web UI 管理面板

## 许可证

[MIT](LICENSE)
