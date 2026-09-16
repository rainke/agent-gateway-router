package proxy

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPassthroughNegotiatesIdentityForUsage(t *testing.T) {
	for _, stream := range []bool{true, false} {
		t.Run(map[bool]string{true: "stream", false: "json"}[stream], func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			reply := `{"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`
			ct := "application/json"
			if stream {
				reply = "data: " + reply + "\n\ndata: [DONE]\n\n"
				ct = "text/event-stream"
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Accept-Encoding") != "identity" {
					t.Errorf("Accept-Encoding=%q; want identity", r.Header.Get("Accept-Encoding"))
					w.Header().Set("Content-Encoding", "br")
				}
				w.Header().Set("Content-Type", ct)
				io.WriteString(w, reply)
			}))
			defer upstream.Close()
			req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"test-provider/model-a","stream_options":{"include_usage":true}}`))
			req.Header.Set("Accept-Encoding", "gzip, deflate, br")
			rec := httptest.NewRecorder()
			newTestProxy(upstream.URL).HandleChatCompletions(rec, req)
			if rec.Code != 200 || rec.Body.String() != reply {
				t.Fatal("response changed")
			}
			files, _ := filepath.Glob(filepath.Join(UsageDir(), "*.jsonl"))
			if len(files) != 1 {
				t.Fatalf("usage files=%d, want 1", len(files))
			}
			data, err := os.ReadFile(files[0])
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), `"total_tokens":12`) {
				t.Fatalf("unexpected usage %s", data)
			}
		})
	}
}

func TestPassthroughUnexpectedCompressionLogged(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(old)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "br")
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "opaque compressed bytes")
	}))
	defer upstream.Close()
	rec := httptest.NewRecorder()
	newTestProxy(upstream.URL).HandleChatCompletions(rec, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"test-provider/model-a"}`)))
	if rec.Body.String() != "opaque compressed bytes" || rec.Header().Get("Content-Encoding") != "br" {
		t.Fatal("compressed response changed")
	}
	for _, want := range []string{`"msg":"上游响应"`, `"content_encoding":"br"`, `"msg":"跳过 usage 统计"`, `"reason":"compressed_response"`} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("missing %s", want)
		}
	}
}
