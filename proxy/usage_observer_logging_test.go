package proxy

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
)

type failingStream struct{ io.Reader }

func (f failingStream) Read(p []byte) (int, error) {
	n, _ := f.Reader.Read(p)
	return n, errors.New("secret upstream error")
}
func (f failingStream) Close() error { return nil }

func TestStreamLogging(t *testing.T) {
	for _, tc := range []struct {
		name, body, reason string
		fail               bool
		want               []string
	}{
		{"usage", "data: {\"type\":\"message_delta\",\"delta\":{\"text\":\"secret text\"},\"usage\":{\"output_tokens\":7,\"private\":\"secret usage\"}}\n\ndata: [DONE]\n\n", "eof", false, []string{`"events":2`, `"usage_found":true`, `"output_tokens":7`, `"event_type":"message_delta"`}},
		{"missing usage", "data: {\"choices\":[{\"delta\":{\"content\":\"secret text\"}}]}\n\n", "eof", false, []string{`"usage_found":false`}},
		{"read error", "data: {}\n\n", "read_error", true, []string{`"usage_found":false`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			var logs bytes.Buffer
			old := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
			defer slog.SetDefault(old)
			var body io.ReadCloser = io.NopCloser(strings.NewReader(tc.body))
			if tc.fail {
				body = failingStream{strings.NewReader(tc.body)}
			}
			o := &usageObserver{ReadCloser: body, stream: true, provider: "test", model: "model"}
			got, _ := io.ReadAll(o)
			o.Close()
			if string(got) != tc.body {
				t.Fatal("response changed")
			}
			out := logs.String()
			for _, want := range append(tc.want, `"msg":"流式响应结束"`, `"reason":"`+tc.reason+`"`) {
				if !strings.Contains(out, want) {
					t.Errorf("missing %s in %s", want, out)
				}
			}
			if strings.Count(out, `"msg":"流式响应结束"`) != 1 {
				t.Error("expected one summary")
			}
			if strings.Contains(out, "secret") {
				t.Error("sensitive data logged")
			}
		})
	}
}

func TestStreamLoggingClosedAndOversized(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"closed partial event", "data: {}\n", `"incomplete_event":true`},
		{"oversized event", "data: " + strings.Repeat("x", maxUsageBuffer) + "\n\n", `"skipped_events":1`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			old := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			defer slog.SetDefault(old)
			o := &usageObserver{ReadCloser: io.NopCloser(strings.NewReader(tc.body)), stream: true}
			got := make([]byte, len(tc.body))
			if _, err := io.ReadFull(o, got); err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.body {
				t.Fatal("response changed")
			}
			o.Close()
			out := logs.String()
			for _, want := range []string{`"reason":"closed"`, tc.want} {
				if !strings.Contains(out, want) {
					t.Errorf("missing %s in %s", want, out)
				}
			}
			if strings.Contains(out, "流式响应事件") {
				t.Error("event details should require debug")
			}
		})
	}
}
