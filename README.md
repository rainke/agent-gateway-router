# agr — Agent Gateway Router

**agr** is a lightweight local Agent gateway proxy written in Go. It runs as a background daemon and sits between local AI clients (Claude Code, Codex, VS Code Copilot) and upstream LLM providers, handling protocol adaptation, model routing, streaming response forwarding, and request/response transformation.

## Architecture

```
AI Client (Claude Code / Codex / Copilot)
        │
        ▼
   localhost:9999
   ┌──────────────────────┐
   │       agr            │
   │                      │
   │  ┌──────┐ ┌───────┐  │
   │  │Router│ │Transf.│  │
   │  │      │ │ Chain │  │
   │  └──┬───┘ └───┬───┘  │
   │     │         │      │
   └─────┼─────────┼──────┘
         │         │
         ▼         ▼
   Provider A   Provider B   ...
```

When a client sends a request, agr extracts the model name, routes it to the configured upstream provider via the router, transforms the request through a configurable transformer chain, forwards it to the upstream, then transforms and streams the response back to the client.

## Features

- **Multi-Protocol Support** — Proxy for Claude Code (`/v1/messages`) and Codex (`/v1/responses`) with protocol transformation
- **Model Routing** — Route client-requested models to different upstream providers. Supports exact match with fallback to default
- **Transformer Chain** — Configurable ordered pipeline of transformers (e.g., `["openai", "deepseek"]`) for request/response adaptation
- **Streaming** — SSE streaming response forwarding with real-time per-chunk transformation
- **DeepSeek Integration** — Specialized transformer that maps Anthropic thinking blocks to DeepSeek reasoning_content and vice versa
- **Daemon Management** — `start`, `stop`, `restart` commands with PID file and graceful shutdown (30s timeout for in-flight streams)
- **TOML Configuration** — Single config file with validation at startup

## Quick Start

```bash
# Build
go build -o agr .

# Start foreground
go run . start -c config.toml

# Start as daemon
go run . start -c config.toml -d

# Override port
go run . start -c config.toml -p 9998

# Stop daemon
go run . stop -c config.toml

# Restart
go run . restart -c config.toml
```

## Configuration

```toml
[server]
port = 9999
log_level = "debug"
pid_file = "~/.agr/agr.pid"
models_config = "models_config.json"

[[providers]]
name = "deepseek"
api_base_url = "https://api.deepseek.com/chat/completions"
api_key = "sk-xxx"
models = ["deepseek-chat", "deepseek-coder"]
transformer = ["openai", "deepseek"]

[[providers]]
name = "opencode-go"
api_base_url = "https://opencode.ai/zen/go/v1/chat/completions"
api_key = "sk-xxx"
models = ["glm-5.1"]
transformer = ["openai"]

[router]
default = "deepseek,deepseek-chat"
"glm-5.1" = "opencode-go,glm-5.1"
```

### models_config

Codex (OpenAI Responses API client) calls `/v1/models` on startup to discover available models and their capabilities. Unlike Claude Code, which only needs a model name to send requests, Codex relies on rich model metadata to populate its UI — including reasoning levels, input modalities, context window size, verbosity controls, and tool support flags.

If `models_config` is not set, agr auto-generates model entries from the router configuration with reasonable defaults. However, these defaults may not match the actual upstream model capabilities (e.g., an upstream model may not support image input or all reasoning levels). A custom `models_config.json` lets you provide accurate per-model metadata so Codex displays the correct controls and avoids sending unsupported options.

Example `models_config.json`:

```json
{
  "models": [
    {
      "slug": "glm-5",
      "display_name": "GLM-5-OC",
      "context_window": 204800,
      "input_modalities": ["text"],
      "supported_reasoning_levels": ["low", "medium", "high"]
    }
  ]
}
```

Place the file at `~/.agr/models_config.json` (or configure a custom path via `server.models_config`). See `models/models.go` for the full list of supported fields.

### Router Mapping

Format: `client_model = "provider_name,upstream_model"`

- Exact match first, then fallback to `router.default`
- Provider and model must exist in the `[[providers]]` section

### Transformer Chain

Built-in transformers:

| Name | Purpose |
|------|---------|
| `openai` | Converts between Claude/Codex/OpenAI protocols and upstream formats |
| `deepseek` | Handles DeepSeek-specific `reasoning_content` ↔ Anthropic thinking mapping |

Transformers are executed in order for requests and reverse order for responses.

## Endpoints

| Path | Client | Phase |
|------|--------|-------|
| `/v1/messages` | Claude Code | 1 |
| `/v1/responses` | Codex | 1 |
| `/api/chat` | VS Code Copilot (Ollama) | 2 (planned) |
| `/api/generate` | VS Code Copilot (Ollama) | 2 (planned) |
| `/api/tags` | VS Code Copilot (Ollama) | 2 (planned) |
| `/health` | Health check | 1 |

Phase 2 endpoints return `501 Not Implemented` in the current version.

## Project Structure

```
├── main.go              # Entry point
├── cmd/                 # Cobra commands (start, stop, restart)
├── config/              # TOML config loading and validation
├── process/             # PID file management and process signaling
├── server/              # HTTP server
├── router/              # Model-to-provider routing
├── proxy/               # Request forwarding and streaming
└── transformer/         # Protocol adaptation transformers
```

## Development

```bash
# Run tests
go test ./...

# Focused test
go test ./transformer -run TestDeepSeek

# Code format
gofmt -l -w .
```

## License
 
 ## Roadmap
 
 | Upstream API | Claude Code | Codex | VS Code Copilot |
 |--------------|:-----------:|:-----:|:---------------:|
 | OpenAI `/v1/chat/completions` | ✅ | ✅ | 🔲 |
 | Anthropic `/v1/messages` | — | 🔲 | 🔲 |
 
 > **Note:** Claude Code natively uses the Anthropic `/v1/messages` API, so it does not need to be listed as a client target for that upstream.

MIT
