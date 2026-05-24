# Repository Guidelines

## Project Structure & Module Organization

`agr` is a Go CLI and local HTTP gateway. The entry point is `main.go`, which delegates to Cobra commands in `cmd/`. Core packages are organized by responsibility: `config/` loads and validates TOML config, `process/` manages PID files and lifecycle signals, `server/` owns the HTTP server, `router/` resolves client models to providers, `proxy/` forwards requests, and `transformer/` adapts request/response formats. Tests live beside their packages as `*_test.go`. The default config path is `~/.agr/config.toml`; avoid committing real provider credentials.

## Build, Test, and Development Commands

- `go test ./...` runs the full unit test suite.
- `go test ./transformer -run TestName` runs a focused package test.
- `go build -o agr .` builds the CLI binary in the repository root.
- `go run . start` starts the gateway using `~/.agr/config.toml`.
- `go run . start -d` starts it as a daemon.
- `go run . stop` stops the daemon.

## Coding Style & Naming Conventions

Use standard Go style: tabs from `gofmt`, short package names, exported identifiers only for public package APIs, and clear error wrapping with `%w`. Keep package boundaries small and practical; place routing logic in `router`, protocol adaptation in `transformer`, and process concerns in `process`. Run `gofmt` on changed Go files before submitting.

## Testing Guidelines

Use Go’s built-in `testing` package. Add tests next to the package under test with names like `TestLoadConfig`, `TestRouteDefault`, or `TestTransformStream`. Prefer table-driven tests for config validation, routing cases, and transformer conversions. For changes touching request forwarding or streaming behavior, include both success and error-path coverage.

## Commit & Pull Request Guidelines

Recent history uses Conventional Commit prefixes, for example `feat: initial commit...` and `test: add unit tests...`. Continue with `feat:`, `fix:`, `test:`, `refactor:`, or `docs:` followed by a concise imperative summary. Pull requests should describe the behavior change, list test commands run, link related issues when present, and include sample config or request/response snippets for protocol changes.

## Security & Configuration Tips

Treat `api_key` values in TOML as secrets. Use local-only config files or redacted examples when documenting providers. Do not log full authorization headers or upstream response bodies that may contain sensitive data.
