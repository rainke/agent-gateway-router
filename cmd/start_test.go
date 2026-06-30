package cmd

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"agr/loglevel"
)

func TestUnescapeHandler_RendersTraceLevel(t *testing.T) {
	var buf bytes.Buffer
	h := newUnescapeHandler(&buf, slog.LevelDebug)
	logger := slog.New(h)
	logger.Log(context.Background(), loglevel.LevelTrace, "转换前的请求体", "body", `{"x":1}`)

	out := buf.String()
	if !strings.Contains(out, "level=TRACE") {
		t.Errorf("期望输出包含 level=TRACE，实际: %s", out)
	}
	if !strings.Contains(out, "转换前的请求体") {
		t.Errorf("期望输出包含消息，实际: %s", out)
	}
}

func TestUnescapeHandler_TraceFiltering(t *testing.T) {
	ctx := context.Background()
	h := newUnescapeHandler(&bytes.Buffer{}, slog.LevelInfo)
	if h.Enabled(ctx, loglevel.LevelTrace) {
		t.Error("info 级别下 trace 不应被启用")
	}

	h2 := newUnescapeHandler(&bytes.Buffer{}, slog.LevelDebug)
	if !h2.Enabled(ctx, loglevel.LevelTrace) {
		t.Error("debug 级别下 trace 应被启用")
	}

	h3 := newUnescapeHandler(&bytes.Buffer{}, loglevel.LevelTrace)
	if !h3.Enabled(ctx, loglevel.LevelTrace) {
		t.Error("trace 级别下 trace 应被启用")
	}
}
