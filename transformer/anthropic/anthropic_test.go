package anthropic

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"agr/transformer/tctx"
)

// codexStreamTransformerIface 是 transformer.CodexStreamTransformer 接口的本地副本，
// 仅用于反射检查 anthropic.Transformer 是否实现了该接口（避免测试时 import cycle）。
type codexStreamTransformerIface interface {
	TransformCodexStream(ctx context.Context, chunk []byte) ([][]byte, error)
}

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
	// Anthropic 格式：tool_use 在 content 数组中
	content, ok := assistant["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("期望 assistant.content 包含 1 个 tool_use，实际 %v", assistant)
	}
	tc := content[0].(map[string]any)
	if tc["type"] != "tool_use" {
		t.Errorf("期望 type=tool_use，实际 %v", tc["type"])
	}
	if tc["id"] != "call_1" {
		t.Errorf("期望 tool_use id=call_1，实际 %v", tc["id"])
	}
	if tc["name"] != "get_weather" {
		t.Errorf("期望 tool_use name=get_weather，实际 %v", tc["name"])
	}

	toolMsg := msgs[2].(map[string]any)
	if toolMsg["role"] != "user" {
		t.Errorf("期望第 3 条 role=user（tool_result 是 user content 数组），实际 %v", toolMsg["role"])
	}
	trContent, ok := toolMsg["content"].([]any)
	if !ok || len(trContent) != 1 {
		t.Fatalf("期望 toolMsg.content 包含 1 个 tool_result，实际 %v", toolMsg)
	}
	trPart := trContent[0].(map[string]any)
	if trPart["type"] != "tool_result" {
		t.Errorf("期望 type=tool_result，实际 %v", trPart["type"])
	}
	if trPart["tool_use_id"] != "call_1" {
		t.Errorf("期望 tool_use_id=call_1，实际 %v", trPart["tool_use_id"])
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
		ResponseID:         "resp_1",
		Model:              "client-model",
		Started:            true,
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

// ====================
// Additional Coverage Tests
// ====================

func TestAnthropic_CodexRequest_ToolChoiceString(t *testing.T) {
	tr := New()
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.UpstreamModelKey, "claude-sonnet-4-5")

	body := []byte(`{
		"model": "client-model",
		"input": "hi",
		"tool_choice": "required"
	}`)

	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	var out map[string]any
	_ = json.Unmarshal(result, &out)

	tc, ok := out["tool_choice"].(map[string]any)
	if !ok || tc["type"] != "any" {
		t.Errorf("期望 tool_choice.type=any，实际 %v", out["tool_choice"])
	}
}

func TestAnthropic_CodexRequest_ToolChoiceMap(t *testing.T) {
	tr := New()
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.UpstreamModelKey, "claude-sonnet-4-5")

	body := []byte(`{
		"model": "client-model",
		"input": "hi",
		"tool_choice": {"type": "function", "name": "get_weather"}
	}`)

	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	var out map[string]any
	_ = json.Unmarshal(result, &out)

	tc, ok := out["tool_choice"].(map[string]any)
	if !ok || tc["type"] != "tool" || tc["name"] != "get_weather" {
		t.Errorf("期望 tool_choice={type:tool,name:get_weather}，实际 %v", out["tool_choice"])
	}
}

func TestAnthropic_CodexRequest_ToolChoiceNone(t *testing.T) {
	tr := New()
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.UpstreamModelKey, "claude-sonnet-4-5")

	body := []byte(`{
		"model": "client-model",
		"input": "hi",
		"tool_choice": "none"
	}`)

	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	var out map[string]any
	_ = json.Unmarshal(result, &out)

	tc, _ := out["tool_choice"].(map[string]any)
	if tc["type"] != "none" {
		t.Errorf("期望 tool_choice.type=none，实际 %v", out["tool_choice"])
	}
}

func TestAnthropic_CodexRequest_ReasoningEffortLow(t *testing.T) {
	tr := New()
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.UpstreamModelKey, "claude-sonnet-4-5")

	body := []byte(`{
		"model": "client-model",
		"input": "hi",
		"reasoning": {"effort": "low"}
	}`)

	result, _ := tr.TransformRequest(ctx, body)
	var out map[string]any
	_ = json.Unmarshal(result, &out)

	thinking, _ := out["thinking"].(map[string]any)
	if v, _ := thinking["budget_tokens"].(float64); v != 1024 {
		t.Errorf("期望 budget_tokens=1024，实际 %v", thinking["budget_tokens"])
	}
}

func TestAnthropic_CodexRequest_ReasoningEffortNone(t *testing.T) {
	tr := New()
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.UpstreamModelKey, "claude-sonnet-4-5")

	body := []byte(`{
		"model": "client-model",
		"input": "hi",
		"reasoning": {"effort": "none"}
	}`)

	result, _ := tr.TransformRequest(ctx, body)
	var out map[string]any
	_ = json.Unmarshal(result, &out)

	if _, ok := out["thinking"]; ok {
		t.Errorf("effort=none 时不应有 thinking 字段，实际 %v", out["thinking"])
	}
}

func TestAnthropic_CodexRequest_ReasoningNoEffort(t *testing.T) {
	tr := New()
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.UpstreamModelKey, "claude-sonnet-4-5")

	body := []byte(`{
		"model": "client-model",
		"input": "hi",
		"reasoning": {}
	}`)

	result, _ := tr.TransformRequest(ctx, body)
	var out map[string]any
	_ = json.Unmarshal(result, &out)

	if _, ok := out["thinking"]; ok {
		t.Errorf("无 effort 时不应有 thinking 字段，实际 %v", out["thinking"])
	}
}

func TestAnthropic_CodexRequest_DeveloperRole(t *testing.T) {
	tr := New()
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.UpstreamModelKey, "claude-sonnet-4-5")

	body := []byte(`{
		"model": "client-model",
		"input": [
			{"type": "message", "role": "developer", "content": [{"type": "input_text", "text": "you are helpful"}]},
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "hi"}]}
		]
	}`)

	result, _ := tr.TransformRequest(ctx, body)
	var out map[string]any
	_ = json.Unmarshal(result, &out)

	msgs := out["messages"].([]any)
	if len(msgs) != 1 {
		t.Errorf("developer 消息应被忽略，期望 1 条消息，实际 %d", len(msgs))
	}
}

func TestAnthropic_CodexRequest_ReasoningHistory(t *testing.T) {
	tr := New()
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.UpstreamModelKey, "claude-sonnet-4-5")

	body := []byte(`{
		"model": "client-model",
		"input": [
			{"type": "reasoning", "summary": [{"type": "summary_text", "text": "old thinking"}]},
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "hi"}]}
		]
	}`)

	result, _ := tr.TransformRequest(ctx, body)
	var out map[string]any
	_ = json.Unmarshal(result, &out)

	msgs := out["messages"].([]any)
	if len(msgs) != 1 {
		t.Errorf("reasoning 应被忽略，期望 1 条消息，实际 %d", len(msgs))
	}
}

func TestAnthropic_CodexRequest_ExtraParameters(t *testing.T) {
	tr := New()
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.UpstreamModelKey, "claude-sonnet-4-5")

	body := []byte(`{
		"model": "client-model",
		"input": "hi",
		"top_p": 0.9,
		"stop": ["END"],
		"metadata": {"user_id": "u_1"}
	}`)

	result, _ := tr.TransformRequest(ctx, body)
	var out map[string]any
	_ = json.Unmarshal(result, &out)

	if out["top_p"] != 0.9 {
		t.Errorf("top_p 未透传")
	}
	if _, ok := out["stop_sequences"]; !ok {
		t.Errorf("stop_sequences 未设置")
	}
	if _, ok := out["metadata"]; !ok {
		t.Errorf("metadata 未透传")
	}
}

func TestAnthropic_CodexRequest_TopPFloat(t *testing.T) {
	tr := New()
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.UpstreamModelKey, "claude-sonnet-4-5")

	body := []byte(`{
		"model": "client-model",
		"input": "hi",
		"top_p": 0.5
	}`)

	result, _ := tr.TransformRequest(ctx, body)
	var out map[string]any
	_ = json.Unmarshal(result, &out)

	if out["top_p"] != 0.5 {
		t.Errorf("top_p 未透传: %v", out["top_p"])
	}
}

func TestAnthropic_CodexRequest_StreamFalse(t *testing.T) {
	tr := New()
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.UpstreamModelKey, "claude-sonnet-4-5")

	body := []byte(`{
		"model": "client-model",
		"input": "hi",
		"stream": false
	}`)

	result, _ := tr.TransformRequest(ctx, body)
	var out map[string]any
	_ = json.Unmarshal(result, &out)

	if out["stream"] != false {
		t.Errorf("stream=false 应透传: %v", out["stream"])
	}
}

func TestAnthropic_CodexRequest_ToolNoName(t *testing.T) {
	tr := New()
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.UpstreamModelKey, "claude-sonnet-4-5")

	body := []byte(`{
		"model": "client-model",
		"input": "hi",
		"tools": [{"type": "function"}]
	}`)

	result, _ := tr.TransformRequest(ctx, body)
	var out map[string]any
	_ = json.Unmarshal(result, &out)

	if v, has := out["tools"]; has {
		t.Errorf("无 name 的 tool 应被过滤，实际: %v", v)
	}
}

func TestAnthropic_CodexRequest_NonFunctionTools(t *testing.T) {
	tr := New()
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.UpstreamModelKey, "claude-sonnet-4-5")

	body := []byte(`{
		"model": "client-model",
		"input": "hi",
		"tools": [
			{"type": "function", "name": "get_weather", "description": "weather", "parameters": {}},
			{"type": "web_search"},
			{"type": "image_generation"}
		]
	}`)

	result, _ := tr.TransformRequest(ctx, body)
	var out map[string]any
	_ = json.Unmarshal(result, &out)

	tools, _ := out["tools"].([]any)
	if len(tools) != 1 {
		t.Errorf("只有 function 工具应被保留，期望 1，实际 %d", len(tools))
	}
}

func TestAnthropic_CodexRequest_InputAsString(t *testing.T) {
	tr := New()
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.UpstreamModelKey, "claude-sonnet-4-5")

	body := []byte(`{
		"model": "client-model",
		"input": "just a string"
	}`)

	result, _ := tr.TransformRequest(ctx, body)
	var out map[string]any
	_ = json.Unmarshal(result, &out)

	msgs := out["messages"].([]any)
	if len(msgs) != 1 || msgs[0].(map[string]any)["content"] != "just a string" {
		t.Errorf("字符串 input 应转为 user 消息: %v", msgs)
	}
}

func TestAnthropic_CodexRequest_InputAsOther(t *testing.T) {
	tr := New()
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.UpstreamModelKey, "claude-sonnet-4-5")

	body := []byte(`{
		"model": "client-model",
		"input": 42
	}`)

	result, _ := tr.TransformRequest(ctx, body)
	var out map[string]any
	_ = json.Unmarshal(result, &out)

	msgs := out["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("期望 1 条消息，实际 %d", len(msgs))
	}
}

func TestAnthropic_CodexRequest_PlainMessagePassthrough(t *testing.T) {
	tr := New()
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.UpstreamModelKey, "claude-sonnet-4-5")

	body := []byte(`{
		"model": "client-model",
		"input": [
			{"role": "user", "content": "hi"}
		]
	}`)

	result, _ := tr.TransformRequest(ctx, body)
	var out map[string]any
	_ = json.Unmarshal(result, &out)

	msgs := out["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("期望 1 条消息，实际 %d", len(msgs))
	}
}

func TestAnthropic_CodexRequest_StringContentFallback(t *testing.T) {
	tr := New()
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.UpstreamModelKey, "claude-sonnet-4-5")

	body := []byte(`{
		"model": "client-model",
		"input": [
			{"type": "message", "role": "user", "content": 42}
		]
	}`)

	result, _ := tr.TransformRequest(ctx, body)
	var out map[string]any
	_ = json.Unmarshal(result, &out)

	msgs := out["messages"].([]any)
	if msgs[0].(map[string]any)["content"] != "42" {
		t.Errorf("非字符串 content 应转字符串: %v", msgs[0])
	}
}

func TestAnthropic_CodexRequest_NilContent(t *testing.T) {
	tr := New()
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.UpstreamModelKey, "claude-sonnet-4-5")

	body := []byte(`{
		"model": "client-model",
		"input": [
			{"type": "message", "role": "assistant"}
		]
	}`)

	result, _ := tr.TransformRequest(ctx, body)
	var out map[string]any
	_ = json.Unmarshal(result, &out)

	msgs := out["messages"].([]any)
	if msgs[0].(map[string]any)["content"] != "" {
		t.Errorf("nil content 应为空字符串: %v", msgs[0])
	}
}

func TestAnthropic_Response_StopReasonEndTurn(t *testing.T) {
	tr := New()
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.ClientModelKey, "client-model")

	body := []byte(`{
		"id": "msg_1",
		"role": "assistant",
		"content": [{"type": "text", "text": "hi"}],
		"stop_reason": "end_turn",
		"usage": {"input_tokens": 5, "output_tokens": 3}
	}`)

	result, _ := tr.TransformResponse(ctx, body)
	var out map[string]any
	_ = json.Unmarshal(result, &out)

	if out["status"] != "completed" {
		t.Errorf("end_turn 应映射为 completed，实际 %v", out["status"])
	}
}

func TestAnthropic_Response_StopReasonMaxTokens(t *testing.T) {
	tr := New()
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.ClientModelKey, "client-model")

	body := []byte(`{
		"id": "msg_1",
		"role": "assistant",
		"content": [{"type": "text", "text": "hi"}],
		"stop_reason": "max_tokens",
		"usage": {"input_tokens": 5, "output_tokens": 3}
	}`)

	result, _ := tr.TransformResponse(ctx, body)
	var out map[string]any
	_ = json.Unmarshal(result, &out)

	if out["status"] != "incomplete" {
		t.Errorf("max_tokens 应映射为 incomplete，实际 %v", out["status"])
	}
}

func TestAnthropic_Response_NoUsage(t *testing.T) {
	tr := New()
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.ClientModelKey, "client-model")

	body := []byte(`{
		"id": "msg_1",
		"role": "assistant",
		"content": [{"type": "text", "text": "hi"}],
		"stop_reason": "end_turn"
	}`)

	result, _ := tr.TransformResponse(ctx, body)
	var out map[string]any
	_ = json.Unmarshal(result, &out)

	// 无 usage 时不应有 usage 字段
	if _, hasUsage := out["usage"]; hasUsage {
		t.Errorf("无 usage 时不应有 usage 字段")
	}
}

func TestAnthropic_Response_NonArrayContent(t *testing.T) {
	tr := New()
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.ClientModelKey, "client-model")

	body := []byte(`{
		"id": "msg_1",
		"role": "assistant",
		"content": "not an array",
		"stop_reason": "end_turn"
	}`)

	result, _ := tr.TransformResponse(ctx, body)
	var out map[string]any
	_ = json.Unmarshal(result, &out)

	// content 不是数组时 output 应为空
	output, _ := out["output"].([]any)
	if len(output) != 0 {
		t.Errorf("非数组 content 应输出空数组，实际: %v", output)
	}
}

func TestAnthropic_Response_NoID(t *testing.T) {
	tr := New()
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.ClientModelKey, "client-model")

	body := []byte(`{
		"role": "assistant",
		"content": [{"type": "text", "text": "hi"}],
		"stop_reason": "end_turn"
	}`)

	result, _ := tr.TransformResponse(ctx, body)
	var out map[string]any
	_ = json.Unmarshal(result, &out)

	id, ok := out["id"].(string)
	if !ok || !strings.HasPrefix(id, "resp_") {
		t.Errorf("无 id 时应生成 resp_ 前缀 ID，实际: %v", out["id"])
	}
}

func TestAnthropic_Response_ThinkingBlock(t *testing.T) {
	tr := New()
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.ClientModelKey, "client-model")

	body := []byte(`{
		"id": "msg_1",
		"role": "assistant",
		"content": [
			{"type": "thinking", "thinking": "deep thought"},
			{"type": "text", "text": "answer"}
		],
		"stop_reason": "end_turn",
		"usage": {"input_tokens": 5, "output_tokens": 3}
	}`)

	result, _ := tr.TransformResponse(ctx, body)
	var out map[string]any
	_ = json.Unmarshal(result, &out)

	// 应有 text message（thinking 在响应阶段被忽略）
	output, _ := out["output"].([]any)
	if len(output) != 1 {
		t.Errorf("thinking 应被忽略，期望 1 个 message，实际 %d", len(output))
	}
	msg := output[0].(map[string]any)
	if msg["type"] != "message" {
		t.Errorf("期望 type=message，实际 %v", msg["type"])
	}
}

func TestAnthropic_Stream_MessageStop(t *testing.T) {
	tr := New()
	state := &StreamState{
		ResponseID:       "resp_1",
		Model:            "client-model",
		Started:          true,
		MessageStarted:   true,
		AccumulatedText:  "hi",
		MessageItemIndex: 0,
	}
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.ClientModelKey, "client-model")
	ctx = context.WithValue(ctx, openaiStreamStateKey, state)

	sseChunk := []byte(`event: message_stop
data: {"type":"message_stop"}

`)

	result, err := tr.TransformStream(ctx, sseChunk)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	events := splitCodexEvents(t, result)
	if len(events) == 0 {
		t.Fatalf("期望事件")
	}
	var foundCompleted bool
	for _, e := range events {
		if eventType(e) == "response.completed" {
			foundCompleted = true
		}
	}
	if !foundCompleted {
		t.Errorf("message_stop 应发出 response.completed")
	}
}

func TestAnthropic_Stream_ContentBlockStop_Thinking(t *testing.T) {
	tr := New()
	state := &StreamState{
		ResponseID:           "resp_1",
		Model:                "client-model",
		Started:              true,
		ReasoningStarted:     true,
		ReasoningItemIndex:   0,
		AccumulatedReasoning: "thought",
		Seq:                  0,
	}
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.ClientModelKey, "client-model")
	ctx = context.WithValue(ctx, openaiStreamStateKey, state)

	sseChunk := []byte(`event: content_block_stop
data: {"type":"content_block_stop","index":0}

`)

	result, err := tr.TransformStream(ctx, sseChunk)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	events := splitCodexEvents(t, result)
	if len(events) == 0 {
		t.Fatalf("期望事件")
	}
	var hasReasoningDone bool
	for _, e := range events {
		if eventType(e) == "response.reasoning_summary_text.done" {
			hasReasoningDone = true
		}
	}
	if !hasReasoningDone {
		t.Errorf("thinking 关闭应发出 reasoning_summary_text.done")
	}
}

func TestAnthropic_Stream_ContentBlockStop_FunctionCall(t *testing.T) {
	tr := New()
	state := &StreamState{
		ResponseID: "resp_1",
		Model:      "client-model",
		Started:    true,
		FunctionCalls: []FunctionCall{
			{CallID: "toolu_1", Name: "get_weather", Index: 0, ItemIndex: 1, Started: true, Arguments: "{\"city\":\"SF\"}"},
		},
		Seq: 0,
	}
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.ClientModelKey, "client-model")
	ctx = context.WithValue(ctx, openaiStreamStateKey, state)

	sseChunk := []byte(`event: content_block_stop
data: {"type":"content_block_stop","index":0}

`)

	result, err := tr.TransformStream(ctx, sseChunk)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	events := splitCodexEvents(t, result)
	var hasArgsDone bool
	for _, e := range events {
		if eventType(e) == "response.function_call_arguments.done" {
			hasArgsDone = true
		}
	}
	if !hasArgsDone {
		t.Errorf("function_call 关闭应发出 function_call_arguments.done")
	}
}

func TestAnthropic_Stream_ContentBlockDelta_EmptyText(t *testing.T) {
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
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":""}}

`)

	result, err := tr.TransformStream(ctx, sseChunk)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	if len(result) != 0 {
		t.Errorf("空文本应不产生事件，实际: %s", string(result))
	}
}

func TestAnthropic_Stream_ContentBlockDelta_EmptyThinking(t *testing.T) {
	tr := New()
	state := &StreamState{
		ResponseID:         "resp_1",
		Model:              "client-model",
		Started:            true,
		ReasoningStarted:   true,
		ReasoningItemIndex: 0,
	}
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.ClientModelKey, "client-model")
	ctx = context.WithValue(ctx, openaiStreamStateKey, state)

	sseChunk := []byte(`event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":""}}

`)

	result, err := tr.TransformStream(ctx, sseChunk)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	if len(result) != 0 {
		t.Errorf("空 thinking 应不产生事件，实际: %s", string(result))
	}
}

func TestAnthropic_Stream_ContentBlockDelta_EmptyPartialJSON(t *testing.T) {
	tr := New()
	state := &StreamState{
		ResponseID: "resp_1",
		Model:      "client-model",
		Started:    true,
		FunctionCalls: []FunctionCall{
			{CallID: "toolu_1", Name: "get_weather", Index: 0, ItemIndex: 1, Started: true},
		},
	}
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.ClientModelKey, "client-model")
	ctx = context.WithValue(ctx, openaiStreamStateKey, state)

	sseChunk := []byte(`event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":""}}

`)

	result, err := tr.TransformStream(ctx, sseChunk)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	if len(result) != 0 {
		t.Errorf("空 partial_json 应不产生事件")
	}
}

func TestAnthropic_Stream_Ping(t *testing.T) {
	tr := New()
	state := &StreamState{ResponseID: "resp_1", Model: "client-model"}
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.ClientModelKey, "client-model")
	ctx = context.WithValue(ctx, openaiStreamStateKey, state)

	sseChunk := []byte(`event: ping
data: {"type":"ping"}

`)

	result, err := tr.TransformStream(ctx, sseChunk)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	if len(result) != 0 {
		t.Errorf("ping 应不产生事件")
	}
}

func TestAnthropic_Stream_Error(t *testing.T) {
	tr := New()
	state := &StreamState{ResponseID: "resp_1", Model: "client-model"}
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.ClientModelKey, "client-model")
	ctx = context.WithValue(ctx, openaiStreamStateKey, state)

	sseChunk := []byte(`event: error
data: {"type":"error","error":{"message":"bad"}}

`)

	_, err := tr.TransformStream(ctx, sseChunk)
	if err == nil {
		t.Errorf("error 事件应返回错误")
	}
}

func TestAnthropic_Stream_UnknownEvent(t *testing.T) {
	tr := New()
	state := &StreamState{ResponseID: "resp_1", Model: "client-model"}
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.ClientModelKey, "client-model")
	ctx = context.WithValue(ctx, openaiStreamStateKey, state)

	sseChunk := []byte(`event: unknown_thing
data: {"foo":"bar"}

`)

	result, err := tr.TransformStream(ctx, sseChunk)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	if len(result) != 0 {
		t.Errorf("未知事件应不产生事件")
	}
}

func TestAnthropic_Stream_NoState(t *testing.T) {
	tr := New()
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	// 不设置 stream state

	sseChunk := []byte(`event: message_start
data: {"type":"message_start"}

`)

	result, err := tr.TransformStream(ctx, sseChunk)
	if err != nil {
		t.Fatalf("无 state 应不报错: %v", err)
	}

	if len(result) != 0 {
		t.Errorf("无 state 时应跳过")
	}
}

func TestAnthropic_Stream_EmptyChunk(t *testing.T) {
	tr := New()
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")

	result, err := tr.TransformStream(ctx, []byte(""))
	if err != nil {
		t.Fatalf("空 chunk 应不报错: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("空 chunk 应返回 nil")
	}
}

func TestAnthropic_Stream_MalformedJSON(t *testing.T) {
	tr := New()
	state := &StreamState{ResponseID: "resp_1", Model: "client-model"}
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.ClientModelKey, "client-model")
	ctx = context.WithValue(ctx, openaiStreamStateKey, state)

	sseChunk := []byte(`event: message_start
data: not valid json

`)

	result, err := tr.TransformStream(ctx, sseChunk)
	if err != nil {
		t.Fatalf("畸形 JSON 不应报错: %v", err)
	}

	if len(result) != 0 {
		t.Errorf("畸形 JSON 应跳过")
	}
}

func TestAnthropic_Stream_MultipleFramesInChunk(t *testing.T) {
	tr := New()
	state := &StreamState{
		ResponseID:         "resp_1",
		Model:              "client-model",
		Started:            true,
		ReasoningItemIndex: 0,
		ReasoningStarted:   true,
	}
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.ClientModelKey, "client-model")
	ctx = context.WithValue(ctx, openaiStreamStateKey, state)

	// 一个 chunk 中包含多个 SSE 帧
	sseChunk := []byte(`event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hi"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":" world"}}

`)

	result, err := tr.TransformStream(ctx, sseChunk)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	events := splitCodexEvents(t, result)
	if len(events) < 2 {
		t.Errorf("应至少产生 2 个事件，实际 %d", len(events))
	}
}

func TestAnthropic_Stream_MessageStartAlreadyStarted(t *testing.T) {
	tr := New()
	state := &StreamState{
		ResponseID: "resp_1",
		Model:      "client-model",
		Started:    true, // 已启动
	}
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.ClientModelKey, "client-model")
	ctx = context.WithValue(ctx, openaiStreamStateKey, state)

	sseChunk := []byte(`event: message_start
data: {"type":"message_start","message":{"id":"msg_1","role":"assistant","content":[],"model":"claude","usage":{"input_tokens":5}}}

`)

	result, err := tr.TransformStream(ctx, sseChunk)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	if len(result) != 0 {
		t.Errorf("重复 message_start 应不产生事件")
	}
}

func TestAnthropic_Stream_ContentBlockStart_UnknownType(t *testing.T) {
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
data: {"type":"content_block_start","index":0,"content_block":{"type":"unknown"}}

`)

	result, err := tr.TransformStream(ctx, sseChunk)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	if len(result) != 0 {
		t.Errorf("未知 block type 应不产生事件")
	}
}

func TestAnthropic_Stream_ContentBlockDelta_UnknownDeltaType(t *testing.T) {
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
data: {"type":"content_block_delta","index":0,"delta":{"type":"unknown"}}

`)

	result, err := tr.TransformStream(ctx, sseChunk)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	if len(result) != 0 {
		t.Errorf("未知 delta type 应不产生事件")
	}
}

func TestAnthropic_Stream_InputJSONDelta_NoFunctionCall(t *testing.T) {
	tr := New()
	state := &StreamState{
		ResponseID: "resp_1",
		Model:      "client-model",
		Started:    true,
		// 无 function calls
	}
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.ClientModelKey, "client-model")
	ctx = context.WithValue(ctx, openaiStreamStateKey, state)

	sseChunk := []byte(`event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"x\":1}"}}

`)

	result, err := tr.TransformStream(ctx, sseChunk)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	if len(result) != 0 {
		t.Errorf("无 function call 时应跳过")
	}
}

// ====================
// Bug-fix Regression Tests
// ====================

// TestAnthropic_CodexStreamTransformer_Interface 验证 anthropic.Transformer
// 实现了 transformer.CodexStreamTransformer 接口，
// 这样 chain.TransformCodexStream 才能调用到它而不是回退到 raw passthrough。
func TestAnthropic_CodexStreamTransformer_Interface(t *testing.T) {
	tr := New()

	// 通过反射检查是否实现了目标方法
	iface := reflect.TypeOf((*codexStreamTransformerIface)(nil)).Elem()
	if !reflect.TypeOf(tr).Implements(iface) {
		t.Fatalf("anthropic.Transformer 必须实现 CodexStreamTransformer 接口")
	}
}

// TestAnthropic_CodexRequest_DeveloperRoleMapsToSystem 验证 Codex 的
// role=developer 消息应被合并到 Anthropic 的 system 字段，而不是被丢弃。
func TestAnthropic_CodexRequest_DeveloperRoleMapsToSystem(t *testing.T) {
	tr := New()
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.UpstreamModelKey, "claude-sonnet-4-5")

	body := []byte(`{
		"model": "client-model",
		"input": [
			{"type": "message", "role": "developer", "content": [
				{"type": "input_text", "text": "you are a helpful assistant"}
			]},
			{"type": "message", "role": "user", "content": [
				{"type": "input_text", "text": "hi"}
			]}
		]
	}`)

	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	var out map[string]any
	_ = json.Unmarshal(result, &out)

	// system 字段应包含 developer 内容
	system, _ := out["system"].(string)
	if !strings.Contains(system, "you are a helpful assistant") {
		t.Errorf("system 字段应包含 developer 内容，实际: %q", system)
	}

	// messages 应只剩 user 消息
	msgs, _ := out["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("期望 1 条 user 消息，实际 %d 条", len(msgs))
	}
	if msgs[0].(map[string]any)["role"] != "user" {
		t.Errorf("期望 user 消息，实际: %v", msgs[0])
	}
}

// TestAnthropic_CodexRequest_DeveloperAndInstructionsBothGoToSystem 验证
// instructions 与 developer 消息都被合并到 system 字段。
func TestAnthropic_CodexRequest_DeveloperAndInstructionsBothGoToSystem(t *testing.T) {
	tr := New()
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.UpstreamModelKey, "claude-sonnet-4-5")

	body := []byte(`{
		"model": "client-model",
		"instructions": "be concise",
		"input": [
			{"type": "message", "role": "developer", "content": [
				{"type": "input_text", "text": "always be polite"}
			]},
			{"type": "message", "role": "user", "content": [
				{"type": "input_text", "text": "hi"}
			]}
		]
	}`)

	result, _ := tr.TransformRequest(ctx, body)
	var out map[string]any
	_ = json.Unmarshal(result, &out)

	system, _ := out["system"].(string)
	if !strings.Contains(system, "be concise") {
		t.Errorf("system 应包含 instructions，实际: %q", system)
	}
	if !strings.Contains(system, "always be polite") {
		t.Errorf("system 应包含 developer 内容，实际: %q", system)
	}
}

// TestAnthropic_CodexRequest_MultipleDeveloperMessagesMerged 验证
// 多个 developer 消息都被合并到 system。
func TestAnthropic_CodexRequest_MultipleDeveloperMessagesMerged(t *testing.T) {
	tr := New()
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.UpstreamModelKey, "claude-sonnet-4-5")

	body := []byte(`{
		"model": "client-model",
		"input": [
			{"type": "message", "role": "developer", "content": [
				{"type": "input_text", "text": "first developer block"}
			]},
			{"type": "message", "role": "user", "content": [
				{"type": "input_text", "text": "ask 1"}
			]},
			{"type": "message", "role": "developer", "content": [
				{"type": "input_text", "text": "second developer block"}
			]},
			{"type": "message", "role": "user", "content": [
				{"type": "input_text", "text": "ask 2"}
			]}
		]
	}`)

	result, _ := tr.TransformRequest(ctx, body)
	var out map[string]any
	_ = json.Unmarshal(result, &out)

	system, _ := out["system"].(string)
	if !strings.Contains(system, "first developer block") {
		t.Errorf("system 应包含 first developer block，实际: %q", system)
	}
	if !strings.Contains(system, "second developer block") {
		t.Errorf("system 应包含 second developer block，实际: %q", system)
	}

	msgs, _ := out["messages"].([]any)
	if len(msgs) != 2 {
		t.Errorf("期望 2 条 user 消息，实际 %d 条", len(msgs))
	}
}

// TestAnthropic_TransformCodexStream_ConvertsAnthropicEventToCodex 验证
// 当调用 TransformCodexStream 时，Anthropic SSE 事件被转换为 Codex 事件。
func TestAnthropic_TransformCodexStream_ConvertsAnthropicEventToCodex(t *testing.T) {
	tr := New()
	state := &StreamState{
		ResponseID: "resp_test",
		Model:      "client-model",
		Started:    true,
	}
	ctx := context.WithValue(context.Background(), tctx.RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, tctx.ClientModelKey, "client-model")
	ctx = context.WithValue(ctx, openaiStreamStateKey, state)

	// 单个 Anthropic text_delta 事件
	chunk := []byte(`event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"hello"}}

`)

	events, err := tr.TransformCodexStream(ctx, chunk)
	if err != nil {
		t.Fatalf("TransformCodexStream 应成功: %v", err)
	}

	if len(events) == 0 {
		t.Fatalf("期望至少 1 个 Codex 事件，实际 0")
	}

	// 至少有一个事件是 response.output_text.delta
	var foundTextDelta bool
	for _, e := range events {
		if eventType(e) == "response.output_text.delta" {
			foundTextDelta = true
		}
	}
	if !foundTextDelta {
		t.Errorf("应输出 response.output_text.delta，实际: %s", string(events[0]))
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
