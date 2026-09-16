package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestRequestParameterLogging(t *testing.T) {
	for _, level := range []slog.Level{slog.LevelDebug, slog.LevelInfo} {
		t.Run(level.String(), func(t *testing.T) {
			var logs bytes.Buffer
			old := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: level})))
			defer slog.SetDefault(old)
			body := `{"model":"test-provider/model-a","stream":true,"stream_options":{"include_usage":true},"max_tokens":32,"temperature":0.5,"messages":[{"role":"user","content":"hello"}]}`
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got, _ := io.ReadAll(r.Body)
				var actual, expected map[string]any
				json.Unmarshal(got, &actual)
				json.Unmarshal([]byte(strings.Replace(body, "test-provider/model-a", "model-a", 1)), &expected)
				if !reflect.DeepEqual(actual, expected) {
					t.Error("request changed")
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{}`)
			}))
			defer upstream.Close()
			rec := httptest.NewRecorder()
			newTestProxy(upstream.URL).HandleChatCompletions(rec, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body)))
			if rec.Code != 200 {
				t.Fatalf("status=%d", rec.Code)
			}
			out := logs.String()
			if level == slog.LevelDebug {
				// slog JSON handler 会转义 body 字符串中的引号。
				for _, want := range []string{
					`"msg":"代理请求参数"`,
					`\"stream\":true`,
					`\"include_usage\":true`,
					`\"max_tokens\":32`,
					`\"temperature\":0.5`,
					`\"content\":\"hello\"`,
				} {
					if !strings.Contains(out, want) {
						t.Errorf("missing %s in %s", want, out)
					}
				}
			} else if strings.Contains(out, "代理请求参数") {
				t.Error("parameters should require debug")
			}
		})
	}
}

func TestRequestParameterLoggingUsesRawBody(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(old)
	body := `{"messages":[{"content":"raw content"}],"temperature":0.7}`
	logRequestParameters([]byte(body), "/v1/chat/completions", "p", "m")
	out := logs.String()
	if !strings.Contains(out, `\"messages\":[{\"content\":\"raw content\"}]`) {
		t.Fatalf("expected raw body in log, got %s", out)
	}
	if strings.Contains(out, "[REDACTED]") {
		t.Fatalf("body should not be redacted, got %s", out)
	}
}
