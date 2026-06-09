package anthropic

import (
	"context"
	"encoding/json"
	"testing"

	"agr/transformer/tctx"
)

// ====================
// Registry / Wiring
// ====================

func TestAnthropic_Registry(t *testing.T) {
	tr := New()
	if tr == nil {
		t.Fatal("New() 不应返回 nil")
	}
}

// ====================
// TransformRequest
// ====================

func TestAnthropic_CodexRequest_ConvertsToMessages(t *testing.T) {
	tr := New()
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.UpstreamModelKey, "claude-sonnet-4-5")

	body := []byte(`{
		"model": "client-model",
		"input": "hello world",
		"instructions": "be nice"
	}`)

	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("Codex 请求转换应成功: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(result, &out); err != nil {
		t.Fatalf("结果不是合法 JSON: %v", err)
	}

	// 上游模型应被覆盖
	if out["model"] != "claude-sonnet-4-5" {
		t.Errorf("期望 model=claude-sonnet-4-5，实际 %v", out["model"])
	}

	// 顶层 system 字段（来自 instructions）
	if out["system"] != "be nice" {
		t.Errorf("期望 system=be nice，实际 %v", out["system"])
	}

	// messages 应包含 user 消息
	msgs, ok := out["messages"].([]any)
	if !ok || len(msgs) == 0 {
		t.Fatalf("期望 messages 数组，实际 %v", out["messages"])
	}
	if msgs[0].(map[string]any)["content"] != "hello world" {
		t.Errorf("期望 user content=hello world，实际 %v", msgs[0])
	}
}

func TestAnthropic_CodexRequest_ArrayInput(t *testing.T) {
	tr := New()
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.UpstreamModelKey, "claude-haiku-4-5")

	body := []byte(`{
		"model": "client-model",
		"input": [
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "hi"}]},
			{"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": "hello"}]},
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "how are you?"}]}
		]
	}`)

	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	var out map[string]any
	_ = json.Unmarshal(result, &out)

	msgs := out["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("期望 3 条消息，实际 %d", len(msgs))
	}
	if msgs[0].(map[string]any)["content"] != "hi" {
		t.Errorf("第 1 条消息内容错误: %v", msgs[0])
	}
	if msgs[1].(map[string]any)["content"] != "hello" {
		t.Errorf("第 2 条消息内容错误: %v", msgs[1])
	}
	if msgs[2].(map[string]any)["content"] != "how are you?" {
		t.Errorf("第 3 条消息内容错误: %v", msgs[2])
	}
}

func TestAnthropic_CodexRequest_WithFunctionCall(t *testing.T) {
	tr := New()
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.UpstreamModelKey, "claude-sonnet-4-5")

	body := []byte(`{
		"model": "client-model",
		"input": [
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "what's the weather?"}]},
			{"type": "function_call", "call_id": "call_1", "name": "get_weather", "arguments": "{\"city\":\"SF\"}"},
			{"type": "function_call_output", "call_id": "call_1", "output": "sunny"}
		]
	}`)

	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	var out map[string]any
	_ = json.Unmarshal(result, &out)

	msgs := out["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("期望 3 条消息 (user/assistant-with-tool/tool)，实际 %d", len(msgs))
	}

	assistant := msgs[1].(map[string]any)
	if assistant["role"] != "assistant" {
		t.Errorf("期望第 2 条 role=assistant，实际 %v", assistant["role"])
	}
	toolCalls, ok := assistant["tool_calls"].([]any)
	if !ok || len(toolCalls) != 1 {
		t.Fatalf("期望 assistant 携带 1 个 tool_call，实际 %v", assistant)
	}
	tc := toolCalls[0].(map[string]any)
	if tc["id"] != "call_1" {
		t.Errorf("期望 tool_call id=call_1，实际 %v", tc["id"])
	}
	fn := tc["function"].(map[string]any)
	if fn["name"] != "get_weather" {
		t.Errorf("期望 tool_call name=get_weather，实际 %v", fn["name"])
	}

	toolMsg := msgs[2].(map[string]any)
	if toolMsg["role"] != "tool" {
		t.Errorf("期望第 3 条 role=tool，实际 %v", toolMsg["role"])
	}
	if toolMsg["tool_call_id"] != "call_1" {
		t.Errorf("期望 tool_call_id=call_1，实际 %v", toolMsg["tool_call_id"])
	}
}

func TestAnthropic_CodexRequest_ToolsConvertedToAnthropicFormat(t *testing.T) {
	tr := New()
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.UpstreamModelKey, "claude-sonnet-4-5")

	body := []byte(`{
		"model": "client-model",
		"input": "hi",
		"tools": [
			{"type": "function", "name": "get_weather", "description": "Get weather", "parameters": {"type": "object"}}
		]
	}`)

	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	var out map[string]any
	_ = json.Unmarshal(result, &out)

	tools, ok := out["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("期望 1 个 tool，实际 %v", out["tools"])
	}
	tool := tools[0].(map[string]any)
	// Anthropic 格式：name 顶级、input_schema 顶级，无 function 嵌套
	if tool["name"] != "get_weather" {
		t.Errorf("期望 name=get_weather，实际 %v", tool["name"])
	}
	if _, ok := tool["input_schema"]; !ok {
		t.Errorf("期望 input_schema 字段，实际 %v", tool)
	}
	if _, ok := tool["function"]; ok {
		t.Errorf("Anthropic 格式不应有 function 字段，实际 %v", tool)
	}
}

func TestAnthropic_CodexRequest_ReasoningEffortMapsToThinking(t *testing.T) {
	tr := New()
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.UpstreamModelKey, "claude-sonnet-4-5")

	body := []byte(`{
		"model": "client-model",
		"input": "hi",
		"reasoning": {"effort": "high"}
	}`)

	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	var out map[string]any
	_ = json.Unmarshal(result, &out)

	thinking, ok := out["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("期望 thinking 字段，实际 %v", out)
	}
	if thinking["type"] != "enabled" {
		t.Errorf("期望 thinking.type=enabled，实际 %v", thinking["type"])
	}
}

func TestAnthropic_CodexRequest_MaxOutputTokens(t *testing.T) {
	tr := New()
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.UpstreamModelKey, "claude-sonnet-4-5")

	body := []byte(`{
		"model": "client-model",
		"input": "hi",
		"max_output_tokens": 1024,
		"temperature": 0.5
	}`)

	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	var out map[string]any
	_ = json.Unmarshal(result, &out)

	if out["max_tokens"] != float64(1024) {
		t.Errorf("期望 max_tokens=1024，实际 %v", out["max_tokens"])
	}
	if out["temperature"] != 0.5 {
		t.Errorf("期望 temperature=0.5，实际 %v", out["temperature"])
	}
}

func TestAnthropic_CodexRequest_StreamFlag(t *testing.T) {
	tr := New()
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.UpstreamModelKey, "claude-sonnet-4-5")

	body := []byte(`{
		"model": "client-model",
		"input": "hi",
		"stream": true
	}`)

	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	var out map[string]any
	_ = json.Unmarshal(result, &out)

	if out["stream"] != true {
		t.Errorf("期望 stream=true，实际 %v", out["stream"])
	}
}

func TestAnthropic_CodexRequest_InvalidJSON_Passthrough(t *testing.T) {
	tr := New()
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.UpstreamModelKey, "claude-sonnet-4-5")

	body := []byte(`{not valid json`)
	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("无效 JSON 不应报错: %v", err)
	}
	if string(result) != string(body) {
		t.Errorf("无效 JSON 应透传")
	}
}

// ====================
// TransformResponse
// ====================

func TestAnthropic_MessagesResponse_ConvertsToCodex(t *testing.T) {
	tr := New()
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.ClientModelKey, "client-model")

	body := []byte(`{
		"id": "msg_123",
		"type": "message",
		"role": "assistant",
		"model": "claude-sonnet-4-5",
		"content": [{"type": "text", "text": "Hello there!"}],
		"stop_reason": "end_turn",
		"usage": {"input_tokens": 10, "output_tokens": 5}
	}`)

	result, err := tr.TransformResponse(ctx, body)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	var out map[string]any
	_ = json.Unmarshal(result, &out)

	if out["object"] != "response" {
		t.Errorf("期望 object=response，实际 %v", out["object"])
	}
	if out["model"] != "client-model" {
		t.Errorf("期望 model=client-model，实际 %v", out["model"])
	}
	if out["status"] != "completed" {
		t.Errorf("期望 status=completed，实际 %v", out["status"])
	}

	output, ok := out["output"].([]any)
	if !ok || len(output) == 0 {
		t.Fatalf("期望 output 数组，实际 %v", out["output"])
	}
	msg := output[0].(map[string]any)
	if msg["type"] != "message" {
		t.Errorf("期望 type=message，实际 %v", msg["type"])
	}
	content := msg["content"].([]any)
	if content[0].(map[string]any)["text"] != "Hello there!" {
		t.Errorf("期望 text=Hello there!，实际 %v", content)
	}
}

func TestAnthropic_MessagesResponse_ToolUse(t *testing.T) {
	tr := New()
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.ClientModelKey, "client-model")

	body := []byte(`{
		"id": "msg_456",
		"role": "assistant",
		"content": [
			{"type": "text", "text": "Let me check"},
			{"type": "tool_use", "id": "toolu_1", "name": "get_weather", "input": {"city": "SF"}}
		],
		"stop_reason": "tool_use",
		"usage": {"input_tokens": 10, "output_tokens": 5}
	}`)

	result, err := tr.TransformResponse(ctx, body)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	var out map[string]any
	_ = json.Unmarshal(result, &out)

	if out["status"] != "incomplete" {
		// tool_use 状态可能映射
		t.Logf("status=%v", out["status"])
	}
	output := out["output"].([]any)
	// 应该有 text message 和 function_call
	if len(output) < 2 {
		t.Fatalf("期望至少 2 个 output item，实际 %d", len(output))
	}

	var hasFunctionCall bool
	for _, item := range output {
		m := item.(map[string]any)
		if m["type"] == "function_call" {
			hasFunctionCall = true
			if m["name"] != "get_weather" {
				t.Errorf("期望 name=get_weather，实际 %v", m["name"])
			}
			if m["call_id"] != "toolu_1" {
				t.Errorf("期望 call_id=toolu_1，实际 %v", m["call_id"])
			}
		}
	}
	if !hasFunctionCall {
		t.Errorf("output 中应包含 function_call")
	}
}

func TestAnthropic_MessagesResponse_InvalidJSON_Passthrough(t *testing.T) {
	tr := New()
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	body := []byte(`{broken`)

	result, err := tr.TransformResponse(ctx, body)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if string(result) != string(body) {
		t.Errorf("无效 JSON 应透传")
	}
}

// ====================
// TransformStream (Anthropic Messages SSE -> Codex SSE)
// ====================

func TestAnthropic_Stream_NonSSE_Passthrough(t *testing.T) {
	tr := New()
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	chunk := []byte(`not sse data`)

	result, err := tr.TransformStream(ctx, chunk)
	if err != nil {
		t.Fatalf("非 SSE 应透传: %v", err)
	}
	if string(result) != string(chunk) {
		t.Errorf("非 SSE 应透传")
	}
}

func TestAnthropic_Stream_MessageStart(t *testing.T) {
	tr := New()
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.ClientModelKey, "client-model")
	ctx = context.WithValue(ctx, openaiStreamStateKey, &StreamState{
		ResponseID: "resp_1",
		Model:      "client-model",
	})

	sseChunk := []byte(`event: message_start
data: {"type":"message_start","message":{"id":"msg_1","role":"assistant","content":[],"model":"claude","usage":{"input_tokens":10}}}

`)

	result, err := tr.TransformStream(ctx, sseChunk)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	// 应返回至少一个 Codex 事件
	events := splitCodexEvents(t, result)
	if len(events) == 0 {
		t.Fatalf("期望至少 1 个事件，实际 0")
	}
	// 第一事件应该是 response.created
	if eventType(events[0]) != "response.created" {
		t.Errorf("首事件应为 response.created，实际 %s", eventType(events[0]))
	}
}

func TestAnthropic_Stream_ContentBlockDelta_Text(t *testing.T) {
	tr := New()
	state := &StreamState{
		ResponseID: "resp_1",
		Model:      "client-model",
		Started:    true,
	}
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.ClientModelKey, "client-model")
	ctx = context.WithValue(ctx, openaiStreamStateKey, state)

	sseChunk := []byte(`event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}

`)

	result, err := tr.TransformStream(ctx, sseChunk)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	events := splitCodexEvents(t, result)
	if len(events) == 0 {
		t.Fatalf("期望事件，实际 0")
	}
	// 应包含 text delta
	var foundTextDelta bool
	for _, e := range events {
		if eventType(e) == "response.output_text.delta" {
			foundTextDelta = true
			var data map[string]any
			_ = json.Unmarshal(e, &data)
			if data["delta"] != "hello" {
				t.Errorf("期望 delta=hello，实际 %v", data["delta"])
			}
		}
	}
	if !foundTextDelta {
		t.Errorf("期望 response.output_text.delta 事件，实际事件: %s", string(result))
	}
}

func TestAnthropic_Stream_ContentBlockStart_Thinking(t *testing.T) {
	tr := New()
	state := &StreamState{
		ResponseID: "resp_1",
		Model:      "client-model",
		Started:    true,
	}
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.ClientModelKey, "client-model")
	ctx = context.WithValue(ctx, openaiStreamStateKey, state)

	sseChunk := []byte(`event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}

`)

	result, err := tr.TransformStream(ctx, sseChunk)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	events := splitCodexEvents(t, result)
	if len(events) == 0 {
		t.Fatalf("期望事件，实际 0")
	}

	var hasReasoningItem bool
	for _, e := range events {
		if eventType(e) == "response.output_item.added" {
			var data map[string]any
			_ = json.Unmarshal(e, &data)
			if item, ok := data["item"].(map[string]any); ok {
				if item["type"] == "reasoning" {
					hasReasoningItem = true
				}
			}
		}
	}
	if !hasReasoningItem {
		t.Errorf("期望 reasoning output_item.added 事件，实际: %s", string(result))
	}
}

func TestAnthropic_Stream_ContentBlockDelta_Thinking(t *testing.T) {
	tr := New()
	state := &StreamState{
		ResponseID: "resp_1",
		Model:      "client-model",
		Started:    true,
		ReasoningItemIndex: 0,
	}
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.ClientModelKey, "client-model")
	ctx = context.WithValue(ctx, openaiStreamStateKey, state)

	sseChunk := []byte(`event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"deep thought"}}

`)

	result, err := tr.TransformStream(ctx, sseChunk)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	events := splitCodexEvents(t, result)
	var foundReasoningDelta bool
	for _, e := range events {
		if eventType(e) == "response.reasoning_summary_text.delta" {
			foundReasoningDelta = true
			var data map[string]any
			_ = json.Unmarshal(e, &data)
			if data["delta"] != "deep thought" {
				t.Errorf("期望 delta=deep thought，实际 %v", data["delta"])
			}
		}
	}
	if !foundReasoningDelta {
		t.Errorf("期望 response.reasoning_summary_text.delta 事件，实际: %s", string(result))
	}
}

func TestAnthropic_Stream_ContentBlockStart_ToolUse(t *testing.T) {
	tr := New()
	state := &StreamState{
		ResponseID: "resp_1",
		Model:      "client-model",
		Started:    true,
	}
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.ClientModelKey, "client-model")
	ctx = context.WithValue(ctx, openaiStreamStateKey, state)

	sseChunk := []byte(`event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{}}}

`)

	result, err := tr.TransformStream(ctx, sseChunk)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	events := splitCodexEvents(t, result)
	var foundToolItem bool
	for _, e := range events {
		if eventType(e) == "response.output_item.added" {
			var data map[string]any
			_ = json.Unmarshal(e, &data)
			if item, ok := data["item"].(map[string]any); ok {
				if item["type"] == "function_call" {
					foundToolItem = true
					if item["name"] != "get_weather" {
						t.Errorf("期望 name=get_weather，实际 %v", item["name"])
					}
				}
			}
		}
	}
	if !foundToolItem {
		t.Errorf("期望 function_call output_item.added 事件，实际: %s", string(result))
	}
}

func TestAnthropic_Stream_InputJSONDelta(t *testing.T) {
	tr := New()
	state := &StreamState{
		ResponseID: "resp_1",
		Model:      "client-model",
		Started:    true,
		FunctionCalls: []FunctionCall{
			{CallID: "toolu_1", Name: "get_weather", Index: 0, Started: true},
		},
		OutputIndex: 1,
	}
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.ClientModelKey, "client-model")
	ctx = context.WithValue(ctx, openaiStreamStateKey, state)

	sseChunk := []byte(`event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"city\":"}}

`)

	result, err := tr.TransformStream(ctx, sseChunk)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	events := splitCodexEvents(t, result)
	var foundArgDelta bool
	for _, e := range events {
		if eventType(e) == "response.function_call_arguments.delta" {
			foundArgDelta = true
			var data map[string]any
			_ = json.Unmarshal(e, &data)
			if data["delta"] != `{"city":` {
				t.Errorf("期望 delta={\"city\":，实际 %v", data["delta"])
			}
		}
	}
	if !foundArgDelta {
		t.Errorf("期望 function_call_arguments.delta 事件，实际: %s", string(result))
	}
}

func TestAnthropic_Stream_MessageDelta_StopReason(t *testing.T) {
	tr := New()
	state := &StreamState{
		ResponseID: "resp_1",
		Model:      "client-model",
		Started:    true,
		Finished:   true, // 假设流已结束
	}
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.ClientModelKey, "client-model")
	ctx = context.WithValue(ctx, openaiStreamStateKey, state)

	sseChunk := []byte(`event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":5}}

`)

	result, err := tr.TransformStream(ctx, sseChunk)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	events := splitCodexEvents(t, result)
	// 应包含 response.completed
	var foundCompleted bool
	for _, e := range events {
		if eventType(e) == "response.completed" {
			foundCompleted = true
		}
	}
	if !foundCompleted {
		t.Errorf("期望 response.completed 事件，实际: %s", string(result))
	}
}

// ====================
// Helpers
// ====================

func splitCodexEvents(t *testing.T, raw []byte) [][]byte {
	t.Helper()
	if len(raw) == 0 {
		return nil
	}
	// 格式1：单个 JSON 对象
	if raw[0] == '{' {
		return [][]byte{raw}
	}
	// 格式2：JSON 数组
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err == nil {
		out := make([][]byte, 0, len(arr))
		for _, m := range arr {
			out = append(out, []byte(m))
		}
		return out
	}
	// 格式3：按 \n\n 分隔的多 SSE 块
	var out [][]byte
	blocks := splitBlocks(raw)
	for _, b := range blocks {
		if len(b) == 0 {
			continue
		}
		out = append(out, b)
	}
	return out
}

func splitBlocks(data []byte) [][]byte {
	var out [][]byte
	start := 0
	for i := 0; i < len(data); i++ {
		if i+1 < len(data) && data[i] == '\n' && data[i+1] == '\n' {
			out = append(out, data[start:i])
			start = i + 2
			i++
		}
	}
	if start < len(data) {
		out = append(out, data[start:])
	}
	return out
}

func eventType(raw []byte) string {
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return ""
	}
	t, _ := data["type"].(string)
	return t
}
