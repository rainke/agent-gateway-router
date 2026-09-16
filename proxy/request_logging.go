package proxy

import (
	"context"
	"log/slog"
)

// 直接打印原始请求体，便于调试。
func logRequestParameters(body []byte, path, provider, model string) {
	if !slog.Default().Enabled(context.Background(), slog.LevelDebug) {
		return
	}
	slog.Debug("代理请求参数", "path", path, "provider", provider, "model", model, "body", string(body))
}
