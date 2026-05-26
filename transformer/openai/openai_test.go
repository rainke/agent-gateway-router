package openai

import (
	"context"
	"encoding/json"
	"testing"
)

func makeCtx(path, upstreamModel, clientModel string) context.Context {
	ctx := context.Background()
	ctx = context.WithValue(ctx, RequestPathKey, path)
	ctx = context.WithValue(ctx, UpstreamModelKey, upstreamModel)
	ctx = context.WithValue(ctx, ClientModelKey, clientModel)
	ctx = context.WithValue(ctx, RequestMetadataKey, &RequestMetadata{})
	return ctx
}

// === 请求转换测试 ===

func TestTransformClaudeRequest_Basic(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/messages", "real-model", "client-model")

	body := []byte(`{
		"model": "claude-3",
		"messages": [{"role": "user", "content": "hello"}],
		"max_tokens": 100,
		"stream": true,
		"temperature": 0.7
	}`)

	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("TransformRequest 失败: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(result, &parsed)

	if parsed["model"] != "real-model" {
		t.Errorf("模型应被替换为 real-model，实际 %v", parsed["model"])
	}
	if parsed["stream"] != true {
		t.Errorf("stream 应为 true")
	}
}

func TestTransformClaudeRequest_ReasoningMarksMetadata(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/messages", "real-model", "client-model")

	body := []byte(`{
		"model": "claude-3",
		"messages": [
			{"role": "assistant", "content": [
				{"type": "thinking", "thinking": "I should inspect the code."},
				{"type": "text", "text": "Done"}
			]}
		]
	}`)

	if _, err := tr.TransformRequest(ctx, body); err != nil {
		t.Fatalf("TransformRequest 失败: %v", err)
	}

	metadata, _ := ctx.Value(RequestMetadataKey).(*RequestMetadata)
	// x-reasoning-included 只针对 /v1/responses 接口，/v1/messages 不应标记
	if metadata != nil && metadata.ReasoningIncluded {
		t.Fatal("reasoning 内容不应在 /v1/messages 中标记为已包含")
	}
}

func TestTransformClaudeRequest_ContentArray(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/messages", "real-model", "")

	body := []byte(`{
		"model": "claude-3",
		"messages": [{"role": "user", "content": [{"type": "text", "text": "hello world"}]}],
		"max_tokens": 100
	}`)

	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("TransformRequest 失败: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(result, &parsed)

	msgs := parsed["messages"].([]any)
	msg := msgs[0].(map[string]any)
	if msg["content"] != "hello world" {
		t.Errorf("content 数组应被提取为文本，实际 %v", msg["content"])
	}
}

func TestTransformClaudeRequest_SystemMessage(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/messages", "real-model", "")

	body := []byte(`{
		"model": "claude-3",
		"system": "You are a helpful assistant.",
		"messages": [{"role": "user", "content": "hi"}],
		"max_tokens": 100
	}`)

	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("TransformRequest 失败: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(result, &parsed)

	msgs := parsed["messages"].([]any)
	if len(msgs) < 2 {
		t.Fatalf("应有至少 2 条消息（system + user），实际 %d", len(msgs))
	}
	firstMsg := msgs[0].(map[string]any)
	if firstMsg["role"] != "system" {
		t.Errorf("第一条消息应为 system，实际 %v", firstMsg["role"])
	}
}

func TestTransformClaudeRequest_SystemArray(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/messages", "real-model", "")

	body := []byte(`{
		"model": "claude-3",
		"system": [{"type": "text", "text": "line1"}, {"type": "text", "text": "line2"}],
		"messages": [{"role": "user", "content": "hi"}],
		"max_tokens": 100
	}`)

	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("TransformRequest 失败: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(result, &parsed)

	msgs := parsed["messages"].([]any)
	firstMsg := msgs[0].(map[string]any)
	if firstMsg["role"] != "system" {
		t.Errorf("第一条消息应为 system")
	}
	content := firstMsg["content"].(string)
	if content != "line1\nline2" {
		t.Errorf("system 数组应合并为换行分隔，实际 %q", content)
	}
}

func TestTransformClaudeRequest_WithTools(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/messages", "real-model", "")

	body := []byte(`{
		"model": "claude-3",
		"messages": [{"role": "user", "content": "run ls"}],
		"max_tokens": 100,
		"tools": [
			{
				"name": "bash",
				"description": "Execute bash command",
				"input_schema": {
					"type": "object",
					"properties": {"command": {"type": "string"}},
					"required": ["command"]
				}
			}
		]
	}`)

	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("TransformRequest 失败: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(result, &parsed)

	tools := parsed["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools 数量期望 1，实际 %d", len(tools))
	}
	tool := tools[0].(map[string]any)
	if tool["type"] != "function" {
		t.Errorf("tool type 应为 function，实际 %v", tool["type"])
	}
	fn := tool["function"].(map[string]any)
	if fn["name"] != "bash" {
		t.Errorf("function name 应为 bash，实际 %v", fn["name"])
	}
}

func TestTransformClaudeRequest_ToolChoice(t *testing.T) {
	tr := &Transformer{}

	tests := []struct {
		name     string
		input    string
		expected any
	}{
		{"auto", `{"type":"auto"}`, "auto"},
		{"any", `{"type":"any"}`, "required"},
		{"specific tool", `{"type":"tool","name":"bash"}`, nil}, // map 类型
		{"string", `"auto"`, "auto"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var toolChoice any
			json.Unmarshal([]byte(tt.input), &toolChoice)
			result := tr.ConvertClaudeToolChoice(toolChoice)
			if tt.expected != nil {
				if result != tt.expected {
					t.Errorf("期望 %v，实际 %v", tt.expected, result)
				}
			} else {
				// map 类型检查
				if _, ok := result.(map[string]any); !ok {
					t.Errorf("期望 map 类型，实际 %T", result)
				}
			}
		})
	}
}

func TestTransformClaudeRequest_AssistantWithToolUse(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/messages", "real-model", "")

	body := []byte(`{
		"model": "claude-3",
		"messages": [
			{"role": "user", "content": "run ls"},
			{"role": "assistant", "content": [
				{"type": "text", "text": "I will run ls"},
				{"type": "tool_use", "id": "tool_1", "name": "bash", "input": {"command": "ls"}}
			]},
			{"role": "user", "content": [
				{"type": "tool_result", "tool_use_id": "tool_1", "content": "file1.txt\nfile2.txt"}
			]}
		],
		"max_tokens": 100
	}`)

	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("TransformRequest 失败: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(result, &parsed)

	msgs := parsed["messages"].([]any)
	if len(msgs) < 3 {
		t.Fatalf("消息数量应至少 3，实际 %d", len(msgs))
	}

	// 检查 assistant 消息包含 tool_calls
	assistantMsg := msgs[1].(map[string]any)
	if assistantMsg["role"] != "assistant" {
		t.Errorf("第二条消息应为 assistant")
	}
	toolCalls, ok := assistantMsg["tool_calls"].([]map[string]any)
	if !ok || len(toolCalls) == 0 {
		// 可能是 []any 类型
		toolCallsAny, ok := assistantMsg["tool_calls"].([]any)
		if !ok || len(toolCallsAny) == 0 {
			t.Fatal("assistant 消息应包含 tool_calls")
		}
	}

	// 检查 tool result 消息
	toolMsg := msgs[2].(map[string]any)
	if toolMsg["role"] != "tool" {
		t.Errorf("第三条消息应为 tool，实际 %v", toolMsg["role"])
	}
	if toolMsg["tool_call_id"] != "tool_1" {
		t.Errorf("tool_call_id 应为 tool_1")
	}
}

func TestTransformClaudeRequest_ToolResultContentArray(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/messages", "real-model", "")

	body := []byte(`{
		"model": "claude-3",
		"messages": [
			{"role": "user", "content": [
				{"type": "tool_result", "tool_use_id": "t1", "content": [{"type": "text", "text": "result text"}]}
			]}
		],
		"max_tokens": 100
	}`)

	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("TransformRequest 失败: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(result, &parsed)

	msgs := parsed["messages"].([]any)
	if len(msgs) == 0 {
		t.Fatal("消息列表为空")
	}
}

// === 响应转换测试 ===

func TestTransformToClaudeResponse_TextOnly(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/messages", "", "claude-3")

	openaiResp := `{
		"id": "chatcmpl-123",
		"choices": [{"message": {"role": "assistant", "content": "Hello!"}, "finish_reason": "stop"}],
		"usage": {"prompt_tokens": 10, "completion_tokens": 5}
	}`

	result, err := tr.TransformResponse(ctx, []byte(openaiResp))
	if err != nil {
		t.Fatalf("TransformResponse 失败: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(result, &parsed)

	if parsed["type"] != "message" {
		t.Errorf("type 应为 message，实际 %v", parsed["type"])
	}
	if parsed["role"] != "assistant" {
		t.Errorf("role 应为 assistant")
	}
	if parsed["model"] != "claude-3" {
		t.Errorf("model 应为 claude-3，实际 %v", parsed["model"])
	}
	if parsed["stop_reason"] != "end_turn" {
		t.Errorf("stop_reason 应为 end_turn，实际 %v", parsed["stop_reason"])
	}

	content := parsed["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content 应有 1 个 block，实际 %d", len(content))
	}
	block := content[0].(map[string]any)
	if block["type"] != "text" {
		t.Errorf("block type 应为 text")
	}
	if block["text"] != "Hello!" {
		t.Errorf("text 应为 Hello!，实际 %v", block["text"])
	}

	usage := parsed["usage"].(map[string]any)
	if usage["input_tokens"].(float64) != 10 {
		t.Errorf("input_tokens 应为 10")
	}
}

func TestTransformToClaudeResponse_WithToolCalls(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/messages", "", "claude-3")

	openaiResp := `{
		"id": "chatcmpl-123",
		"choices": [{
			"message": {
				"role": "assistant",
				"content": null,
				"tool_calls": [{
					"id": "call_abc",
					"type": "function",
					"function": {"name": "bash", "arguments": "{\"command\":\"ls\"}"}
				}]
			},
			"finish_reason": "tool_calls"
		}],
		"usage": {"prompt_tokens": 10, "completion_tokens": 15}
	}`

	result, err := tr.TransformResponse(ctx, []byte(openaiResp))
	if err != nil {
		t.Fatalf("TransformResponse 失败: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(result, &parsed)

	if parsed["stop_reason"] != "tool_use" {
		t.Errorf("stop_reason 应为 tool_use，实际 %v", parsed["stop_reason"])
	}

	content := parsed["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content 应有 1 个 tool_use block，实际 %d", len(content))
	}
	block := content[0].(map[string]any)
	if block["type"] != "tool_use" {
		t.Errorf("block type 应为 tool_use，实际 %v", block["type"])
	}
	if block["id"] != "call_abc" {
		t.Errorf("id 应为 call_abc")
	}
	if block["name"] != "bash" {
		t.Errorf("name 应为 bash")
	}
	input := block["input"].(map[string]any)
	if input["command"] != "ls" {
		t.Errorf("input.command 应为 ls")
	}
}

func TestTransformToClaudeResponse_FinishReasonLength(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/messages", "", "claude-3")

	openaiResp := `{
		"id": "chatcmpl-123",
		"choices": [{"message": {"role": "assistant", "content": "partial..."}, "finish_reason": "length"}],
		"usage": {"prompt_tokens": 10, "completion_tokens": 100}
	}`

	result, err := tr.TransformResponse(ctx, []byte(openaiResp))
	if err != nil {
		t.Fatalf("TransformResponse 失败: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(result, &parsed)

	if parsed["stop_reason"] != "max_tokens" {
		t.Errorf("stop_reason 应为 max_tokens，实际 %v", parsed["stop_reason"])
	}
}

func TestTransformToClaudeResponse_InvalidJSON(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/messages", "", "claude-3")

	// 无法解析的 JSON 应直接返回原始内容
	body := []byte(`not json`)
	result, err := tr.TransformResponse(ctx, body)
	if err != nil {
		t.Fatalf("不应返回错误: %v", err)
	}
	if string(result) != string(body) {
		t.Errorf("无法解析时应返回原始内容")
	}
}

// === 流式响应转换测试 ===

func TestTransformToClaudeStreamChunk_TextDelta(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/messages", "", "claude-3")

	chunk := `{"choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`

	result, err := tr.TransformStream(ctx, []byte(chunk))
	if err != nil {
		t.Fatalf("TransformStream 失败: %v", err)
	}
	if result == nil {
		t.Fatal("结果不应为 nil")
	}

	var parsed map[string]any
	json.Unmarshal(result, &parsed)

	if parsed["type"] != "content_block_delta" {
		t.Errorf("type 应为 content_block_delta，实际 %v", parsed["type"])
	}
	delta := parsed["delta"].(map[string]any)
	if delta["type"] != "text_delta" {
		t.Errorf("delta.type 应为 text_delta")
	}
	if delta["text"] != "Hello" {
		t.Errorf("delta.text 应为 Hello，实际 %v", delta["text"])
	}
}

func TestTransformToClaudeStreamChunk_ReasoningContentDelta(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/messages", "", "claude-3")
	state := &StreamState{BlockIndex: -1, OpenBlocks: make(map[int]bool)}
	ctx = context.WithValue(ctx, StreamStateKey, state)

	chunk := `{"choices":[{"index":0,"delta":{"reasoning_content":"I should inspect the code."},"finish_reason":null}]}`

	result, err := tr.TransformStream(ctx, []byte(chunk))
	if err != nil {
		t.Fatalf("TransformStream 失败: %v", err)
	}
	if result == nil {
		t.Fatal("reasoning_content chunk 不应返回 nil")
	}

	var events []map[string]any
	if err := json.Unmarshal(result, &events); err != nil {
		t.Fatalf("解析事件数组失败: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("期望 2 个事件，实际 %d", len(events))
	}

	start := events[0]
	if start["type"] != "content_block_start" {
		t.Fatalf("第一个事件应为 content_block_start，实际 %v", start["type"])
	}
	block := start["content_block"].(map[string]any)
	if block["type"] != "thinking" {
		t.Fatalf("content_block.type 应为 thinking，实际 %v", block["type"])
	}

	delta := events[1]["delta"].(map[string]any)
	if delta["type"] != "thinking_delta" {
		t.Fatalf("delta.type 应为 thinking_delta，实际 %v", delta["type"])
	}
	if delta["thinking"] != "I should inspect the code." {
		t.Fatalf("thinking 内容不符合预期，实际 %v", delta["thinking"])
	}
}

func TestTransformToClaudeStreamChunk_TextAfterReasoningStartsTextBlock(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/messages", "", "claude-3")
	state := &StreamState{BlockIndex: -1, OpenBlocks: make(map[int]bool)}
	ctx = context.WithValue(ctx, StreamStateKey, state)

	reasoningChunk := `{"choices":[{"index":0,"delta":{"reasoning_content":"thinking"},"finish_reason":null}]}`
	if _, err := tr.TransformStream(ctx, []byte(reasoningChunk)); err != nil {
		t.Fatalf("TransformStream reasoning 失败: %v", err)
	}

	textChunk := `{"choices":[{"index":0,"delta":{"content":"answer"},"finish_reason":null}]}`
	result, err := tr.TransformStream(ctx, []byte(textChunk))
	if err != nil {
		t.Fatalf("TransformStream text 失败: %v", err)
	}

	var events []map[string]any
	if err := json.Unmarshal(result, &events); err != nil {
		t.Fatalf("解析事件数组失败: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("期望 stop thinking、start text、text delta 三个事件，实际 %d", len(events))
	}
	if events[0]["type"] != "content_block_stop" {
		t.Fatalf("第一个事件应停止 thinking block，实际 %v", events[0]["type"])
	}
	block := events[1]["content_block"].(map[string]any)
	if block["type"] != "text" {
		t.Fatalf("第二个事件应启动 text block，实际 %v", block["type"])
	}
	delta := events[2]["delta"].(map[string]any)
	if delta["type"] != "text_delta" || delta["text"] != "answer" {
		t.Fatalf("第三个事件应为 text_delta answer，实际 %v", delta)
	}
}

func TestTransformToClaudeStreamChunk_EmptyContent(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/messages", "", "claude-3")

	chunk := `{"choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`

	result, err := tr.TransformStream(ctx, []byte(chunk))
	if err != nil {
		t.Fatalf("TransformStream 失败: %v", err)
	}
	if result != nil {
		t.Error("空内容 chunk 应返回 nil")
	}
}

func TestTransformToClaudeStreamChunk_FinishStop(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/messages", "", "claude-3")

	chunk := `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`

	result, err := tr.TransformStream(ctx, []byte(chunk))
	if err != nil {
		t.Fatalf("TransformStream 失败: %v", err)
	}
	if result != nil {
		t.Error("finish_reason=stop 的 chunk 应返回 nil")
	}
}

func TestTransformToClaudeStreamChunk_FinishToolCalls(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/messages", "", "claude-3")

	chunk := `{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`

	result, err := tr.TransformStream(ctx, []byte(chunk))
	if err != nil {
		t.Fatalf("TransformStream 失败: %v", err)
	}
	if result != nil {
		t.Error("finish_reason=tool_calls 的 chunk 应返回 nil")
	}
}

func TestTransformToClaudeStreamChunk_NoChoices(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/messages", "", "claude-3")

	chunk := `{"choices":[]}`

	result, err := tr.TransformStream(ctx, []byte(chunk))
	if err != nil {
		t.Fatalf("TransformStream 失败: %v", err)
	}
	if result != nil {
		t.Error("空 choices 应返回 nil")
	}
}

func TestTransformToClaudeStreamChunk_InvalidJSON(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/messages", "", "claude-3")

	chunk := `not json`

	result, err := tr.TransformStream(ctx, []byte(chunk))
	if err != nil {
		t.Fatalf("不应返回错误: %v", err)
	}
	// 无法解析时返回原始内容
	if string(result) != chunk {
		t.Errorf("无法解析时应返回原始内容")
	}
}

func TestTransformToClaudeStreamChunk_ToolCallDelta(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/messages", "", "claude-3")
	state := &StreamState{BlockIndex: 0}
	ctx = context.WithValue(ctx, StreamStateKey, state)

	// 第一个 chunk：tool call 开始
	chunk := `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"bash","arguments":""}}]},"finish_reason":null}]}`

	result, err := tr.TransformStream(ctx, []byte(chunk))
	if err != nil {
		t.Fatalf("TransformStream 失败: %v", err)
	}
	if result == nil {
		t.Fatal("tool call 开始 chunk 不应返回 nil")
	}

	// 应该是事件数组
	var events []map[string]any
	if err := json.Unmarshal(result, &events); err != nil {
		t.Fatalf("解析事件数组失败: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("事件数组不应为空")
	}

	// 第一个事件应该是 content_block_start
	if events[0]["type"] != "content_block_start" {
		t.Errorf("第一个事件应为 content_block_start，实际 %v", events[0]["type"])
	}
	block := events[0]["content_block"].(map[string]any)
	if block["type"] != "tool_use" {
		t.Errorf("content_block.type 应为 tool_use")
	}
	if block["name"] != "bash" {
		t.Errorf("content_block.name 应为 bash")
	}
}

func TestTransformToClaudeStreamChunk_ToolCallArgsDelta(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/messages", "", "claude-3")
	state := &StreamState{BlockIndex: 1}
	ctx = context.WithValue(ctx, StreamStateKey, state)

	// arguments 增量 chunk
	chunk := `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"command\":"}}]},"finish_reason":null}]}`

	result, err := tr.TransformStream(ctx, []byte(chunk))
	if err != nil {
		t.Fatalf("TransformStream 失败: %v", err)
	}
	if result == nil {
		t.Fatal("args delta chunk 不应返回 nil")
	}

	var events []map[string]any
	json.Unmarshal(result, &events)

	if len(events) == 0 {
		t.Fatal("事件数组不应为空")
	}
	if events[0]["type"] != "content_block_delta" {
		t.Errorf("事件类型应为 content_block_delta，实际 %v", events[0]["type"])
	}
	delta := events[0]["delta"].(map[string]any)
	if delta["type"] != "input_json_delta" {
		t.Errorf("delta.type 应为 input_json_delta")
	}
}

func TestTransformToClaudeStreamChunk_ToolCallArgsDeltaWithFinishReason(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/messages", "", "claude-3")
	state := &StreamState{BlockIndex: 1}
	ctx = context.WithValue(ctx, StreamStateKey, state)

	chunk := `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"}"}}]},"finish_reason":"tool_calls"}]}`

	result, err := tr.TransformStream(ctx, []byte(chunk))
	if err != nil {
		t.Fatalf("TransformStream 失败: %v", err)
	}
	if result == nil {
		t.Fatal("带 finish_reason 的最后 arguments chunk 不应被跳过")
	}

	var events []map[string]any
	if err := json.Unmarshal(result, &events); err != nil {
		t.Fatalf("解析事件数组失败: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("期望 1 个事件，实际 %d", len(events))
	}
	delta := events[0]["delta"].(map[string]any)
	if delta["type"] != "input_json_delta" {
		t.Fatalf("delta.type 应为 input_json_delta，实际 %v", delta["type"])
	}
	if delta["partial_json"] != "}" {
		t.Fatalf("partial_json 应为 }，实际 %q", delta["partial_json"])
	}
}

// === Codex 请求转换测试 ===

func TestTransformCodexRequest_StringInput(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/responses", "real-model", "")

	body := []byte(`{
		"model": "gpt-4",
		"input": "hello world",
		"stream": true,
		"max_output_tokens": 500
	}`)

	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("TransformRequest 失败: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(result, &parsed)

	if parsed["model"] != "real-model" {
		t.Errorf("模型应被替换为 real-model")
	}
	if parsed["max_tokens"].(float64) != 500 {
		t.Errorf("max_tokens 应为 500")
	}

	msgs := parsed["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("消息数量应为 1，实际 %d", len(msgs))
	}
}

func TestTransformCodexRequest_ArrayInput(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/responses", "real-model", "")

	body := []byte(`{
		"model": "gpt-4",
		"input": [{"role": "user", "content": "hello"}],
		"stream": false
	}`)

	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("TransformRequest 失败: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(result, &parsed)

	msgs := parsed["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("消息数量应为 1，实际 %d", len(msgs))
	}
}

func TestTransformCodexRequest_WithInstructions(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/responses", "real-model", "")

	body := []byte(`{
		"model": "gpt-4",
		"input": "hello",
		"instructions": "You are a coding assistant",
		"temperature": 0.5
	}`)

	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("TransformRequest 失败: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(result, &parsed)

	msgs := parsed["messages"].([]any)
	if len(msgs) < 2 {
		t.Fatalf("应有至少 2 条消息（system + user），实际 %d", len(msgs))
	}
	firstMsg := msgs[0].(map[string]any)
	if firstMsg["role"] != "system" {
		t.Errorf("第一条消息应为 system")
	}
}

// === Codex 响应转换测试 ===

func TestTransformToCodexResponse(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/responses", "", "gpt-4")

	openaiResp := `{
		"id": "chatcmpl-123",
		"choices": [{"message": {"role": "assistant", "content": "Hello!"}, "finish_reason": "stop"}],
		"usage": {"prompt_tokens": 10, "completion_tokens": 5}
	}`

	result, err := tr.TransformResponse(ctx, []byte(openaiResp))
	if err != nil {
		t.Fatalf("TransformResponse 失败: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(result, &parsed)

	if parsed["object"] != "response" {
		t.Errorf("object 应为 response，实际 %v", parsed["object"])
	}
	if parsed["model"] != "gpt-4" {
		t.Errorf("model 应为 gpt-4")
	}
	if parsed["status"] != "completed" {
		t.Errorf("status 应为 completed")
	}
}

// === 默认路径透传测试 ===

func TestTransformRequest_UnknownPath(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/unknown/path", "", "")

	body := []byte(`{"model":"test","data":"unchanged"}`)
	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("未知路径不应返回错误: %v", err)
	}
	if string(result) != string(body) {
		t.Errorf("未知路径应透传请求体")
	}
}

func TestTransformResponse_UnknownPath(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/unknown/path", "", "")

	body := []byte(`{"result":"ok"}`)
	result, err := tr.TransformResponse(ctx, body)
	if err != nil {
		t.Fatalf("未知路径不应返回错误: %v", err)
	}
	if string(result) != string(body) {
		t.Errorf("未知路径应透传响应体")
	}
}

func TestTransformStream_UnknownPath(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/unknown/path", "", "")

	chunk := []byte(`{"data":"chunk"}`)
	result, err := tr.TransformStream(ctx, chunk)
	if err != nil {
		t.Fatalf("未知路径不应返回错误: %v", err)
	}
	if string(result) != string(chunk) {
		t.Errorf("未知路径应透传 chunk")
	}
}

func TestTransformStream_CodexPath(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/responses", "", "gpt-4")

	chunk := []byte(`{"data":"chunk"}`)
	result, err := tr.TransformStream(ctx, chunk)
	if err != nil {
		t.Fatalf("Codex 路径不应返回错误: %v", err)
	}
	if string(result) != string(chunk) {
		t.Errorf("Codex 路径应透传 chunk")
	}
}

func TestTransformClaudeRequest_InvalidJSON(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/messages", "real-model", "")

	_, err := tr.TransformRequest(ctx, []byte(`not json`))
	if err == nil {
		t.Fatal("无效 JSON 应返回错误")
	}
}

func TestTransformCodexRequest_InvalidJSON(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/responses", "real-model", "")

	_, err := tr.TransformRequest(ctx, []byte(`not json`))
	if err == nil {
		t.Fatal("无效 JSON 应返回错误")
	}
}

func TestExtractToolResultContent(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expected string
	}{
		{"string", "hello", "hello"},
		{"nil", nil, ""},
		{"array", []any{map[string]any{"type": "text", "text": "result"}}, "result"},
		{"number", 42, "42"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractToolResultContent(tt.input)
			if result != tt.expected {
				t.Errorf("期望 %q，实际 %q", tt.expected, result)
			}
		})
	}
}

func TestTransformClaudeRequest_ReasoningEffort_TopLevel(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/messages", "real-model", "")

	body := []byte(`{
		"model": "claude-3",
		"messages": [{"role": "user", "content": "hello"}],
		"max_tokens": 100,
		"reasoning_effort": "high"
	}`)

	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("TransformRequest 失败: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(result, &parsed)

	if parsed["reasoning_effort"] != "high" {
		t.Errorf("reasoning_effort 应为 high，实际 %v", parsed["reasoning_effort"])
	}
}

func TestTransformClaudeRequest_ReasoningEffort_OutputConfig(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/messages", "real-model", "")

	body := []byte(`{
		"model": "claude-3",
		"messages": [{"role": "user", "content": "hello"}],
		"max_tokens": 100,
		"output_config": {"effort": "medium"}
	}`)

	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("TransformRequest 失败: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(result, &parsed)

	if parsed["reasoning_effort"] != "medium" {
		t.Errorf("output_config.effort 应映射为 reasoning_effort=medium，实际 %v", parsed["reasoning_effort"])
	}
}

func TestTransformClaudeRequest_ReasoningEffort_TopLevelTakesPrecedence(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/messages", "real-model", "")

	body := []byte(`{
		"model": "claude-3",
		"messages": [{"role": "user", "content": "hello"}],
		"max_tokens": 100,
		"reasoning_effort": "high",
		"output_config": {"effort": "low"}
	}`)

	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("TransformRequest 失败: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(result, &parsed)

	// 顶层 reasoning_effort 优先于 output_config.effort
	if parsed["reasoning_effort"] != "high" {
		t.Errorf("顶层 reasoning_effort 应优先，实际 %v", parsed["reasoning_effort"])
	}
}

func TestTransformClaudeRequest_NoReasoningEffort(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/messages", "real-model", "")

	body := []byte(`{
		"model": "claude-3",
		"messages": [{"role": "user", "content": "hello"}],
		"max_tokens": 100
	}`)

	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("TransformRequest 失败: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(result, &parsed)

	if _, ok := parsed["reasoning_effort"]; ok {
		t.Error("没有 reasoning_effort 时不应设置该字段")
	}
}

func TestTransformClaudeRequest_TopP(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/messages", "real-model", "")

	body := []byte(`{"model":"claude-3","messages":[{"role":"user","content":"hi"}],"max_tokens":100,"top_p":0.9}`)

	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("TransformRequest 失败: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(result, &parsed)
	if parsed["top_p"].(float64) != 0.9 {
		t.Errorf("top_p 应为 0.9")
	}
}

func TestConvertClaudeUserMessage_PlainString(t *testing.T) {
	tr := &Transformer{}
	msg := map[string]any{"role": "user", "content": "hello"}
	result := tr.ConvertClaudeUserMessage(msg)
	if len(result) != 1 {
		t.Fatalf("应返回 1 条消息，实际 %d", len(result))
	}
	m := result[0].(map[string]any)
	if m["content"] != "hello" {
		t.Errorf("content 应为 hello")
	}
}

func TestConvertClaudeUserMessage_MixedContent(t *testing.T) {
	tr := &Transformer{}
	msg := map[string]any{
		"role": "user",
		"content": []any{
			map[string]any{"type": "text", "text": "before"},
			map[string]any{"type": "tool_result", "tool_use_id": "t1", "content": "result"},
		},
	}
	result := tr.ConvertClaudeUserMessage(msg)
	// 混合内容时，text 在前，tool 消息在后
	if len(result) != 2 {
		t.Fatalf("混合内容应返回 2 条消息，实际 %d", len(result))
	}
	m := result[0].(map[string]any)
	if m["content"] != "before" {
		t.Errorf("第一条应为 text 消息，实际 %v", m["content"])
	}
	toolMsg := result[1].(map[string]any)
	if toolMsg["role"] != "tool" {
		t.Errorf("第二条应为 tool 消息，实际 role=%v", toolMsg["role"])
	}
}

func TestConvertClaudeUserMessage_MultipleToolResults(t *testing.T) {
	tr := &Transformer{}
	msg := map[string]any{
		"role": "user",
		"content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "t1", "content": "r1"},
			map[string]any{"type": "tool_result", "tool_use_id": "t2", "content": "r2"},
		},
	}
	result := tr.ConvertClaudeUserMessage(msg)
	// 多个 tool_result 返回多条 tool 消息
	if len(result) != 2 {
		t.Errorf("应有 2 个 tool 消息，实际 %d", len(result))
	}
}

func TestConvertClaudeAssistantMessage_PlainString(t *testing.T) {
	tr := &Transformer{}
	msg := map[string]any{"role": "assistant", "content": "hello"}
	results := tr.ConvertClaudeAssistantMessage(msg)
	if len(results) != 1 {
		t.Fatalf("应返回 1 条消息，实际 %d", len(results))
	}
	m := results[0].(map[string]any)
	if m["content"] != "hello" {
		t.Errorf("content 应为 hello")
	}
}

func TestConvertClaudeAssistantMessage_OtherType(t *testing.T) {
	tr := &Transformer{}
	msg := map[string]any{"role": "assistant", "content": 123}
	results := tr.ConvertClaudeAssistantMessage(msg)
	if len(results) != 1 {
		t.Fatalf("应返回 1 条消息")
	}
}

func TestConvertClaudeToolChoice_Unknown(t *testing.T) {
	tr := &Transformer{}
	result := tr.ConvertClaudeToolChoice(map[string]any{"type": "unknown"})
	if result != "auto" {
		t.Errorf("未知 tool_choice 类型应返回 auto，实际 %v", result)
	}
}

func TestConvertClaudeToolChoice_NonMapNonString(t *testing.T) {
	tr := &Transformer{}
	result := tr.ConvertClaudeToolChoice(123)
	if result != "auto" {
		t.Errorf("非 map/string 类型应返回 auto，实际 %v", result)
	}
}

func TestTransformToClaudeResponse_EmptyChoices(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/messages", "", "claude-3")

	openaiResp := `{"id":"123","choices":[],"usage":{"prompt_tokens":0,"completion_tokens":0}}`
	result, err := tr.TransformResponse(ctx, []byte(openaiResp))
	if err != nil {
		t.Fatalf("TransformResponse 失败: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(result, &parsed)
	content := parsed["content"].([]any)
	if len(content) == 0 {
		t.Error("空 choices 时应有默认空 text block")
	}
}

func TestTransformToCodexResponse_InvalidJSON(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/responses", "", "gpt-4")

	body := []byte(`not json`)
	result, err := tr.TransformResponse(ctx, body)
	if err != nil {
		t.Fatalf("不应返回错误: %v", err)
	}
	if string(result) != string(body) {
		t.Errorf("无法解析时应返回原始内容")
	}
}

func TestNowISO(t *testing.T) {
	result := NowISO()
	if result == "" {
		t.Error("NowISO 不应返回空字符串")
	}
}

func TestTransformToClaudeStreamChunk_UsageChunk(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/messages", "", "claude-3")

	// usage chunk 没有有效 choices
	chunk := `{"usage":{"prompt_tokens":10,"completion_tokens":5}}`
	result, err := tr.TransformStream(ctx, []byte(chunk))
	if err != nil {
		t.Fatalf("不应返回错误: %v", err)
	}
	if result != nil {
		t.Error("usage chunk 应返回 nil")
	}
}

func TestTransformToClaudeStreamChunk_NoDelta(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/messages", "", "claude-3")

	chunk := `{"choices":[{"index":0}]}`
	result, err := tr.TransformStream(ctx, []byte(chunk))
	if err != nil {
		t.Fatalf("不应返回错误: %v", err)
	}
	if result != nil {
		t.Error("无 delta 的 chunk 应返回 nil")
	}
}

func TestHandleToolCallDelta_EmptyToolCalls(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/messages", "", "claude-3")

	result, err := tr.HandleToolCallDelta(ctx, []any{}, "claude-3")
	if err != nil {
		t.Fatalf("不应返回错误: %v", err)
	}
	if result != nil {
		t.Error("空 tool_calls 应返回 nil")
	}
}

func TestHandleToolCallDelta_InvalidItem(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/messages", "", "claude-3")

	result, err := tr.HandleToolCallDelta(ctx, []any{"not a map"}, "claude-3")
	if err != nil {
		t.Fatalf("不应返回错误: %v", err)
	}
	if result != nil {
		t.Error("无效 item 应返回 nil")
	}
}

// === Codex 请求转换 - Reasoning 相关测试 ===

func TestTransformCodexRequest_ReasoningInputItem(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/responses", "real-model", "")

	// 模拟包含 reasoning output item 的历史输入
	body := []byte(`{
		"model": "deepseek-r1",
		"input": [
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "What is 2+2?"}]},
			{"type": "reasoning", "id": "rs_123", "summary": [{"type": "summary_text", "text": "Let me calculate 2+2. The answer is 4."}]},
			{"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": "The answer is 4."}]},
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "What about 3+3?"}]}
		],
		"stream": true
	}`)

	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("TransformRequest 失败: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(result, &parsed)

	msgs := parsed["messages"].([]any)
	// 应有: user, assistant(with reasoning_content), user
	if len(msgs) != 3 {
		t.Fatalf("应有 3 条消息，实际 %d", len(msgs))
	}

	// 第一条: user
	m0 := msgs[0].(map[string]any)
	if m0["role"] != "user" {
		t.Errorf("msgs[0] role 应为 user，实际 %v", m0["role"])
	}

	// 第二条: assistant with reasoning_content
	m1 := msgs[1].(map[string]any)
	if m1["role"] != "assistant" {
		t.Errorf("msgs[1] role 应为 assistant，实际 %v", m1["role"])
	}
	if m1["reasoning_content"] != "Let me calculate 2+2. The answer is 4." {
		t.Errorf("msgs[1] reasoning_content 不正确: %v", m1["reasoning_content"])
	}
	if m1["content"] != "The answer is 4." {
		t.Errorf("msgs[1] content 应为 'The answer is 4.'，实际 %v", m1["content"])
	}

	// 第三条: user
	m2 := msgs[2].(map[string]any)
	if m2["role"] != "user" {
		t.Errorf("msgs[2] role 应为 user，实际 %v", m2["role"])
	}
}

func TestTransformCodexRequest_ReasoningBeforeFunctionCall(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/responses", "real-model", "")

	// reasoning 后面跟着 function_call（而不是 message）
	body := []byte(`{
		"model": "deepseek-r1",
		"input": [
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "list files"}]},
			{"type": "reasoning", "id": "rs_456", "summary": [{"type": "summary_text", "text": "I should run ls command."}]},
			{"type": "function_call", "call_id": "call_1", "name": "bash", "arguments": "{\"command\":\"ls\"}"},
			{"type": "function_call_output", "call_id": "call_1", "output": "file1.txt\nfile2.txt"}
		],
		"stream": true
	}`)

	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("TransformRequest 失败: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(result, &parsed)

	msgs := parsed["messages"].([]any)
	// 应有: user, assistant(with tool_calls + reasoning_content), tool
	if len(msgs) != 3 {
		t.Fatalf("应有 3 条消息，实际 %d", len(msgs))
	}

	// 第二条: assistant with tool_calls and reasoning_content
	m1 := msgs[1].(map[string]any)
	if m1["role"] != "assistant" {
		t.Errorf("msgs[1] role 应为 assistant，实际 %v", m1["role"])
	}
	if m1["reasoning_content"] != "I should run ls command." {
		t.Errorf("msgs[1] reasoning_content 不正确: %v", m1["reasoning_content"])
	}
	toolCallsAny, ok := m1["tool_calls"].([]any)
	if !ok || len(toolCallsAny) == 0 {
		t.Fatal("msgs[1] 应包含 tool_calls")
	}
	tc0 := toolCallsAny[0].(map[string]any)
	if tc0["id"] != "call_1" {
		t.Errorf("tool_calls[0].id 应为 call_1")
	}

	// 第三条: tool
	m2 := msgs[2].(map[string]any)
	if m2["role"] != "tool" {
		t.Errorf("msgs[2] role 应为 tool，实际 %v", m2["role"])
	}
}

func TestTransformCodexRequest_ReasoningWithoutFollowingAssistant(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/responses", "real-model", "")

	// reasoning 后面直接跟 user message（边界情况）
	body := []byte(`{
		"model": "deepseek-r1",
		"input": [
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "hello"}]},
			{"type": "reasoning", "id": "rs_789", "summary": [{"type": "summary_text", "text": "thinking..."}]},
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "another question"}]}
		],
		"stream": true
	}`)

	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("TransformRequest 失败: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(result, &parsed)

	msgs := parsed["messages"].([]any)
	// 应有: user, assistant(empty content + reasoning_content), user
	if len(msgs) != 3 {
		t.Fatalf("应有 3 条消息，实际 %d", len(msgs))
	}

	// 第二条: assistant with reasoning_content (flushed as standalone)
	m1 := msgs[1].(map[string]any)
	if m1["role"] != "assistant" {
		t.Errorf("msgs[1] role 应为 assistant，实际 %v", m1["role"])
	}
	if m1["reasoning_content"] != "thinking..." {
		t.Errorf("msgs[1] reasoning_content 不正确: %v", m1["reasoning_content"])
	}
}

func TestTransformCodexRequest_ReasoningAtEnd(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/responses", "real-model", "")

	// reasoning 在输入末尾（边界情况）
	body := []byte(`{
		"model": "deepseek-r1",
		"input": [
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "hello"}]},
			{"type": "reasoning", "id": "rs_end", "summary": [{"type": "summary_text", "text": "trailing reasoning"}]}
		],
		"stream": true
	}`)

	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("TransformRequest 失败: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(result, &parsed)

	msgs := parsed["messages"].([]any)
	// 应有: user, assistant(reasoning_content)
	if len(msgs) != 2 {
		t.Fatalf("应有 2 条消息，实际 %d", len(msgs))
	}

	m1 := msgs[1].(map[string]any)
	if m1["role"] != "assistant" {
		t.Errorf("msgs[1] role 应为 assistant")
	}
	if m1["reasoning_content"] != "trailing reasoning" {
		t.Errorf("msgs[1] reasoning_content 不正确: %v", m1["reasoning_content"])
	}
}

func TestTransformCodexRequest_ReasoningMultipleSummaryParts(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/responses", "real-model", "")

	// reasoning 有多个 summary parts
	body := []byte(`{
		"model": "deepseek-r1",
		"input": [
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "complex question"}]},
			{"type": "reasoning", "id": "rs_multi", "summary": [
				{"type": "summary_text", "text": "First thought."},
				{"type": "summary_text", "text": "Second thought."}
			]},
			{"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": "answer"}]}
		],
		"stream": true
	}`)

	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("TransformRequest 失败: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(result, &parsed)

	msgs := parsed["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("应有 2 条消息，实际 %d", len(msgs))
	}

	m1 := msgs[1].(map[string]any)
	if m1["reasoning_content"] != "First thought.\nSecond thought." {
		t.Errorf("多个 summary parts 应用换行连接，实际 %v", m1["reasoning_content"])
	}
}

func TestTransformCodexRequest_ReasoningEmptySummary(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/responses", "real-model", "")

	// reasoning 有空 summary
	body := []byte(`{
		"model": "deepseek-r1",
		"input": [
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "hello"}]},
			{"type": "reasoning", "id": "rs_empty", "summary": []},
			{"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": "hi"}]}
		],
		"stream": true
	}`)

	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("TransformRequest 失败: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(result, &parsed)

	msgs := parsed["messages"].([]any)
	// 空 reasoning 不应添加 reasoning_content
	if len(msgs) != 2 {
		t.Fatalf("应有 2 条消息，实际 %d", len(msgs))
	}

	m1 := msgs[1].(map[string]any)
	if _, hasReasoning := m1["reasoning_content"]; hasReasoning {
		t.Error("空 summary 不应设置 reasoning_content")
	}
}

func TestTransformCodexRequest_ReasoningEffort(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/responses", "real-model", "")

	body := []byte(`{
		"model": "deepseek-r1",
		"input": "hello",
		"reasoning": {"effort": "high", "summary": "auto"},
		"stream": true
	}`)

	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("TransformRequest 失败: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(result, &parsed)

	if parsed["reasoning_effort"] != "high" {
		t.Errorf("reasoning_effort 应为 high，实际 %v", parsed["reasoning_effort"])
	}
}

func TestTransformCodexRequest_ReasoningMarksMetadata(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/responses", "real-model", "")

	body := []byte(`{
		"model": "deepseek-r1",
		"input": [
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "hello"}]},
			{"type": "reasoning", "id": "rs_1", "summary": [{"type": "summary_text", "text": "thinking..."}]},
			{"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": "hi"}]}
		]
	}`)

	if _, err := tr.TransformRequest(ctx, body); err != nil {
		t.Fatalf("TransformRequest 失败: %v", err)
	}

	metadata, _ := ctx.Value(RequestMetadataKey).(*RequestMetadata)
	if metadata == nil || !metadata.ReasoningIncluded {
		t.Fatal("reasoning 内容应标记为已包含")
	}
}

// === Codex 响应转换 - Reasoning 相关测试 ===

func TestTransformToCodexResponse_WithReasoning(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/responses", "", "deepseek-r1")

	openaiResp := `{
		"id": "chatcmpl-123",
		"choices": [{
			"message": {
				"role": "assistant",
				"content": "The answer is 4.",
				"reasoning_content": "Let me calculate: 2+2=4"
			},
			"finish_reason": "stop"
		}],
		"usage": {"prompt_tokens": 10, "completion_tokens": 20}
	}`

	result, err := tr.TransformResponse(ctx, []byte(openaiResp))
	if err != nil {
		t.Fatalf("TransformResponse 失败: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(result, &parsed)

	output := parsed["output"].([]any)
	if len(output) != 2 {
		t.Fatalf("output 应有 2 个 item（reasoning + message），实际 %d", len(output))
	}

	// 第一个应为 reasoning
	reasoning := output[0].(map[string]any)
	if reasoning["type"] != "reasoning" {
		t.Errorf("output[0].type 应为 reasoning，实际 %v", reasoning["type"])
	}
	summary := reasoning["summary"].([]any)
	if len(summary) != 1 {
		t.Fatalf("summary 应有 1 个 part，实际 %d", len(summary))
	}
	summaryPart := summary[0].(map[string]any)
	if summaryPart["type"] != "summary_text" {
		t.Errorf("summary[0].type 应为 summary_text")
	}
	if summaryPart["text"] != "Let me calculate: 2+2=4" {
		t.Errorf("summary text 不正确: %v", summaryPart["text"])
	}

	// 第二个应为 message
	msg := output[1].(map[string]any)
	if msg["type"] != "message" {
		t.Errorf("output[1].type 应为 message，实际 %v", msg["type"])
	}
	content := msg["content"].([]any)
	contentPart := content[0].(map[string]any)
	if contentPart["text"] != "The answer is 4." {
		t.Errorf("message text 不正确: %v", contentPart["text"])
	}
}

func TestTransformToCodexResponse_WithoutReasoning(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/responses", "", "gpt-4")

	openaiResp := `{
		"id": "chatcmpl-456",
		"choices": [{
			"message": {"role": "assistant", "content": "Hello!"},
			"finish_reason": "stop"
		}],
		"usage": {"prompt_tokens": 5, "completion_tokens": 3}
	}`

	result, err := tr.TransformResponse(ctx, []byte(openaiResp))
	if err != nil {
		t.Fatalf("TransformResponse 失败: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(result, &parsed)

	output := parsed["output"].([]any)
	// 没有 reasoning 时只有 message
	if len(output) != 1 {
		t.Fatalf("output 应有 1 个 item（只有 message），实际 %d", len(output))
	}
	msg := output[0].(map[string]any)
	if msg["type"] != "message" {
		t.Errorf("output[0].type 应为 message，实际 %v", msg["type"])
	}
}

func TestTransformToCodexResponse_EmptyReasoningContent(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/responses", "", "deepseek-r1")

	// reasoning_content 为空字符串时不应添加 reasoning item
	openaiResp := `{
		"id": "chatcmpl-789",
		"choices": [{
			"message": {"role": "assistant", "content": "Hi", "reasoning_content": ""},
			"finish_reason": "stop"
		}],
		"usage": {"prompt_tokens": 5, "completion_tokens": 3}
	}`

	result, err := tr.TransformResponse(ctx, []byte(openaiResp))
	if err != nil {
		t.Fatalf("TransformResponse 失败: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(result, &parsed)

	output := parsed["output"].([]any)
	if len(output) != 1 {
		t.Fatalf("空 reasoning_content 时 output 应只有 1 个 item，实际 %d", len(output))
	}
}
