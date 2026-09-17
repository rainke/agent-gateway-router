# Anthropic base URL: TDD evidence

## Source and intended behavior

Implemented the conversation-approved plan on 2026-09-17. A provider can set an
optional `anthropic_base_url` for Messages and count_tokens while Chat Completions
and Responses continue using `api_base_url`. Existing configurations retain their
behavior. Both addresses share credentials, models, and URL normalization.

User journeys:

- Configure separate OpenAI and Anthropic addresses under one provider/model.
- Keep using existing configurations without adding a new field.
- Receive unchanged successful, error, and streaming responses from the selected upstream.
- Apply a request adaptor without changing which protocol selects the base address.

## RED and GREEN

The same command was run before and after implementation:

```sh
go test ./config ./proxy ./server -run AnthropicBaseURL -count=1
```

- RED checkpoint: `76f9651` (`test: cover Anthropic base URL selection`). Config
  and proxy tests failed to compile because `Provider.AnthropicBaseURL` did not
  exist. The server tests compiled and executed, failing five Messages/count_tokens
  cases with `request sent to the wrong upstream`: TOML overrides were ignored.
- GREEN checkpoint: `6aee627` (`feat: support a separate Anthropic base URL`).
  The same command passed in all three packages after adding the configuration
  field, startup validation, and original-protocol address selection.
- No production refactor stage was needed.

Both checkpoints are on `codex/anthropic-base-url`. Preserve this evidence if the
commits are later squashed.

## Test guarantees

| Behavior | Test | Type | Result |
| --- | --- | --- | --- |
| Load absent, empty, and valid overrides; reject malformed HTTP(S) URLs with provider/field context | `config/TestLoadAnthropicBaseURL` | Unit/config integration | PASS |
| Normalize host, prefix, version, trailing slash, and legacy endpoint addresses; preserve base and client query parameters | `proxy/TestAnthropicBaseURLPaths` | HTTP integration | PASS |
| Choose the address using the original protocol while forwarding the adaptor's final path | `proxy/TestAnthropicBaseURLUsesOriginalProtocol` | HTTP integration | PASS |
| Route all four public interfaces correctly with and without an override, through TOML loading and real HTTP requests | `server/TestAnthropicBaseURLHTTP` | End-to-end with mock upstreams | PASS |
| Preserve provider credentials, protocol headers, model rewrite, large integers, status, response headers/body, errors, and SSE bytes | `server/TestAnthropicBaseURLHTTP` and existing passthrough tests | End-to-end/integration | PASS |

## Full validation

```sh
go test -race ./... -coverprofile=/tmp/agr-anthropic-base-url.cover
go tool cover -func=/tmp/agr-anthropic-base-url.cover
go build -o agr .
git diff --check
```

All commands passed. Overall statement coverage: **81.2%**. Relevant packages:
config **90.2%**, proxy **89.7%**, server **91.5%**, adaptor **100.0%**.
The existing cmd package remains at **58.9%**; CLI coverage expansion was outside
this change. The real-provider smoke test below is separate from coverage.

## Real DeepSeek smoke test

Started the newly built CLI with a temporary config and PID file on an available
local port. Configured `api_base_url = "https://api.deepseek.com"`,
`anthropic_base_url = "https://api.deepseek.com/anthropic"`, and
`models = ["deepseek-flash"]`. The existing credential was passed through a
temporary environment variable; no secret was written into the test config or
this report.

Sent `POST /v1/messages/count_tokens` through that local gateway with:

```json
{"model":"deepseek/deepseek-flash","messages":[{"role":"user","content":"Hello, how are you?"}]}
```

Observed **HTTP 200** and `{"input_tokens":36}`. The temporary gateway was stopped
and its configuration removed. The installed gateway on port 9999 and the user's
persistent configuration were not changed. Other real-provider protocols were
covered with mock upstreams rather than additional inference requests.
