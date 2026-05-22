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
				Transformer: []string{"openai-to-custom"},
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
				Transformer: []string{"openai-to-custom"},
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
	// 测试包含 event: 行的流式响应
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		flusher := w.(http.Flusher)

		fmt.Fprintf(w, "event: message\n")
		fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"test\"},\"finish_reason\":null}]}\n\n")
		flusher.Flush()
		fmt.Fprintf(w, "\n") // 空行
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
	if !strings.Contains(respBody, "event: message") {
		t.Error("event: 行应被透传")
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

func TestHandleMessages_RouteNotFound(t *testing.T) {
	// 创建没有默认路由的 proxy
	cfg := &config.Config{
		Providers: []config.Provider{
			{
				Name:        "p1",
				APIBaseURL:  "http://localhost:1",
				APIKey:      "sk-test",
				Models:      []string{"m1"},
				Transformer: []string{"openai-to-custom"},
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
