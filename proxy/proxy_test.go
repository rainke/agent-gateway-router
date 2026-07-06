package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agr/config"
	"agr/router"
)

func TestMain(m *testing.M) {
	// 全局设置 usageDir 为临时目录，避免测试写入生产 ~/.agr/usage/
	dir := filepath.Join("/tmp", "agr-usage-test", "global")
	os.RemoveAll(dir)
	usageDir = dir
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func newTestProxy(upstreamURL string) *Proxy {
	cfg := &config.Config{
		Providers: []config.Provider{
			{
				Name:        "test-provider",
				APIBaseURL:  upstreamURL,
				APIKey:      "sk-test",
				Models:      []string{"model-a"},
				Transformer: []string{"openai"},
			},
		},
		Router: map[string]string{
			"default":  "test-provider,model-a",
			"claude-3": "test-provider,model-a",
		},
	}
	r := router.New(cfg)
	return New(cfg, r)
}

func newTestProxyWithTransformer(upstreamURL string, transformers []string) *Proxy {
	cfg := &config.Config{
		Providers: []config.Provider{
			{
				Name:        "test-provider",
				APIBaseURL:  upstreamURL,
				APIKey:      "sk-test",
				Models:      []string{"model-a"},
				Transformer: transformers,
			},
		},
		Router: map[string]string{
			"default":  "test-provider,model-a",
			"claude-3": "test-provider,model-a",
		},
	}
	r := router.New(cfg)
	return New(cfg, r)
}

func TestHandleNotImplemented(t *testing.T) {
	p := newTestProxy("http://localhost")

	endpoints := []string{"/api/chat", "/api/generate", "/api/tags"}
	for _, ep := range endpoints {
		t.Run(ep, func(t *testing.T) {
			req := httptest.NewRequest("GET", ep, nil)
			w := httptest.NewRecorder()
			p.HandleNotImplemented(w, req)

			if w.Code != http.StatusNotImplemented {
				t.Errorf("状态码期望 501，实际 %d", w.Code)
			}

			var resp map[string]any
			json.Unmarshal(w.Body.Bytes(), &resp)
			errObj := resp["error"].(map[string]any)
			if errObj["code"] != "feature_not_implemented" {
				t.Errorf("错误码不匹配")
			}
		})
	}
}

func TestHandleMessages_NonStream(t *testing.T) {
	// 模拟上游返回 OpenAI 格式响应
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 验证请求头
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("Authorization 头不正确")
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type 头不正确")
		}

		// 验证请求体
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		json.Unmarshal(body, &req)
		if req["model"] != "model-a" {
			t.Errorf("上游请求模型应为 model-a，实际 %v", req["model"])
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-123",
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "Hello!"}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 5},
		})
	}))
	defer upstream.Close()

	p := newTestProxy(upstream.URL)

	body := `{"model":"claude-3","messages":[{"role":"user","content":"hi"}],"max_tokens":100,"stream":false}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	p.HandleMessages(w, req)

	if w.Code != 200 {
		t.Fatalf("状态码期望 200，实际 %d，body: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp["type"] != "message" {
		t.Errorf("响应 type 应为 message，实际 %v", resp["type"])
	}
	if resp["role"] != "assistant" {
		t.Errorf("响应 role 应为 assistant")
	}

	content := resp["content"].([]any)
	block := content[0].(map[string]any)
	if block["text"] != "Hello!" {
		t.Errorf("响应文本应为 Hello!，实际 %v", block["text"])
	}
}

func TestHandleMessages_NonStreamReasoningHeader(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-123",
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "Hello!"}, "finish_reason": "stop"}},
		})
	}))
	defer upstream.Close()

	p := newTestProxyWithTransformer(upstream.URL, []string{"openai"})

	body := `{"model":"claude-3","messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"I should inspect the code."},{"type":"text","text":"Hello!"}]}]}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	p.HandleMessages(w, req)

	// x-reasoning-included 只针对 /v1/responses 接口，/v1/messages 不应设置
	if got := w.Header().Get("x-reasoning-included"); got != "" {
		t.Fatalf("x-reasoning-included 头不应在 /v1/messages 中设置，实际 %q", got)
	}
}

func TestHandleMessagesCountTokens(t *testing.T) {
	upstreamCalled := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	p := newTestProxy(upstream.URL)

	body := `{
		"model": "claude-3",
		"system": "You are concise.",
		"messages": [{"role": "user", "content": [{"type": "text", "text": "hello"}]}],
		"tools": [{"name": "bash", "description": "run bash", "input_schema": {"type": "object"}}]
	}`
	req := httptest.NewRequest("POST", "/v1/messages/count_tokens", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	p.HandleMessagesCountTokens(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("状态码期望 200，实际 %d，body: %s", w.Code, w.Body.String())
	}
	if upstreamCalled {
		t.Fatal("count_tokens 不应请求上游")
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应 JSON 解析失败: %v", err)
	}
	if resp["input_tokens"].(float64) <= 0 {
		t.Errorf("input_tokens 应大于 0，实际 %v", resp["input_tokens"])
	}
}

func TestHandleMessagesCountTokens_ToolsIncreaseCount(t *testing.T) {
	base := []byte(`{"model":"claude-3","messages":[{"role":"user","content":"hello"}]}`)
	withTools := []byte(`{"model":"claude-3","messages":[{"role":"user","content":"hello"}],"tools":[{"name":"bash","description":"run bash","input_schema":{"type":"object","properties":{"command":{"type":"string"}}}}]}`)

	baseTokens, err := countClaudeMessageTokens(base, "unknown-model")
	if err != nil {
		t.Fatalf("base 计数失败: %v", err)
	}
	toolTokens, err := countClaudeMessageTokens(withTools, "unknown-model")
	if err != nil {
		t.Fatalf("tools 计数失败: %v", err)
	}
	if toolTokens <= baseTokens {
		t.Fatalf("包含 tools 时 token 数应增加，base=%d tools=%d", baseTokens, toolTokens)
	}
}

func TestHandleMessagesCountTokens_InvalidBody(t *testing.T) {
	p := newTestProxy("http://localhost")

	req := httptest.NewRequest("POST", "/v1/messages/count_tokens", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	p.HandleMessagesCountTokens(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("状态码期望 400，实际 %d", w.Code)
	}
}

// newTestProxyWithAnthropicProvider 创建一个使用 anthropic transformer 的测试代理。
// upstreamURL 是 provider 的 api_base_url（不含 /count_tokens 后缀）。
func newTestProxyWithAnthropicProvider(upstreamURL string) *Proxy {
	cfg := &config.Config{
		Providers: []config.Provider{
			{
				Name:        "anthropic-provider",
				APIBaseURL:  upstreamURL,
				APIKey:      "sk-anthropic",
				Models:      []string{"model-a"},
				Transformer: []string{"anthropic"},
			},
		},
		Router: map[string]string{
			"default":  "anthropic-provider,model-a",
			"claude-3": "anthropic-provider,model-a",
		},
	}
	r := router.New(cfg)
	return New(cfg, r)
}

// TestHandleMessagesCountTokens_AnthropicProvider_ProxiesUpstream 验证当 provider
// 使用 anthropic transformer 时，count_tokens 请求应代理到上游的 /count_tokens 端点，
// 而不是本地用 tiktoken 估算。
func TestHandleMessagesCountTokens_AnthropicProvider_ProxiesUpstream(t *testing.T) {
	var receivedPath string
	var receivedAuth string
	var receivedBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		receivedAuth = r.Header.Get("Authorization")
		receivedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"input_tokens":42}`))
	}))
	defer upstream.Close()

	p := newTestProxyWithAnthropicProvider(upstream.URL)

	body := `{"model":"claude-3","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest("POST", "/v1/messages/count_tokens", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	p.HandleMessagesCountTokens(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("状态码期望 200，实际 %d，body: %s", w.Code, w.Body.String())
	}

	// 上游应被请求，且路径为 /count_tokens
	if receivedPath == "" {
		t.Fatal("anthropic provider 的 count_tokens 应代理到上游")
	}
	if !strings.HasSuffix(receivedPath, "/count_tokens") {
		t.Errorf("上游路径应以 /count_tokens 结尾，实际 %s", receivedPath)
	}

	// Authorization 头应被设置
	if receivedAuth != "Bearer sk-anthropic" {
		t.Errorf("Authorization 头期望 Bearer sk-anthropic，实际 %s", receivedAuth)
	}

	// 上游收到的请求体应保留原始 model（anthropic transformer 对 Messages API 透传）
	var upstreamReq map[string]any
	if err := json.Unmarshal(receivedBody, &upstreamReq); err != nil {
		t.Fatalf("上游请求体 JSON 解析失败: %v", err)
	}
	if upstreamReq["model"] != "model-a" {
		t.Errorf("上游请求 model 应为 model-a（路由后的上游模型），实际 %v", upstreamReq["model"])
	}

	// 响应应原样透传上游返回的 input_tokens
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应 JSON 解析失败: %v", err)
	}
	if resp["input_tokens"].(float64) != 42 {
		t.Errorf("input_tokens 期望 42（上游返回值），实际 %v", resp["input_tokens"])
	}
}

// TestHandleMessagesCountTokens_AnthropicProvider_UsesAPIBaseURL 验证 count_tokens
// 代理 URL 是在 provider 的 api_base_url 基础上追加 /count_tokens。
// 例如 api_base_url=https://api.deepseek.com/anthropic/v1/messages
// 则代理到 https://api.deepseek.com/anthropic/v1/messages/count_tokens
func TestHandleMessagesCountTokens_AnthropicProvider_UsesAPIBaseURL(t *testing.T) {
	var receivedPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"input_tokens":1}`))
	}))
	defer upstream.Close()

	// 模拟 DeepSeek Anthropic 配置：api_base_url 以 /v1/messages 结尾
	p := newTestProxyWithAnthropicProvider(upstream.URL + "/v1/messages")

	body := `{"model":"claude-3","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/messages/count_tokens", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	p.HandleMessagesCountTokens(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("状态码期望 200，实际 %d，body: %s", w.Code, w.Body.String())
	}

	// 路径应为 /v1/messages/count_tokens
	if receivedPath != "/v1/messages/count_tokens" {
		t.Errorf("上游路径期望 /v1/messages/count_tokens，实际 %s", receivedPath)
	}
}

// TestHandleMessagesCountTokens_AnthropicProvider_UpstreamError 验证上游返回错误时，
// 代理应将错误状态码透传给客户端。
func TestHandleMessagesCountTokens_AnthropicProvider_UpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"type":"invalid_request_error","message":"bad request"}}`))
	}))
	defer upstream.Close()

	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(previousLogger)

	p := newTestProxyWithAnthropicProvider(upstream.URL + "/v1/messages")

	body := `{"model":"claude-3","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/messages/count_tokens", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	p.HandleMessagesCountTokens(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("状态码期望 400（透传上游错误），实际 %d", w.Code)
	}

	logOutput := logs.String()
	for _, want := range []string{
		"上游返回非正常状态",
		"path=/v1/messages/count_tokens",
		"provider=anthropic-provider",
		"status=400",
		"bad request",
	} {
		if !strings.Contains(logOutput, want) {
			t.Fatalf("日志应包含 %q，实际日志: %s", want, logOutput)
		}
	}
}

// TestHandleMessagesCountTokens_NonAnthropicProvider_LocalEstimation 验证当 provider
// 不使用 anthropic transformer 时，count_tokens 仍使用本地 tiktoken 估算，不请求上游。
func TestHandleMessagesCountTokens_NonAnthropicProvider_LocalEstimation(t *testing.T) {
	upstreamCalled := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	// 使用默认的 openai transformer（非 anthropic）
	p := newTestProxy(upstream.URL)

	body := `{"model":"claude-3","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest("POST", "/v1/messages/count_tokens", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	p.HandleMessagesCountTokens(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("状态码期望 200，实际 %d，body: %s", w.Code, w.Body.String())
	}
	if upstreamCalled {
		t.Fatal("非 anthropic provider 的 count_tokens 不应请求上游")
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应 JSON 解析失败: %v", err)
	}
	if resp["input_tokens"].(float64) <= 0 {
		t.Errorf("input_tokens 应大于 0（本地估算），实际 %v", resp["input_tokens"])
	}
}

// newTestProxyWithOpenAIAndAnthropicProviders 模拟用户的真实配置：
// 两个 provider 提供相同的上游模型 model-a，一个用 openai transformer（主路由），
// 另一个用 anthropic transformer。主路由指向 openai provider。
func newTestProxyWithOpenAIAndAnthropicProviders(openaiURL, anthropicURL string) *Proxy {
	cfg := &config.Config{
		Providers: []config.Provider{
			{
				Name:        "openai-provider",
				APIBaseURL:  openaiURL,
				APIKey:      "sk-openai",
				Models:      []string{"model-a"},
				Transformer: []string{"openai"},
			},
			{
				Name:        "anthropic-provider",
				APIBaseURL:  anthropicURL,
				APIKey:      "sk-anthropic",
				Models:      []string{"model-a"},
				Transformer: []string{"anthropic"},
			},
		},
		Router: map[string]string{
			"default":  "openai-provider,model-a",
			"claude-3": "openai-provider,model-a",
		},
	}
	r := router.New(cfg)
	return New(cfg, r)
}

// TestHandleMessagesCountTokens_FallbackToAnthropicProvider 验证当主路由的 provider
// 不使用 anthropic transformer 时，count_tokens 应在所有 provider 中查找一个同时满足
// "使用 anthropic transformer" 且 "提供相同上游模型" 的 provider，并代理到它的
// /count_tokens 端点。
//
// 这对应用户的真实配置：deepseek (openai) 和 deepseek-anthropic (anthropic) 都提供
// deepseek-v4-flash，主路由指向 deepseek (openai)，但 count_tokens 应代理到
// deepseek-anthropic 的 /count_tokens 端点。
func TestHandleMessagesCountTokens_FallbackToAnthropicProvider(t *testing.T) {
	var openaiCalled bool
	var anthropicPath string
	var anthropicAuth string
	var anthropicBody []byte

	openaiUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		openaiCalled = true
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer openaiUpstream.Close()

	anthropicUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		anthropicPath = r.URL.Path
		anthropicAuth = r.Header.Get("Authorization")
		anthropicBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"input_tokens":99}`))
	}))
	defer anthropicUpstream.Close()

	// 主路由指向 openai provider，但存在一个 anthropic provider 提供相同模型
	p := newTestProxyWithOpenAIAndAnthropicProviders(openaiUpstream.URL, anthropicUpstream.URL)

	body := `{"model":"claude-3","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest("POST", "/v1/messages/count_tokens", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	p.HandleMessagesCountTokens(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("状态码期望 200，实际 %d，body: %s", w.Code, w.Body.String())
	}

	// 不应请求 openai provider
	if openaiCalled {
		t.Fatal("count_tokens 不应请求主路由的 openai provider")
	}

	// 应代理到 anthropic provider 的 /count_tokens 端点
	if anthropicPath == "" {
		t.Fatal("count_tokens 应回退查找 anthropic provider 并代理到上游")
	}
	if !strings.HasSuffix(anthropicPath, "/count_tokens") {
		t.Errorf("上游路径应以 /count_tokens 结尾，实际 %s", anthropicPath)
	}
	if anthropicAuth != "Bearer sk-anthropic" {
		t.Errorf("Authorization 头期望 Bearer sk-anthropic，实际 %s", anthropicAuth)
	}

	// 上游收到的 model 应为 anthropic provider 的上游模型名（model-a）
	var upstreamReq map[string]any
	if err := json.Unmarshal(anthropicBody, &upstreamReq); err != nil {
		t.Fatalf("上游请求体 JSON 解析失败: %v", err)
	}
	if upstreamReq["model"] != "model-a" {
		t.Errorf("上游请求 model 应为 model-a，实际 %v", upstreamReq["model"])
	}

	// 响应应原样透传上游返回的 input_tokens
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应 JSON 解析失败: %v", err)
	}
	if resp["input_tokens"].(float64) != 99 {
		t.Errorf("input_tokens 期望 99（上游返回值），实际 %v", resp["input_tokens"])
	}
}

// TestHandleMessagesCountTokens_NoAnthropicProviderForModel_LocalEstimation 验证当
// 主路由 provider 不使用 anthropic transformer，且没有任何 anthropic provider 提供相同
// 上游模型时，count_tokens 回退到本地 tiktoken 估算。
func TestHandleMessagesCountTokens_NoAnthropicProviderForModel_LocalEstimation(t *testing.T) {
	upstreamCalled := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	// 只有一个 openai provider，没有 anthropic provider 提供相同模型
	p := newTestProxy(upstream.URL)

	body := `{"model":"claude-3","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest("POST", "/v1/messages/count_tokens", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	p.HandleMessagesCountTokens(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("状态码期望 200，实际 %d，body: %s", w.Code, w.Body.String())
	}
	if upstreamCalled {
		t.Fatal("没有 anthropic provider 时不应请求上游，应使用本地估算")
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应 JSON 解析失败: %v", err)
	}
	if resp["input_tokens"].(float64) <= 0 {
		t.Errorf("input_tokens 应大于 0（本地估算），实际 %v", resp["input_tokens"])
	}
}

func TestHandleMessages_Stream(t *testing.T) {
	// 模拟上游返回 SSE 流式响应
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		flusher := w.(http.Flusher)

		chunks := []string{
			`{"choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{"content":" World"},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		}

		for _, chunk := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()
		}
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	p := newTestProxy(upstream.URL)

	body := `{"model":"claude-3","messages":[{"role":"user","content":"hi"}],"max_tokens":100,"stream":true}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	p.HandleMessages(w, req)

	if w.Code != 200 {
		t.Fatalf("状态码期望 200，实际 %d", w.Code)
	}

	respBody := w.Body.String()

	// 验证包含 Anthropic SSE 事件
	if !strings.Contains(respBody, "event: message_start") {
		t.Error("响应应包含 message_start 事件")
	}
	if !strings.Contains(respBody, "event: content_block_start") {
		t.Error("响应应包含 content_block_start 事件")
	}
	if !strings.Contains(respBody, "event: content_block_delta") {
		t.Error("响应应包含 content_block_delta 事件")
	}
	if !strings.Contains(respBody, "event: content_block_stop") {
		t.Error("响应应包含 content_block_stop 事件")
	}
	if !strings.Contains(respBody, "event: message_delta") {
		t.Error("响应应包含 message_delta 事件")
	}
	if !strings.Contains(respBody, "event: message_stop") {
		t.Error("响应应包含 message_stop 事件")
	}
	if !strings.Contains(respBody, "Hello") {
		t.Error("响应应包含文本 Hello")
	}
	if !strings.Contains(respBody, "World") {
		t.Error("响应应包含文本 World")
	}
}

func TestHandleMessages_StreamReasoningHeader(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		flusher := w.(http.Flusher)
		fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Done\"},\"finish_reason\":null}]}\n\n")
		flusher.Flush()
		fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		flusher.Flush()
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	p := newTestProxyWithTransformer(upstream.URL, []string{"openai"})

	body := `{"model":"claude-3","messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"I should inspect the code."},{"type":"text","text":"Done"}]}],"max_tokens":100,"stream":true}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	p.HandleMessages(w, req)

	// x-reasoning-included 只针对 /v1/responses 接口，/v1/messages 不应设置
	if got := w.Header().Get("x-reasoning-included"); got != "" {
		t.Fatalf("x-reasoning-included 头不应在 /v1/messages 中设置，实际 %q", got)
	}
}

func TestHandleMessages_StreamWithReasoningContent(t *testing.T) {
	// 模拟 DeepSeek 返回 reasoning_content + content 的 SSE 流式响应
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		flusher := w.(http.Flusher)

		chunks := []string{
			`{"choices":[{"index":0,"delta":{"reasoning_content":"I should inspect the code."},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{"content":"Done"},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		}

		for _, chunk := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()
		}
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	p := newTestProxy(upstream.URL)

	body := `{"model":"claude-3","messages":[{"role":"user","content":"hi"}],"max_tokens":100,"stream":true}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	p.HandleMessages(w, req)

	if w.Code != 200 {
		t.Fatalf("状态码期望 200，实际 %d", w.Code)
	}

	respBody := w.Body.String()
	if !strings.Contains(respBody, `"type":"thinking"`) {
		t.Error("响应应包含 thinking content block")
	}
	if !strings.Contains(respBody, `"type":"thinking_delta"`) {
		t.Error("响应应包含 thinking_delta")
	}
	if !strings.Contains(respBody, `"thinking":"I should inspect the code."`) {
		t.Error("响应应包含 reasoning_content 转换后的 thinking")
	}
	if !strings.Contains(respBody, `"type":"text_delta"`) || !strings.Contains(respBody, `"text":"Done"`) {
		t.Error("响应应包含后续文本")
	}
}

func TestHandleMessages_StreamWithToolCalls(t *testing.T) {
	// 模拟上游返回带 tool_calls 的流式响应
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		flusher := w.(http.Flusher)

		chunks := []string{
			`{"choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"bash","arguments":""}}]},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"command\":"}}]},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"ls\"}"}}]},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		}

		for _, chunk := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()
		}
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	p := newTestProxy(upstream.URL)

	body := `{"model":"claude-3","messages":[{"role":"user","content":"run ls"}],"max_tokens":100,"stream":true,"tools":[{"name":"bash","description":"run bash","input_schema":{"type":"object","properties":{"command":{"type":"string"}}}}]}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	p.HandleMessages(w, req)

	respBody := w.Body.String()

	// 验证包含 tool_use 相关事件
	if !strings.Contains(respBody, "tool_use") {
		t.Error("响应应包含 tool_use")
	}
	if !strings.Contains(respBody, "bash") {
		t.Error("响应应包含工具名 bash")
	}
	if !strings.Contains(respBody, "input_json_delta") {
		t.Error("响应应包含 input_json_delta")
	}
	if !strings.Contains(respBody, `"stop_reason":"tool_use"`) {
		t.Error("stop_reason 应为 tool_use")
	}
}

func TestHandleMessages_StreamWithFinalToolArgsOnFinishChunk(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		flusher := w.(http.Flusher)

		chunks := []string{
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"Write","arguments":"{\"file_path\":\"/tmp/README.md\""}}]},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"type":"function","function":{"arguments":"}"}}]},"finish_reason":"tool_calls"}]}`,
		}

		for _, chunk := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()
		}
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	p := newTestProxy(upstream.URL)

	body := `{"model":"claude-3","messages":[{"role":"user","content":"write file"}],"max_tokens":100,"stream":true,"tools":[{"name":"Write","description":"write file","input_schema":{"type":"object","properties":{"file_path":{"type":"string"}}}}]}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	p.HandleMessages(w, req)

	respBody := w.Body.String()
	if !strings.Contains(respBody, `"partial_json":"}"`) {
		t.Fatalf("响应应包含 finish chunk 上的最后 arguments，实际响应: %s", respBody)
	}
	if !strings.Contains(respBody, `"stop_reason":"tool_use"`) {
		t.Error("stop_reason 应为 tool_use")
	}
}

func TestHandleMessages_InvalidBody(t *testing.T) {
	p := newTestProxy("http://localhost")

	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	p.HandleMessages(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("状态码期望 400，实际 %d", w.Code)
	}
}

func TestHandleMessages_MissingModel(t *testing.T) {
	p := newTestProxy("http://localhost")

	body := `{"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	p.HandleMessages(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("状态码期望 400，实际 %d", w.Code)
	}
}

func TestHandleMessages_UpstreamError(t *testing.T) {
	// 上游不可达
	p := newTestProxy("http://127.0.0.1:1")

	body := `{"model":"claude-3","messages":[{"role":"user","content":"hi"}],"max_tokens":100}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	p.HandleMessages(w, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("状态码期望 502，实际 %d", w.Code)
	}
}

func TestHandleResponses_NonStream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-456",
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "Response!"}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 5, "completion_tokens": 3},
		})
	}))
	defer upstream.Close()

	p := newTestProxy(upstream.URL)

	body := `{"model":"claude-3","input":"hello","stream":false}`
	req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	p.HandleResponses(w, req)

	if w.Code != 200 {
		t.Fatalf("状态码期望 200，实际 %d，body: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp["object"] != "response" {
		t.Errorf("object 应为 response，实际 %v", resp["object"])
	}
	if resp["status"] != "completed" {
		t.Errorf("status 应为 completed")
	}
}

func TestHandleResponses_Stream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		flusher := w.(http.Flusher)

		chunks := []string{
			`{"choices":[{"index":0,"delta":{"content":"Hi"},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		}
		for _, chunk := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()
		}
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	p := newTestProxy(upstream.URL)

	body := `{"model":"claude-3","input":"hello","stream":true}`
	req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	p.HandleResponses(w, req)

	if w.Code != 200 {
		t.Fatalf("状态码期望 200，实际 %d", w.Code)
	}

	respBody := w.Body.String()
	if !strings.Contains(respBody, "data:") {
		t.Error("流式响应应包含 data: 前缀")
	}
	if !strings.Contains(respBody, "[DONE]") {
		t.Error("流式响应应包含 [DONE] 标记")
	}
}

func TestHandleResponses_NonStreamReasoningHeader(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-456",
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "Response!"}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 5, "completion_tokens": 3},
		})
	}))
	defer upstream.Close()

	p := newTestProxyWithTransformer(upstream.URL, []string{"openai"})

	body := `{"model":"mimo-v2.5-pro","input":"(4.113+5.666) * 3.22 等于几","include":["reasoning.encrypted_content"],"reasoning":{"effort":"high","summary":"concise"}}`
	req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	p.HandleResponses(w, req)

	if got := w.Header().Get("x-reasoning-included"); got != "true" {
		t.Fatalf("x-reasoning-included 头应为 true，实际 %q", got)
	}
}

func TestHandleResponses_StreamReasoningHeader(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		flusher := w.(http.Flusher)

		chunks := []string{
			`{"choices":[{"index":0,"delta":{"content":"27.65"},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		}
		for _, chunk := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()
		}
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	p := newTestProxyWithTransformer(upstream.URL, []string{"openai"})

	body := `{"model":"mimo-v2.5-pro","input":"(4.113+5.666) * 3.22 等于几","include":["reasoning.encrypted_content"],"reasoning":{"effort":"high","summary":"concise"},"stream":true}`
	req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	p.HandleResponses(w, req)

	if got := w.Header().Get("x-reasoning-included"); got != "true" {
		t.Fatalf("x-reasoning-included 头应为 true，实际 %q", got)
	}
}

func TestHandleResponses_NoReasoningHeaderWithoutConfig(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-789",
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "Hello!"}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 5, "completion_tokens": 3},
		})
	}))
	defer upstream.Close()

	p := newTestProxyWithTransformer(upstream.URL, []string{"openai"})

	// 没有 reasoning 配置和 include 字段
	body := `{"model":"mimo-v2.5-pro","input":"hello"}`
	req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	p.HandleResponses(w, req)

	if got := w.Header().Get("x-reasoning-included"); got != "" {
		t.Fatalf("x-reasoning-included 头不应设置，实际 %q", got)
	}
}

func TestHandleMessages_DefaultRoute(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-789",
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "default"}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 5, "completion_tokens": 1},
		})
	}))
	defer upstream.Close()

	p := newTestProxy(upstream.URL)

	// 使用未配置的模型名，应走默认路由
	body := `{"model":"unknown-model","messages":[{"role":"user","content":"hi"}],"max_tokens":100}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	p.HandleMessages(w, req)

	if w.Code != 200 {
		t.Fatalf("状态码期望 200，实际 %d", w.Code)
	}
}

func TestHandleMessages_WithToolResult(t *testing.T) {
	// 验证包含 tool_result 的多轮对话能正确转换
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		json.Unmarshal(body, &req)

		// 验证 tool_result 被转换为 tool role 消息
		msgs := req["messages"].([]any)
		foundTool := false
		for _, msg := range msgs {
			m := msg.(map[string]any)
			if m["role"] == "tool" {
				foundTool = true
				if m["tool_call_id"] != "tool_1" {
					t.Errorf("tool_call_id 应为 tool_1")
				}
			}
		}
		if !foundTool {
			t.Error("应包含 tool role 消息")
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-tool",
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "Done"}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 20, "completion_tokens": 1},
		})
	}))
	defer upstream.Close()

	p := newTestProxy(upstream.URL)

	body := `{
		"model": "claude-3",
		"messages": [
			{"role": "user", "content": "run ls"},
			{"role": "assistant", "content": [{"type": "tool_use", "id": "tool_1", "name": "bash", "input": {"command": "ls"}}]},
			{"role": "user", "content": [{"type": "tool_result", "tool_use_id": "tool_1", "content": "file1.txt"}]}
		],
		"max_tokens": 100
	}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	p.HandleMessages(w, req)

	if w.Code != 200 {
		t.Fatalf("状态码期望 200，实际 %d，body: %s", w.Code, w.Body.String())
	}
}

func TestExtractModel(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr bool
		want    string
	}{
		{"valid", `{"model":"gpt-4"}`, false, "gpt-4"},
		{"missing model", `{"messages":[]}`, true, ""},
		{"invalid json", `not json`, true, ""},
		{"model not string", `{"model":123}`, true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := extractModel([]byte(tt.body), "/v1/messages")
			if tt.wantErr && err == nil {
				t.Error("期望返回错误")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("不期望错误: %v", err)
			}
			if !tt.wantErr && result != tt.want {
				t.Errorf("期望 %s，实际 %s", tt.want, result)
			}
		})
	}
}

func TestHandleMessages_StreamWithDataNoSpace(t *testing.T) {
	// 测试 "data:" 格式（无空格）
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		flusher := w.(http.Flusher)

		// 使用 "data:" 格式（无空格）
		fmt.Fprintf(w, "data:{\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hi\"},\"finish_reason\":null}]}\n\n")
		flusher.Flush()
		fmt.Fprintf(w, "data:{\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		flusher.Flush()
		fmt.Fprintf(w, "data:[DONE]\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	p := newTestProxy(upstream.URL)

	body := `{"model":"claude-3","messages":[{"role":"user","content":"hi"}],"max_tokens":100,"stream":true}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	p.HandleMessages(w, req)

	if w.Code != 200 {
		t.Fatalf("状态码期望 200，实际 %d", w.Code)
	}

	respBody := w.Body.String()
	if !strings.Contains(respBody, "Hi") {
		t.Error("响应应包含文本 Hi")
	}
}

func TestHandleMessages_NonStreamWithToolCalls(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-tc",
			"choices": []map[string]any{{
				"message": map[string]any{
					"role":    "assistant",
					"content": nil,
					"tool_calls": []map[string]any{{
						"id":       "call_1",
						"type":     "function",
						"function": map[string]any{"name": "bash", "arguments": `{"command":"ls"}`},
					}},
				},
				"finish_reason": "tool_calls",
			}},
			"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 15},
		})
	}))
	defer upstream.Close()

	p := newTestProxy(upstream.URL)

	body := `{"model":"claude-3","messages":[{"role":"user","content":"run ls"}],"max_tokens":100,"tools":[{"name":"bash","description":"run bash","input_schema":{"type":"object","properties":{"command":{"type":"string"}}}}]}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	p.HandleMessages(w, req)

	if w.Code != 200 {
		t.Fatalf("状态码期望 200，实际 %d，body: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp["stop_reason"] != "tool_use" {
		t.Errorf("stop_reason 应为 tool_use，实际 %v", resp["stop_reason"])
	}

	content := resp["content"].([]any)
	found := false
	for _, block := range content {
		b := block.(map[string]any)
		if b["type"] == "tool_use" {
			found = true
			if b["name"] != "bash" {
				t.Errorf("tool name 应为 bash")
			}
		}
	}
	if !found {
		t.Error("响应应包含 tool_use block")
	}
}

func TestHandleMessages_AcceptHeader(t *testing.T) {
	var receivedAccept string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAccept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-h",
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "ok"}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 1, "completion_tokens": 1},
		})
	}))
	defer upstream.Close()

	p := newTestProxy(upstream.URL)

	body := `{"model":"claude-3","messages":[{"role":"user","content":"hi"}],"max_tokens":10}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()

	p.HandleMessages(w, req)

	if receivedAccept != "application/json" {
		t.Errorf("Accept 头应被转发，实际 %q", receivedAccept)
	}
}

func TestHandleMessages_NoAPIKey(t *testing.T) {
	var receivedAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-nokey",
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "ok"}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 1, "completion_tokens": 1},
		})
	}))
	defer upstream.Close()

	// 创建没有 API key 的 proxy
	cfg := &config.Config{
		Providers: []config.Provider{
			{
				Name:        "nokey",
				APIBaseURL:  upstream.URL,
				APIKey:      "",
				Models:      []string{"m1"},
				Transformer: []string{"openai"},
			},
		},
		Router: map[string]string{"default": "nokey,m1"},
	}
	r := router.New(cfg)
	p := New(cfg, r)

	body := `{"model":"test","messages":[{"role":"user","content":"hi"}],"max_tokens":10}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	p.HandleMessages(w, req)

	if receivedAuth != "" {
		t.Errorf("无 API key 时不应发送 Authorization 头，实际 %q", receivedAuth)
	}
}

func TestHandleResponses_InvalidBody(t *testing.T) {
	p := newTestProxy("http://localhost")

	req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	p.HandleResponses(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("状态码期望 400，实际 %d", w.Code)
	}
}

func TestHandleResponses_StreamWithEventLines(t *testing.T) {
	// 测试 Codex 流式响应转换为 Responses API 格式
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		flusher := w.(http.Flusher)

		fmt.Fprintf(w, "event: message\n")
		fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"test\"},\"finish_reason\":null}]}\n\n")
		flusher.Flush()
		fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		flusher.Flush()
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	p := newTestProxy(upstream.URL)

	body := `{"model":"claude-3","input":"hello","stream":true}`
	req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	p.HandleResponses(w, req)

	if w.Code != 200 {
		t.Fatalf("状态码期望 200，实际 %d", w.Code)
	}

	respBody := w.Body.String()
	// 应包含 Responses API 事件
	if !strings.Contains(respBody, "event: response.created") {
		t.Error("应包含 response.created 事件")
	}
	if !strings.Contains(respBody, "event: response.output_text.delta") {
		t.Error("应包含 response.output_text.delta 事件")
	}
	if !strings.Contains(respBody, "event: response.completed") {
		t.Error("应包含 response.completed 事件")
	}
	if !strings.Contains(respBody, "[DONE]") {
		t.Error("应包含 [DONE]")
	}
}

func TestHandleMessages_UpstreamNonStreamError(t *testing.T) {
	// 上游返回非 200 状态码
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(500)
		w.Write([]byte(`{"error":"internal server error"}`))
	}))
	defer upstream.Close()

	p := newTestProxy(upstream.URL)

	body := `{"model":"claude-3","messages":[{"role":"user","content":"hi"}],"max_tokens":100}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	p.HandleMessages(w, req)

	// 非流式响应会透传上游状态码
	if w.Code != 500 {
		t.Errorf("状态码期望 500，实际 %d", w.Code)
	}
}

func TestHandleMessages_LogsUpstreamAbnormalResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"message":"invalid request from provider"}}`))
	}))
	defer upstream.Close()

	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(previousLogger)

	p := newTestProxy(upstream.URL)

	body := `{"model":"claude-3","messages":[{"role":"user","content":"hi"}],"max_tokens":100}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	p.HandleMessages(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("状态码期望 400，实际 %d", w.Code)
	}

	logOutput := logs.String()
	for _, want := range []string{
		"上游返回非正常状态",
		"provider=test-provider",
		"status=400",
		"body=",
		"invalid request from provider",
	} {
		if !strings.Contains(logOutput, want) {
			t.Fatalf("日志应包含 %q，实际日志: %s", want, logOutput)
		}
	}
}

func TestLogUpstreamAbnormalResponse_SkipsNormalStatus(t *testing.T) {
	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(previousLogger)

	logUpstreamAbnormalResponse(http.StatusOK, "provider", "model", "/v1/messages", "application/json", []byte(`{"ok":true}`))

	if logs.Len() != 0 {
		t.Fatalf("正常上游状态不应记录错误日志，实际日志: %s", logs.String())
	}
}

func TestLogUpstreamAbnormalResponse_TruncatesBody(t *testing.T) {
	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(previousLogger)

	body := strings.Repeat("a", maxLoggedUpstreamBodyBytes+20)
	logUpstreamAbnormalResponse(http.StatusBadGateway, "provider", "model", "/v1/messages", "text/plain", []byte(body))

	logOutput := logs.String()
	if !strings.Contains(logOutput, "...(truncated)") {
		t.Fatalf("超长上游响应体应被截断，实际日志: %s", logOutput)
	}
	if strings.Contains(logOutput, strings.Repeat("a", maxLoggedUpstreamBodyBytes+20)) {
		t.Fatalf("日志不应包含完整超长响应体")
	}
}

func TestHandleMessages_StreamFinishLength(t *testing.T) {
	// 测试 finish_reason=length 的流式响应
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		flusher := w.(http.Flusher)

		fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"},\"finish_reason\":null}]}\n\n")
		flusher.Flush()
		fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"length\"}]}\n\n")
		flusher.Flush()
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	p := newTestProxy(upstream.URL)

	body := `{"model":"claude-3","messages":[{"role":"user","content":"hi"}],"max_tokens":5,"stream":true}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	p.HandleMessages(w, req)

	respBody := w.Body.String()
	if !strings.Contains(respBody, `"stop_reason":"max_tokens"`) {
		t.Error("stop_reason 应为 max_tokens")
	}
}

func TestHandleResponses_StreamTextComplete(t *testing.T) {
	// 测试 Codex 流式响应完整的文本生成流程
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		flusher := w.(http.Flusher)

		chunks := []string{
			`{"choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{"content":" World"},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`,
		}
		for _, chunk := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()
		}
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	p := newTestProxy(upstream.URL)

	body := `{"model":"claude-3","input":"hello","stream":true}`
	req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	p.HandleResponses(w, req)

	if w.Code != 200 {
		t.Fatalf("状态码期望 200，实际 %d", w.Code)
	}

	respBody := w.Body.String()

	// 验证完整的 Responses API 事件生命周期
	if !strings.Contains(respBody, "event: response.created") {
		t.Error("应包含 response.created")
	}
	if !strings.Contains(respBody, "event: response.in_progress") {
		t.Error("应包含 response.in_progress")
	}
	if !strings.Contains(respBody, "event: response.output_item.added") {
		t.Error("应包含 response.output_item.added")
	}
	if !strings.Contains(respBody, "event: response.content_part.added") {
		t.Error("应包含 response.content_part.added")
	}
	if !strings.Contains(respBody, "event: response.output_text.delta") {
		t.Error("应包含 response.output_text.delta")
	}
	if !strings.Contains(respBody, "event: response.output_text.done") {
		t.Error("应包含 response.output_text.done")
	}
	if !strings.Contains(respBody, "event: response.content_part.done") {
		t.Error("应包含 response.content_part.done")
	}
	if !strings.Contains(respBody, "event: response.output_item.done") {
		t.Error("应包含 response.output_item.done")
	}
	if !strings.Contains(respBody, "event: response.completed") {
		t.Error("应包含 response.completed")
	}
	if !strings.Contains(respBody, "[DONE]") {
		t.Error("应包含 [DONE]")
	}
	// 验证文本内容
	if !strings.Contains(respBody, `"delta":"Hello"`) {
		t.Error("应包含 delta Hello")
	}
	if !strings.Contains(respBody, `"delta":" World"`) {
		t.Error("应包含 delta World")
	}
}

func TestHandleResponses_StreamWithToolCalls(t *testing.T) {
	// 测试 Codex 流式响应中的 tool call
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		flusher := w.(http.Flusher)

		chunks := []string{
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"bash","arguments":""}}]},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"command\":"}}]},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"ls\"}"}}]},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		}
		for _, chunk := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()
		}
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	p := newTestProxy(upstream.URL)

	body := `{"model":"claude-3","input":"run ls","stream":true}`
	req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	p.HandleResponses(w, req)

	if w.Code != 200 {
		t.Fatalf("状态码期望 200，实际 %d", w.Code)
	}

	respBody := w.Body.String()

	if !strings.Contains(respBody, "event: response.output_item.added") {
		t.Error("应包含 response.output_item.added")
	}
	if !strings.Contains(respBody, "function_call") {
		t.Error("应包含 function_call")
	}
	if !strings.Contains(respBody, "bash") {
		t.Error("应包含工具名 bash")
	}
	if !strings.Contains(respBody, "event: response.function_call_arguments.delta") {
		t.Error("应包含 response.function_call_arguments.delta")
	}
	if !strings.Contains(respBody, "event: response.function_call_arguments.done") {
		t.Error("应包含 response.function_call_arguments.done")
	}
	if !strings.Contains(respBody, "event: response.completed") {
		t.Error("应包含 response.completed")
	}
}

func TestHandleResponses_StreamFinishLength(t *testing.T) {
	// 测试 finish_reason=length 时 Codex 流式响应的 status
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		flusher := w.(http.Flusher)

		fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"},\"finish_reason\":null}]}\n\n")
		flusher.Flush()
		fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"length\"}]}\n\n")
		flusher.Flush()
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	p := newTestProxy(upstream.URL)

	body := `{"model":"claude-3","input":"hello","stream":true,"max_output_tokens":5}`
	req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	p.HandleResponses(w, req)

	respBody := w.Body.String()
	if !strings.Contains(respBody, `"status":"incomplete"`) {
		t.Error("finish_reason=length 时 status 应为 incomplete")
	}
}

func TestHandleMessages_RouteNotFound(t *testing.T) {
	// 创建没有默认路由的 proxy
	cfg := &config.Config{
		Providers: []config.Provider{
			{
				Name:        "p1",
				APIBaseURL:  "http://localhost:1",
				APIKey:      "sk-test",
				Models:      []string{"m1"},
				Transformer: []string{"openai"},
			},
		},
		Router: map[string]string{
			"specific-only": "p1,m1",
		},
	}
	r := router.New(cfg)
	p := New(cfg, r)

	body := `{"model":"unknown","messages":[{"role":"user","content":"hi"}],"max_tokens":10}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	p.HandleMessages(w, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("状态码期望 502，实际 %d", w.Code)
	}
}
