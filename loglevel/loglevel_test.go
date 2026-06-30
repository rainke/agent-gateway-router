package loglevel

import (
	"context"
	"log/slog"
	"testing"
)

func TestLevelTraceValue(t *testing.T) {
	// trace 介于 debug(-4) 与 info(0) 之间，取 -2。
	if LevelTrace != slog.Level(-2) {
		t.Errorf("LevelTrace 期望 slog.Level(-2)，实际 %d", int(LevelTrace))
	}
}

func TestLevelPriorityOrder(t *testing.T) {
	// 优先级顺序（从最详细到最简略）：debug > trace > info > warn > error
	// 在 slog 中数值越小越详细，因此期望严格递增。
	want := []slog.Level{slog.LevelDebug, LevelTrace, slog.LevelInfo, slog.LevelWarn, slog.LevelError}
	for i := 0; i < len(want)-1; i++ {
		if !(want[i] < want[i+1]) {
			t.Errorf("级别顺序错误：%d(%d) 应小于 %d(%d)", i, int(want[i]), i+1, int(want[i+1]))
		}
	}
	// trace 必须严格位于 debug 与 info 之间
	if !(slog.LevelDebug < LevelTrace && LevelTrace < slog.LevelInfo) {
		t.Errorf("trace 应介于 debug 与 info 之间，实际 debug=%d trace=%d info=%d",
			int(slog.LevelDebug), int(LevelTrace), int(slog.LevelInfo))
	}
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		in   string
		want slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"trace", LevelTrace},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"error", slog.LevelError},
		{"", slog.LevelInfo},        // 空字符串走默认 info
		{"verbose", slog.LevelInfo}, // 未知级别走默认 info
		{"DEBUG", slog.LevelInfo},   // 大小写敏感，未匹配走默认 info
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := ParseLevel(tt.in); got != tt.want {
				t.Errorf("ParseLevel(%q) = %d，期望 %d", tt.in, int(got), int(tt.want))
			}
		})
	}
}

func TestLevelName(t *testing.T) {
	tests := []struct {
		level slog.Level
		want  string
	}{
		{slog.LevelDebug, "DEBUG"},
		{LevelTrace, "TRACE"},
		{slog.LevelInfo, "INFO"},
		{slog.LevelWarn, "WARN"},
		{slog.LevelError, "ERROR"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := LevelName(tt.level); got != tt.want {
				t.Errorf("LevelName(%d) = %q，期望 %q", int(tt.level), got, tt.want)
			}
		})
	}
}

// capturingHandler 记录收到的日志记录，用于验证 Trace 以 trace 级别输出。
type capturingHandler struct {
	records []slog.Record
}

func (h *capturingHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= LevelTrace }
func (h *capturingHandler) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r)
	return nil
}
func (h *capturingHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *capturingHandler) WithGroup(_ string) slog.Handler      { return h }

func TestTrace_EmittedAtTraceLevel(t *testing.T) {
	h := &capturingHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })

	Trace("转换前的请求体", "body", `{"x":1}`)

	if len(h.records) != 1 {
		t.Fatalf("期望捕获 1 条记录，实际 %d", len(h.records))
	}
	r := h.records[0]
	if r.Level != LevelTrace {
		t.Errorf("记录级别期望 trace(%d)，实际 %d", int(LevelTrace), int(r.Level))
	}
	if r.Message != "转换前的请求体" {
		t.Errorf("消息期望 \"转换前的请求体\"，实际 %q", r.Message)
	}
}

// 确保 capturingHandler 满足 slog.Handler 接口（编译期检查）。
var _ slog.Handler = (*capturingHandler)(nil)
