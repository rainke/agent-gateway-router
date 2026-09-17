package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agr/config"
	"agr/router"
)

func newTestConfig() *config.Config {
	return &config.Config{
		Server: config.ServerConfig{
			Port:     0, // 使用随机端口
			LogLevel: "error",
			PIDFile:  "/tmp/agr-test.pid",
		},
		Providers: []config.Provider{
			{
				Name:       "test",
				APIBaseURL: "http://localhost:1",
				APIKey:     "sk-test",
				Models:     []string{"m1"},
			},
		},
	}
}

func TestNew(t *testing.T) {
	cfg := newTestConfig()
	cfg.Server.Port = 19876
	srv := New(cfg)
	if srv == nil {
		t.Fatal("New 返回 nil")
	}
	if srv.httpServer == nil {
		t.Fatal("httpServer 为 nil")
	}
}

func TestModelsEndpoint(t *testing.T) {
	for _, tc := range []struct {
		name      string
		providers []config.Provider
		want      string
	}{
		{"configured", []config.Provider{
			{Name: "first", APIKey: "secret", APIBaseURL: "http://localhost:1", Models: []string{"shared", "org/model", "shared", ""}},
			{Name: "second", Models: []string{"shared"}},
			{Name: "empty"},
		}, `{"object":"list","data":[{"id":"first/shared","object":"model","created":0,"owned_by":"first"},{"id":"first/org/model","object":"model","created":0,"owned_by":"first"},{"id":"second/shared","object":"model","created":0,"owned_by":"second"}]}`},
		{"no providers", nil, `{"object":"list","data":[]}`},
		{"no models", []config.Provider{{Name: "empty"}}, `{"object":"list","data":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := newTestConfig()
			cfg.Providers = tc.providers
			srv := New(cfg)
			rec := httptest.NewRecorder()
			srv.httpServer.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type = %q", got)
			}
			if got := strings.TrimSpace(rec.Body.String()); got != tc.want {
				t.Errorf("body = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestModelsEndpointMethodNotAllowed(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		t.Run(method, func(t *testing.T) {
			rec := httptest.NewRecorder()
			New(newTestConfig()).httpServer.Handler.ServeHTTP(rec, httptest.NewRequest(method, "/v1/models", nil))
			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want 405", rec.Code)
			}
			if !strings.Contains(rec.Header().Get("Allow"), "GET") {
				t.Errorf("Allow = %q", rec.Header().Get("Allow"))
			}
		})
	}
}

func TestServer_StartAndShutdown(t *testing.T) {
	cfg := newTestConfig()
	cfg.Server.Port = 19877
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages/count_tokens" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"input_tokens":42}`))
	}))
	defer upstream.Close()
	cfg.Providers[0].APIBaseURL = upstream.URL
	srv := New(cfg)

	// 启动服务
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start()
	}()

	// 等待服务启动
	time.Sleep(100 * time.Millisecond)

	// 测试健康检查
	resp, err := http.Get("http://localhost:19877/health")
	if err != nil {
		t.Fatalf("健康检查请求失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("健康检查状态码期望 200，实际 %d", resp.StatusCode)
	}

	// 测试 Ollama 端点返回 501
	resp2, err := http.Get("http://localhost:19877/api/tags")
	if err != nil {
		t.Fatalf("api/tags 请求失败: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != 501 {
		t.Errorf("api/tags 状态码期望 501，实际 %d", resp2.StatusCode)
	}

	var body map[string]any
	json.NewDecoder(resp2.Body).Decode(&body)
	errObj := body["error"].(map[string]any)
	if errObj["code"] != "feature_not_implemented" {
		t.Errorf("错误码不匹配")
	}

	// 测试 Claude count_tokens 端点
	countBody := `{"model":"test/m1","messages":[{"role":"user","content":"hello"}]}`
	resp3, err := http.Post("http://localhost:19877/v1/messages/count_tokens", "application/json", strings.NewReader(countBody))
	if err != nil {
		t.Fatalf("count_tokens 请求失败: %v", err)
	}
	defer resp3.Body.Close()

	if resp3.StatusCode != 200 {
		t.Errorf("count_tokens 状态码期望 200，实际 %d", resp3.StatusCode)
	}

	var countResp map[string]any
	json.NewDecoder(resp3.Body).Decode(&countResp)
	if countResp["input_tokens"].(float64) <= 0 {
		t.Errorf("input_tokens 应大于 0，实际 %v", countResp["input_tokens"])
	}

	// 优雅停机
	if err := srv.Shutdown(); err != nil {
		t.Fatalf("Shutdown 失败: %v", err)
	}

	// 等待 Start 返回
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Start 返回错误: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("等待 Start 返回超时")
	}
}

func TestChatCompletionsForwarded(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[]}`))
	}))
	defer upstream.Close()
	cfg := newTestConfig()
	cfg.Providers[0].APIBaseURL = upstream.URL
	srv := New(cfg)
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"test/m1"}`)))
	if rec.Code != 200 || rec.Body.String() != `{"choices":[]}` {
		t.Fatalf("response=%d %s", rec.Code, rec.Body.String())
	}
}

func TestModelsEndpointHTTP(t *testing.T) {
	cfg := newTestConfig()
	srv := httptest.NewServer(New(cfg).httpServer.Handler)
	defer srv.Close()
	resp, err := srv.Client().Get(srv.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list.Data) != 1 {
		t.Fatalf("models = %+v", list.Data)
	}
	route, err := router.New(cfg).Route(list.Data[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if route.Provider.Name != "test" || route.Model != "m1" {
		t.Fatalf("route = %+v", route)
	}
	head, err := srv.Client().Head(srv.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	defer head.Body.Close()
	if head.StatusCode != http.StatusOK || head.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("HEAD response = %+v", head)
	}
}
