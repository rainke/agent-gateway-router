# agr — AI Gateway Router

**agr** 是一个用 Go 编写的轻量级本地 AI 网关代理。它作为后台守护进程运行，位于本地 AI 客户端（Claude Code、Codex、VS Code Copilot 等）和上游 LLM 提供商之间，处理模型路由和原生 API 转发。请求仅按路由替换模型名，响应与流式事件保持上游格式。

## 为什么需要 agr？

| 痛点 | agr 的解决方案 |
|------|---------------|
| 多个 AI 客户端使用不同 API | 同一端口代理 Messages、Responses 和 Chat Completions，由上游原生处理 |
| 想把 Claude Code 的请求转发到 DeepSeek / GLM / Mimo 等国产模型 | 通过 `<provider>/<model>` 选择上游 |
| 每个客户端都要单独配置 API Key 和 Base URL | 统一网关入口，客户端只需指向 `localhost:9999` |
| 需要在多个提供商之间切换 | 通过模型名中的提供商前缀明确选择上游 |

## 架构总览

```text
客户端 → 模型路由 → HTTP 代理 → 上游原生 API
                       ↑              │
                       └── 原样响应 ──┘
```

1. 从请求体读取 `model`，按配置选择提供商与上游模型。
2. 仅替换请求的 `model` 字段，保留 tools、thinking、reasoning 等协议字段。
3. 将请求发往上游对应端点，携带协议头和提供商凭据。
4. 透传响应体、状态码、响应头及 trailer；SSE 实时转发，不重建事件。

## 功能特性

- **原生 API 代理** — Messages、Responses、Chat Completions 和 Messages count_tokens
- **模型路由** — 通过 `<provider>/<model>` 选择提供商和模型
- **流式传输** — 保留 SSE 的 event、data、id、注释和结束事件，客户端断开时取消上游请求
- **用量统计** — 旁路读取原生 JSON / SSE usage，不修改响应；向上游请求 `Accept-Encoding: identity` 以读取 usage；上游仍返回压缩响应或超过统计缓冲上限（4 MiB）的响应或事件时跳过统计
- **守护进程管理** — `start`/`stop`/`restart`，PID 管理和优雅停机
- **TOML 配置** — 校验提供商配置，支持环境变量凭据

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
api_base_url = "https://api.deepseek.com/v1"
api_key = "sk-your-key-here"
models = ["deepseek-chat"]
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
log_level = "info"              # debug | trace | info | warn | error
pid_file = "~/.agr/agr.pid"

# ── 提供商定义 ──────────────────────────────────────────────

# 提供商 1：智谱 GLM（OpenAI 兼容接口）
[[providers]]
name = "zhipu"
api_base_url = "https://api.zhipu.example.com/v1"
api_key = "your-zhipu-key"
models = ["glm-5-oc"]

# 提供商 2：DeepSeek（需要 thinking 映射）
[[providers]]
name = "deepseek"
api_base_url = "https://api.deepseek.com/v1"
api_key = "sk-your-deepseek-key"
models = ["deepseek-chat"]

# 提供商 3：Mimo（OpenAI 兼容接口）
[[providers]]
name = "mimo"
api_base_url = "https://api.mimo.example.com/v1"
api_key = "your-mimo-key"
models = ["mimo-v2.5-pro"]

# 提供商 4：Mimo（Anthropic 兼容接口，供 Claude Code 使用）
[[providers]]
name = "mimo-anthropic"
api_base_url = "https://api.mimo.example.com/anthropic/v1"
api_key = "your-mimo-key"
models = ["mimo-v2.5-pro"]

# 提供商 5：支持 Responses API 的提供商（供 Codex 使用）
[[providers]]
name = "freemodel"
api_base_url = "https://api.freemodel.example.com/v1"
api_key = "your-freemodel-key"
models = ["gpt-5.5", "gpt-5.3-codex"]

```

### 配置字段说明

#### `[server]`

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `port` | int | `9999` | 本地监听端口 |
| `log_level` | string | `"info"` | 日志级别：`debug` / `trace` / `info` / `warn` / `error`（从详细到简略） |
| `pid_file` | string | `"~/.agr/agr.pid"` | PID 文件路径 |

#### `[[providers]]`

| 字段 | 类型 | 说明 |
|------|------|------|
| `name` | string | 提供商唯一名称，不能包含 `/`，作为对外模型名前缀 |
| `api_base_url` | string | 上游 API 基础地址（主机或带路径前缀） |
| `api_key` | string | 上游 API 密钥；支持 `"env:VAR_NAME"` 形式从环境变量读取 |
| `models` | []string | 该提供商支持的模型列表 |

#### 对外模型名

客户端使用 `<provider>/<model>`，例如 `deepseek/deepseek-chat` 或 `freemodel/gpt-5.5`。
`provider` 必须匹配提供商名称，`model` 必须在该提供商的 `models` 列表中。
只按第一个 `/` 拆分，因此 `provider/org/model` 会向上游传递 `org/model`。
不再支持模型别名或默认路由；旧 `[router]` 配置会被忽略，可直接删除。

### 配置校验规则

agr 在启动时执行严格校验，以下情况会直接报错退出：

- `providers.name` 重复
- `providers.name` 为空或包含 `/`
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
  "model": "zhipu/glm-5-oc"
}
```

> **提示**：Claude Code 走 `/v1/messages` 端点，使用 Anthropic Messages 协议。agr 将其转发到上游的 Messages 端点。

如果上游的 Messages API 使用独立路径前缀，在基础地址中配置该前缀：

```toml
[[providers]]
name = "mimo-anthropic"
api_base_url = "https://api.mimo.example.com/anthropic/v1"
api_key = "your-key"
models = ["mimo-v2.5-pro"]
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
model = "mimo/mimo-v2.5-pro"
model_provider = "agr"
model_reasoning_effort = "medium"
model_catalog_json = "/Users/me/.codex/agr-model-catalog.json"
```

启动 Codex 时指定 agr profile：

```bash
codex -p agr
```


> **提示**：Codex 0.134.0 起，`-p agr` 会在 `~/.codex/config.toml` 之上叠加读取 `~/.codex/agr.config.toml`，不再读取 `~/.codex/config.toml` 中的 `[profiles.agr]`。Codex 走 `/v1/responses` 端点，使用 OpenAI Responses API 协议。agr 直接转发到所选提供商的 Responses API。

### Codex 模型元数据（model_catalog_json）

agr 不再提供 `/v1/models` 模型发现接口；Codex 的模型能力元数据应通过 Codex 自己的 `model_catalog_json` 配置加载。`model_catalog_json` 是 Codex 配置中的 JSON 模型目录路径，推荐在 `~/.codex/agr.config.toml` 这个 agr profile 中配置，这样只在 `codex -p agr` 时生效。

创建 `~/.codex/agr-model-catalog.json`：

```json
{
  "models": [
    {
      "slug": "zhipu/glm-5-oc",
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
      "slug": "mimo/mimo-v2.5-pro",
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
      "slug": "freemodel/gpt-5.5",
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

> **说明**：`model_catalog_json` 可以放在 `~/.codex/config.toml` 顶层，也可以放在 `~/.codex/agr.config.toml` profile 文件中；同时存在时，Codex 会使用当前 profile 中的值。请确保 JSON 中的 `slug` 与 `~/.codex/agr.config.toml` 里的 `model` 一致，均使用 agr 的 `<provider>/<model>` 格式。

## 代理行为与配置迁移

`api_base_url` 配置上游 API 基础地址，例如 `https://gateway.example.com/v1`。
同一提供商可处理所有已注册的 API 格式，agr 按请求路径选择端点：

| 配置地址 | 客户端路径 | 上游路径 |
| --- | --- | --- |
| `https://gateway.example.com` | `/v1/messages` | `/v1/messages` |
| `https://gateway.example.com/v1` | `/v1/responses` | `/v1/responses` |
| `https://gateway.example.com/anthropic/v1` | `/v1/messages/count_tokens` | `/anthropic/v1/messages/count_tokens` |

旧配置的 `transformer` 字段会被忽略，可以直接删除。旧的完整端点地址也兼容：
`/v1/chat/completions`、`/v1/messages`、`/v1/responses` 的末尾端点会按客户端路径替换；
无 `/v1` 的完整端点（例如 `/chat/completions`）继续使用无版本前缀的路径（例如 `/responses`）。
若各协议使用不同前缀，请按实际上游地址配置提供商和模型路由。

不再执行 thinking 映射、reasoning_effort 调整、协议限制或本地 token 估算。
`count_tokens` 始终使用模型路由选中的提供商，上游不支持时原样返回其错误。
请求只改写路由模型名；未知字段和大整数保持原值，JSON 的空白和键顺序可能重新序列化。
请求中的查询参数、协议头会保留；配置 `api_key` 时替换 Authorization，
Messages 请求及原本带有 `x-api-key` 的请求同时使用提供商的 `x-api-key`。
HTTP 逐跳头由反向代理移除。上游重定向直接返回客户端。

## API 端点

| 端点 | 协议 | 目标客户端 | 状态 |
|------|------|-----------|------|
| `/v1/messages` | Anthropic Messages API | Claude Code | ✅ 已实现 |
| `/v1/responses` | OpenAI Responses API | Codex | ✅ 已实现 |
| `/v1/chat/completions` | OpenAI Chat Completions | OpenAI 兼容客户端 | ✅ 已实现 |
| `/v1/messages/count_tokens` | Anthropic token 计数 | Claude Code | ✅ 上游转发 |
| `/health` | — | 健康检查 | ✅ 已实现 |

> **VS Code Copilot 集成**：VS Code 1.122+ 原生支持自定义 OpenAI 兼容端点，无需 Ollama 协议伪装。在 VS Code 设置中配置 `chat.agent.customEndpoint` 指向 `http://localhost:9999` 即可。

## 典型部署场景

### 场景 1：用国产模型驱动 Claude Code

```toml
[server]
port = 9999

[[providers]]
name = "mimo"
api_base_url = "https://api.mimo.example.com/v1"
api_key = "your-key"
models = ["mimo-v2.5-pro"]
```

Claude Code `settings.json`：

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "http://localhost:9999"
  },
  "model": "mimo/mimo-v2.5-pro"
}
```

### 场景 2：同一网关同时服务 Claude Code 和 Codex

```toml
[server]
port = 9999

# Claude Code 走这个提供商（Anthropic 协议）
[[providers]]
name = "mimo-anthropic"
api_base_url = "https://api.mimo.example.com/anthropic/v1"
api_key = "your-key"
models = ["mimo-v2.5-pro"]

# Codex 走这个提供商（Responses 协议）
[[providers]]
name = "freemodel"
api_base_url = "https://api.freemodel.example.com/v1"
api_key = "your-key"
models = ["gpt-5.5"]
```

### 场景 3：多提供商 + DeepSeek

```toml
[server]
port = 9999

[[providers]]
name = "zhipu"
api_base_url = "https://api.zhipu.example.com/v1"
api_key = "your-zhipu-key"
models = ["glm-5-oc"]

[[providers]]
name = "opencode"
api_base_url = "https://opencode.ai/zen/go/v1"
api_key = "opencode-key"
models = ["glm-5.1"]

[[providers]]
name = "deepseek"
api_base_url = "https://api.deepseek.com/v1"
api_key = "sk-deepseek-key"
models = ["deepseek-chat"]
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
├── process/                 # PID 文件与进程信号管理
├── version/                 # 版本信息
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
go test ./proxy -run TestPassthrough

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

### 提交规范

使用 Conventional Commits：

```
feat: add VS Code Copilot custom endpoint support
fix: handle empty streaming chunks from DeepSeek
test: cover native API forwarding
refactor: extract request path detection into router
docs: update README with client configuration examples
```

## 安全注意事项

- **API Key 保密**：`config.toml` 中的 `api_key` 是敏感信息，不要提交到公开仓库
- **环境变量引用**：`api_key` 可写为 `"env:OPENAI_API_KEY"`，启动时从同名环境变量读取，避免密钥落盘
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

在配置文件中将 `log_level` 改为 `"debug"`，重启服务。日志记录路由和上游状态，不记录请求或响应正文。

`debug` 级别会记录「代理请求参数」：直接打印原始请求体（`body`），实际转发模型单独记录。

`debug` 日志还记录上游响应状态、Content-Type 和 Content-Encoding。网关向上游发送 `Accept-Encoding: identity`，避免 Copilot 等客户端接受压缩时导致 usage 无法读取；若上游仍返回压缩内容，保持原样转发，并以 `warn` 日志记录统计跳过原因。

流式响应结束时，`info` 日志会记录 provider、model、接收字节数、SSE 数据事件数、跳过的超限事件数、是否存在未完成事件、是否收到 usage，以及各项 token 统计。`reason=eof` 表示响应体读取结束，`read_error` 表示读取出错，`closed` 表示读取结束前响应体被关闭；EOF 本身不代表模型生成成功。`usage_found=false` 表示未提取到上游 usage，token 字段为 0 不代表实际没有消耗。

`debug` 级别另外逐条记录 SSE 数据事件的序号、类型、大小和截至该事件是否已收到 usage；事件类型使用固定列表，未知类型记为 `unknown`，不输出原始流式正文。日志仍写入 `~/.agr/logs/<日期>.log`，用量统计写入 `~/.agr/usage/<日期>.jsonl`。

**Q: Claude Code 连不上网关？**

确认 `ANTHROPIC_BASE_URL` 设置为 `http://localhost:9999`（注意没有 `/v1` 后缀）。

**Q: Codex 连不上网关？**

确认 `base_url` 设置为 `http://localhost:9999/v1`（注意有 `/v1` 后缀），并且 `wire_api = "responses"`。

**Q: 如何让 Claude Code 和 Codex 同时使用同一个网关？**

在客户端使用 `<provider>/<model>` 选择不同提供商，Claude Code 请求走 Anthropic 协议端点，Codex 走 Responses 协议端点，agr 保持各自 API 格式并转发到对应端点。参考上方"场景 2"。

**Q: VS Code Copilot 如何连接网关？**

VS Code 1.122+ 原生支持自定义端点，在设置中配置 `chat.agent.customEndpoint` 为 `http://localhost:9999` 即可。

## 路线图

- [x] 核心网关能力（Claude Code + Codex 代理）
- [x] VS Code Copilot 自定义端点支持（≥1.122）
- [ ] 多提供商 fallback
- [ ] 请求速率限制
- [ ] Web UI 管理面板

## 许可证

[MIT](LICENSE)
