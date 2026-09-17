package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agr/adaptor"
	"agr/config"
	"agr/router"
	"fmt"
)

func newProxyWithProviders(providers ...config.Provider) *Proxy {
	cfg := &config.Config{Providers: providers}
	return New(cfg, router.New(cfg))
}

func TestHandleProxy_ConfiguredMiniMaxAdaptor(t *testing.T) {
	var received []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"1","choices":[]}`)
	}))
	defer upstream.Close()

	p := newProxyWithProviders(config.Provider{
		Name:       "mm", // 名称不必是 minimax，由 adaptors 配置驱动
		APIBaseURL: upstream.URL,
		APIKey:     "sk-mm",
		Models:     []string{"MiniMax-M3"},
		Adaptors:   []string{"minimax"},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"mm/MiniMax-M3","messages":[{"role":"user","content":"hi"}]}`))
	p.HandleChatCompletions(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(received, &payload); err != nil {
		t.Fatalf("上游请求体非法: %v", err)
	}
	if string(payload["model"]) != `"MiniMax-M3"` {
		t.Errorf("model = %s", payload["model"])
	}
	if string(payload["reasoning_split"]) != "true" {
		t.Errorf("reasoning_split = %s, want true", payload["reasoning_split"])
	}
}

func TestHandleProxy_MiniMaxPreservesClientReasoningSplit(t *testing.T) {
	var received []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{}`)
	}))
	defer upstream.Close()

	p := newProxyWithProviders(config.Provider{
		Name:       "minimax",
		APIBaseURL: upstream.URL,
		APIKey:     "sk-mm",
		Models:     []string{"MiniMax-M3"},
		Adaptors:   []string{"minimax"},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"minimax/MiniMax-M3","reasoning_split":false,"messages":[]}`))
	p.HandleChatCompletions(rec, req)

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(received, &payload); err != nil {
		t.Fatal(err)
	}
	if string(payload["reasoning_split"]) != "false" {
		t.Errorf("reasoning_split = %s, want false", payload["reasoning_split"])
	}
}

func TestHandleProxy_NoAdaptorConfigured(t *testing.T) {
	var received []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{}`)
	}))
	defer upstream.Close()

	// 即便 provider 名为 minimax，未配置 adaptors 也不注入
	p := newProxyWithProviders(config.Provider{
		Name:       "minimax",
		APIBaseURL: upstream.URL,
		APIKey:     "sk-mm",
		Models:     []string{"MiniMax-M3"},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"minimax/MiniMax-M3","messages":[]}`))
	p.HandleChatCompletions(rec, req)

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(received, &payload); err != nil {
		t.Fatal(err)
	}
	if _, exists := payload["reasoning_split"]; exists {
		t.Errorf("未配置 adaptors 不应注入: %s", received)
	}
}

func TestHandleProxy_MiniMaxMessagesUnchanged(t *testing.T) {
	var received []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{}`)
	}))
	defer upstream.Close()

	p := newProxyWithProviders(config.Provider{
		Name:       "minimax",
		APIBaseURL: upstream.URL,
		APIKey:     "sk-mm",
		Models:     []string{"MiniMax-M3"},
		Adaptors:   []string{"minimax"},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/messages",
		strings.NewReader(`{"model":"minimax/MiniMax-M3","messages":[]}`))
	p.HandleMessages(rec, req)

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(received, &payload); err != nil {
		t.Fatal(err)
	}
	if _, exists := payload["reasoning_split"]; exists {
		t.Errorf("messages 端点不应注入: %s", received)
	}
}

type requestAdaptor func(*adaptor.Request) error

func (f requestAdaptor) Apply(req *adaptor.Request) error { return f(req) }

func TestHandleProxy_AdaptorRequest(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			called := false
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				if r.Method != http.MethodPut || r.URL.Path != "/v1/custom" || r.URL.RawQuery != "base=1&adapted=2" {
					t.Errorf("upstream request: %s %s", r.Method, r.URL)
				}
				if len(r.Header.Values("X-Adapted")) != 2 || r.Header.Get("X-Removed") != "" || r.Header.Get("X-Hop") != "" {
					t.Errorf("headers were not adapted or sanitized")
				}
				if r.Header.Get("Authorization") != "Bearer provider-key" {
					t.Error("provider credentials lost")
				}
				body, _ := io.ReadAll(r.Body)
				if string(body) != `{"model":"upstream","new":true}` {
					t.Errorf("body=%s", body)
				}
				if r.ContentLength != int64(len(body)) {
					t.Error("incorrect content length")
				}
				io.WriteString(w, `{}`)
			}))
			defer upstream.Close()
			name := "test-request-" + fmt.Sprint(fail)
			adaptor.Register(name, requestAdaptor(func(req *adaptor.Request) error {
				if req.Path != "/v1/chat/completions" || req.Method != http.MethodPost || req.RawQuery != "client=1" || req.Headers.Get("X-Removed") != "yes" || string(req.Body["model"]) != `"upstream"` {
					t.Errorf("incomplete adaptor request: path=%s method=%s", req.Path, req.Method)
				}
				if fail {
					return fmt.Errorf("test failure")
				}
				req.Body = map[string]json.RawMessage{"model": json.RawMessage(`"upstream"`), "new": json.RawMessage(`true`)}
				req.Headers = http.Header{"X-Adapted": []string{"one", "two"}, "Connection": []string{"X-Hop"}, "X-Hop": []string{"remove"}}
				req.Path = "/v1/custom"
				req.Method = http.MethodPut
				req.RawQuery = "adapted=2"
				return nil
			}))
			p := newProxyWithProviders(config.Provider{Name: "test", APIBaseURL: upstream.URL + "/v1?base=1", APIKey: "provider-key", Models: []string{"upstream"}, Adaptors: []string{name}})
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions?client=1", strings.NewReader(`{"model":"test/upstream"}`))
			req.Header.Set("X-Removed", "yes")
			rec := httptest.NewRecorder()
			p.HandleChatCompletions(rec, req)
			if fail {
				if rec.Code != http.StatusBadRequest || called {
					t.Fatalf("status=%d called=%v", rec.Code, called)
				}
			} else if rec.Code != http.StatusOK || !called {
				t.Fatalf("status=%d called=%v body=%s", rec.Code, called, rec.Body.String())
			}
			if req.Header.Get("X-Removed") != "yes" || req.URL.RawQuery != "client=1" {
				t.Error("original request mutated")
			}
		})
	}
}
