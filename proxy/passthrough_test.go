package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPassthrough(t *testing.T) {
	for _, path := range []string{"/v1/messages", "/v1/responses", "/v1/chat/completions", "/v1/messages/count_tokens"} {
		for _, stream := range []bool{false, true} {
			t.Run(path+map[bool]string{true: "/stream", false: "/normal"}[stream], func(t *testing.T) {
				reply := "{\"model\":\"model-a\",\"custom\":9007199254740993}"
				contentType := "application/json; charset=utf-8"
				if stream {
					reply = ": ping\r\nevent: custom\r\nid: 42\r\nretry: 100\r\ndata: {\"untouched\":true}\r\n\r\ndata:[DONE]\n\n"
					contentType = "text/event-stream"
				}
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.RequestURI() != path+"?beta=1" {
						t.Errorf("upstream URI = %s", r.URL.RequestURI())
					}
					body, _ := io.ReadAll(r.Body)
					for _, value := range []string{`"model":"model-a"`, `9007199254740993`, `"reasoning_effort":"xhigh"`, `"tool_result"`} {
						if !strings.Contains(string(body), value) {
							t.Errorf("missing unchanged field %s in %s", value, body)
						}
					}
					if r.Header.Get("Anthropic-Version") != "2023-06-01" || r.Header.Get("Anthropic-Beta") != "test-beta" {
						t.Error("lost protocol headers")
					}
					if r.Header.Get("Authorization") != "Bearer sk-test" || r.Header.Get("X-Api-Key") != "sk-test" {
						t.Error("provider credentials not applied")
					}
					w.Header().Set("Content-Type", contentType)
					w.Header().Add("X-Request-Id", "request-1")
					w.Header().Set("Trailer", "X-Completion")
					w.WriteHeader(201)
					io.WriteString(w, reply)
					w.Header().Set("X-Completion", "finished")
				}))
				defer upstream.Close()
				p := newTestProxy(upstream.URL + "/v1")
				req := httptest.NewRequest("POST", path+"?beta=1", strings.NewReader(`{"model":"test-provider/model-a","reasoning_effort":"xhigh","custom":9007199254740993,"messages":[{"content":[{"type":"tool_result"}]}]}`))
				req.Header.Set("Anthropic-Version", "2023-06-01")
				req.Header.Set("Anthropic-Beta", "test-beta")
				req.Header.Set("X-Api-Key", "client-placeholder")
				rec := httptest.NewRecorder()
				p.handleProxy(rec, req, path)
				if rec.Code != 201 || rec.Body.String() != reply {
					t.Errorf("status/body = %d %q; want 201 %q", rec.Code, rec.Body.String(), reply)
				}
				if rec.Header().Get("Content-Type") != contentType || rec.Header().Get("X-Request-Id") != "request-1" {
					t.Error("lost response headers")
				}
				if rec.Result().Trailer.Get("X-Completion") != "finished" {
					t.Error("lost response trailer")
				}
			})
		}
	}
}

func TestPassthroughCountTokens(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages/count_tokens" {
			t.Errorf("path=%s", r.URL.Path)
		}
		w.Header().Set("Retry-After", "15")
		w.WriteHeader(429)
		io.WriteString(w, `{"error":{"type":"rate_limit"}}`)
	}))
	defer upstream.Close()
	p := newTestProxy(upstream.URL)
	rec := httptest.NewRecorder()
	p.HandleMessagesCountTokens(rec, httptest.NewRequest("POST", "/v1/messages/count_tokens", strings.NewReader(`{"model":"test-provider/model-a"}`)))
	if rec.Code != 429 || rec.Header().Get("Retry-After") != "15" || rec.Body.String() != `{"error":{"type":"rate_limit"}}` {
		t.Fatalf("response=%d %s %s", rec.Code, rec.Header(), rec.Body.String())
	}
}

func TestPassthroughURLs(t *testing.T) {
	for _, tc := range []struct{ base, path, want string }{
		{"", "/v1/messages", "/v1/messages"},
		{"/v1/", "/v1/responses", "/v1/responses"},
		{"/gateway", "/v1/messages", "/gateway/v1/messages"},
		{"/anthropic/v1/messages", "/v1/messages/count_tokens", "/anthropic/v1/messages/count_tokens"},
		{"/v1/chat/completions", "/v1/responses", "/v1/responses"},
		{"/chat/completions", "/v1/messages", "/messages"},
		{"/responses", "/v1/messages", "/messages"},
	} {
		t.Run(tc.base+tc.path, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tc.want {
					t.Errorf("path=%s want %s", r.URL.Path, tc.want)
				}
				io.WriteString(w, `{}`)
			}))
			defer upstream.Close()
			rec := httptest.NewRecorder()
			newTestProxy(upstream.URL+tc.base).handleProxy(rec, httptest.NewRequest("POST", tc.path, strings.NewReader(`{"model":"test-provider/model-a"}`)), tc.path)
			if rec.Code != 200 {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestPassthroughErrorsAndRedirect(t *testing.T) {
	for _, status := range []int{302, 400, 401, 429, 500, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				w.Header().Set("Location", "/should-not-follow")
				w.Header().Set("Retry-After", "30")
				w.WriteHeader(status)
				io.WriteString(w, "upstream error\n")
			}))
			defer upstream.Close()
			rec := httptest.NewRecorder()
			newTestProxy(upstream.URL).HandleMessages(rec, httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"test-provider/model-a"}`)))
			if rec.Code != status || rec.Body.String() != "upstream error\n" || rec.Header().Get("Retry-After") != "30" {
				t.Fatalf("response=%d %s %s", rec.Code, rec.Header(), rec.Body.String())
			}
		})
	}
}

func TestPassthroughUsage(t *testing.T) {
	for _, tc := range []struct{ name, contentType, body string }{
		{"messages", "application/json", `{"usage":{"input_tokens":10,"output_tokens":3}}`},
		{"responses", "application/json", `{"usage":{"input_tokens":10,"output_tokens":3,"input_tokens_details":{"cached_tokens":2},"output_tokens_details":{"reasoning_tokens":1}}}`},
		{"messages_stream", "text/event-stream", "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":3}}\n\n"},
		{"responses_stream", "text/event-stream", "event: response.completed\ndata: {\"type\":\"response.completed\",\ndata: \"response\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":3,\"input_tokens_details\":{\"cached_tokens\":2},\"output_tokens_details\":{\"reasoning_tokens\":1}}}}\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := setupUsageTestDir(t)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				io.WriteString(w, tc.body)
			}))
			defer upstream.Close()
			rec := httptest.NewRecorder()
			newTestProxy(upstream.URL).HandleResponses(rec, httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"test-provider/model-a"}`)))
			if rec.Body.String() != tc.body {
				t.Fatal("usage observer changed response")
			}
			data, err := os.ReadFile(usageJSONLPath(t, dir))
			if err != nil {
				t.Fatal(err)
			}
			var record UsageRecord
			if err := json.Unmarshal(bytes.TrimSpace(data), &record); err != nil {
				t.Fatal(err)
			}
			if record.InputTokens != 10 || record.OutputTokens != 3 || record.TotalTokens != 13 {
				t.Fatalf("usage=%+v", record)
			}
			if strings.HasPrefix(tc.name, "responses") && (record.CachedTokens != 2 || record.OutputReasoningTokens != 1) {
				t.Fatalf("details=%+v", record)
			}
		})
	}
}

func TestPassthroughFlushAndCancel(t *testing.T) {
	canceled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, ": first\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(canceled)
	}))
	defer upstream.Close()
	p := newTestProxy(upstream.URL)
	gateway := httptest.NewServer(http.HandlerFunc(p.HandleMessages))
	defer gateway.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", gateway.URL+"/v1/messages", strings.NewReader(`{"model":"test-provider/model-a","stream":true}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	first := make([]byte, len(": first\n\n"))
	if _, err := io.ReadFull(resp.Body, first); err != nil {
		t.Fatal(err)
	}
	if string(first) != ": first\n\n" {
		t.Fatalf("first chunk=%q", first)
	}
	cancel()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("upstream not canceled")
	}
}

func TestPassthroughInvalidRequests(t *testing.T) {
	for _, body := range []string{"not json", `{}`, `null`, `[]`, `{"model":42}`, `{"model":null}`, `{"model":""}`} {
		t.Run(body, func(t *testing.T) {
			rec := httptest.NewRecorder()
			newTestProxy("http://localhost:1").HandleMessages(rec, httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body)))
			if rec.Code != 400 {
				t.Fatalf("status=%d", rec.Code)
			}
		})
	}
	rec := httptest.NewRecorder()
	newTestProxy("http://localhost:1").HandleResponses(rec, httptest.NewRequest("GET", "/v1/responses", nil))
	if rec.Code != 405 || rec.Header().Get("Allow") != "POST" {
		t.Fatalf("response=%d %s", rec.Code, rec.Header())
	}
}

func TestPassthroughGatewayErrors(t *testing.T) {
	for _, base := range []string{"http://127.0.0.1:1", "ftp://example.com", "/relative", "http://%invalid"} {
		t.Run(base, func(t *testing.T) {
			rec := httptest.NewRecorder()
			newTestProxy(base).HandleResponses(rec, httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"test-provider/model-a"}`)))
			if rec.Code != 502 {
				t.Fatalf("response=%d %s", rec.Code, rec.Body.String())
			}
		})
	}
	p := newTestProxy("http://localhost:1")
	rec := httptest.NewRecorder()
	p.HandleResponses(rec, httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"unknown"}`)))
	if rec.Code != 502 {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestPassthroughModelAndCredentials(t *testing.T) {
	body := "{ \"model\": \"model-a\", \"input\": \"<hello>\" }\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received, _ := io.ReadAll(r.Body)
		var payload map[string]string
		if err := json.Unmarshal(received, &payload); err != nil || len(payload) != 2 || payload["model"] != "model-a" || payload["input"] != "<hello>" {
			t.Errorf("body changed: %q", received)
		}
		if r.Header.Get("Authorization") != "Bearer client-key" || r.Header.Get("X-Api-Key") != "client-api-key" {
			t.Error("client credentials changed without provider key")
		}
		if r.Header.Get("X-Hop") != "" {
			t.Error("hop-by-hop request header leaked")
		}
		if r.URL.RawQuery != "configured=1&beta=2" {
			t.Errorf("query=%s", r.URL.RawQuery)
		}
		w.Header().Set("Connection", "X-Hop-Response")
		w.Header().Set("X-Hop-Response", "strip")
		w.WriteHeader(204)
	}))
	defer upstream.Close()
	p := newTestProxy(upstream.URL + "?configured=1")
	p.cfg.Providers[0].APIKey = ""
	req := httptest.NewRequest("POST", "/v1/chat/completions?beta=2", strings.NewReader(strings.Replace(body, "model-a", "test-provider/model-a", 1)))
	req.Header.Set("Authorization", "Bearer client-key")
	req.Header.Set("X-Api-Key", "client-api-key")
	req.Header.Set("Connection", "X-Hop")
	req.Header.Set("X-Hop", "strip")
	rec := httptest.NewRecorder()
	p.HandleChatCompletions(rec, req)
	if rec.Code != 204 || rec.Header().Get("X-Hop-Response") != "" {
		t.Fatalf("response=%d %s", rec.Code, rec.Header())
	}
}

type failingReadCloser struct{}

func (failingReadCloser) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (failingReadCloser) Close() error             { return nil }

func TestPassthroughReadError(t *testing.T) {
	req := httptest.NewRequest("POST", "/v1/messages", nil)
	req.Body = failingReadCloser{}
	rec := httptest.NewRecorder()
	newTestProxy("http://localhost:1").HandleMessages(rec, req)
	if rec.Code != 400 {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestPassthroughLargeStream(t *testing.T) {
	dir := setupUsageTestDir(t)
	body := "data: " + strings.Repeat("x", maxUsageBuffer+100) + "\n\n" + strings.Repeat("data: "+strings.Repeat("x", 1024)+"\n", 4097) + "\n: done\n\ndata: {\"usage\":{\"input_tokens\":10,\"output_tokens\":3}}\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, body)
	}))
	defer upstream.Close()
	rec := httptest.NewRecorder()
	newTestProxy(upstream.URL).HandleResponses(rec, httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"test-provider/model-a"}`)))
	if rec.Body.String() != body {
		t.Fatalf("large SSE changed: got %d want %d bytes", rec.Body.Len(), len(body))
	}
	_ = usageJSONLPath(t, dir)
}

func TestPassthroughUsageExclusions(t *testing.T) {
	for _, tc := range []struct {
		name, path, body string
		status           int
	}{
		{"count_tokens", "/v1/messages/count_tokens", `{"input_tokens":42}`, 200},
		{"error", "/v1/responses", `{"usage":{"input_tokens":42}}`, 400},
		{"large_json", "/v1/responses", `{"padding":"` + strings.Repeat("x", maxUsageBuffer) + `","usage":{"input_tokens":42}}`, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := setupUsageTestDir(t)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer upstream.Close()
			rec := httptest.NewRecorder()
			newTestProxy(upstream.URL).handleProxy(rec, httptest.NewRequest("POST", tc.path, strings.NewReader(`{"model":"test-provider/model-a"}`)), tc.path)
			if rec.Code != tc.status || rec.Body.String() != tc.body {
				t.Fatal("response changed")
			}
			entries, err := os.ReadDir(dir)
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatal("unexpected usage record")
			}
		})
	}
}
