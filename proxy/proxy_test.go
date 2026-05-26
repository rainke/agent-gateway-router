package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agr/config"
	"agr/router"
)

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
