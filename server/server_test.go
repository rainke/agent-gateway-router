package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agr/config"
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
				Name:        "test",
				APIBaseURL:  "http://localhost:1",
				APIKey:      "sk-test",
				Models:      []string{"m1"},
				Transformer: []string{"openai"},
			},
		},
		Router: map[string]string{
			"default": "test,m1",
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

func TestModelsEndpointNotRegistered(t *testing.T) {
	cfg := newTestConfig()
	srv := New(cfg)

	req, err := http.NewRequest(http.MethodGet, "/v1/models", nil)
	if err != nil {
		t.Fatalf("创建 models 请求失败: %v", err)
	}
	rec := httptest.NewRecorder()

	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("/v1/models 状态码期望 404，实际 %d", rec.Code)
	}
}

func TestServer_StartAndShutdown(t *testing.T) {
	cfg := newTestConfig()
	cfg.Server.Port = 19877
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
	countBody := `{"model":"claude-3","messages":[{"role":"user","content":"hello"}]}`
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
