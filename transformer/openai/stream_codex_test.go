package openai

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func makeCodexCtx(clientModel string) context.Context {
	ctx := context.Background()
	ctx = context.WithValue(ctx, RequestPathKey, "/v1/responses")
	ctx = context.WithValue(ctx, ClientModelKey, clientModel)
	return ctx
}

func makeCodexCtxWithState(clientModel string) (context.Context, *CodexStreamState) {
	ctx := makeCodexCtx(clientModel)
	state := &CodexStreamState{
		ResponseID: "resp_test123",
		Model:      clientModel,
	}
	ctx = context.WithValue(ctx, CodexStreamStateKey, state)
	return ctx, state
}

func TestTransformToCodexStreamChunk_TextDelta(t *testing.T) {
	tr := &Transformer{}
	ctx, state := makeCodexCtxWithState("gpt-4")

	chunk := []byte(`{"choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`)
	events, err := tr.transformToCodexStreamChunk(ctx, chunk, "gpt-4")
	if err != nil {
		t.Fatalf("不应返回错误: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("应返回事件")
	}

	// 第一个事件应为 output_item.added
	var evt map[string]any
	json.Unmarshal(events[0], &evt)
	if evt["type"] != "response.output_item.added" {
		t.Errorf("第一个事件应为 response.output_item.added，实际 %v", evt["type"])
	}

	// 第二个事件应为 content_part.added
	json.Unmarshal(events[1], &evt)
	if evt["type"] != "response.content_part.added" {
		t.Errorf("第二个事件应为 response.content_part.added，实际 %v", evt["type"])
	}

	// 第三个事件应为 output_text.delta
	json.Unmarshal(events[2], &evt)
	if evt["type"] != "response.output_text.delta" {
		t.Errorf("第三个事件应为 response.output_text.delta，实际 %v", evt["type"])
	}
	if evt["delta"] != "Hello" {
		t.Errorf("delta 应为 Hello，实际 %v", evt["delta"])
	}
	if evt["output_index"].(float64) != 0 {
		t.Errorf("output_index 应为 0")
	}
	if evt["content_index"].(float64) != 0 {
		t.Errorf("content_index 应为 0")
	}

	// 验证状态更新
	if state.AccumulatedText != "Hello" {
		t.Errorf("AccumulatedText 应为 Hello，实际 %q", state.AccumulatedText)
	}
	if !state.OutputItemStarted {
		t.Error("OutputItemStarted 应为 true")
	}
	if !state.ContentPartStarted {
		t.Error("ContentPartStarted 应为 true")
	}
}

func TestTransformToCodexStreamChunk_MultipleTextDeltas(t *testing.T) {
	tr := &Transformer{}
	ctx, state := makeCodexCtxWithState("gpt-4")

	// 第一个 chunk
	chunk1 := []byte(`{"choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`)
	events1, _ := tr.transformToCodexStreamChunk(ctx, chunk1, "gpt-4")
	if len(events1) != 3 {
		t.Fatalf("第一个 chunk 应产生 3 个事件（item_added + part_added + delta），实际 %d", len(events1))
	}

	// 第二个 chunk - 不应再发送 item_added 和 part_added
	chunk2 := []byte(`{"choices":[{"index":0,"delta":{"content":" World"},"finish_reason":null}]}`)
	events2, _ := tr.transformToCodexStreamChunk(ctx, chunk2, "gpt-4")
	if len(events2) != 1 {
		t.Fatalf("后续 chunk 应只产生 1 个 delta 事件，实际 %d", len(events2))
	}

	var evt map[string]any
	json.Unmarshal(events2[0], &evt)
	if evt["type"] != "response.output_text.delta" {
		t.Errorf("事件类型应为 response.output_text.delta")
	}
	if evt["delta"] != " World" {
		t.Errorf("delta 应为 ' World'，实际 %v", evt["delta"])
	}

	if state.AccumulatedText != "Hello World" {
		t.Errorf("AccumulatedText 应为 'Hello World'，实际 %q", state.AccumulatedText)
	}
}

func TestTransformToCodexStreamChunk_FinishStop(t *testing.T) {
	tr := &Transformer{}
	ctx, state := makeCodexCtxWithState("gpt-4")

	// 先发送文本
	chunk1 := []byte(`{"choices":[{"index":0,"delta":{"content":"Done"},"finish_reason":null}]}`)
	tr.transformToCodexStreamChunk(ctx, chunk1, "gpt-4")

	// 发送 finish_reason=stop（带 usage，模拟 usage 和 finish 在同一个 chunk）
	chunk2 := []byte(`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`)
	events, err := tr.transformToCodexStreamChunk(ctx, chunk2, "gpt-4")
	if err != nil {
		t.Fatalf("不应返回错误: %v", err)
	}

	// 应包含: text_done, content_part_done, output_item_done, response.completed
	if len(events) < 4 {
		t.Fatalf("finish 应产生至少 4 个事件，实际 %d", len(events))
	}

	// 验证最后一个事件是 response.completed
	var lastEvt map[string]any
	json.Unmarshal(events[len(events)-1], &lastEvt)
	if lastEvt["type"] != "response.completed" {
		t.Errorf("最后一个事件应为 response.completed，实际 %v", lastEvt["type"])
	}

	resp := lastEvt["response"].(map[string]any)
	if resp["status"] != "completed" {
		t.Errorf("status 应为 completed，实际 %v", resp["status"])
	}
	if resp["id"] != state.ResponseID {
		t.Errorf("response id 应为 %s", state.ResponseID)
	}
}

func TestTransformToCodexStreamChunk_FinishLength(t *testing.T) {
	tr := &Transformer{}
	ctx, _ := makeCodexCtxWithState("gpt-4")

	// 先发送文本
	chunk1 := []byte(`{"choices":[{"index":0,"delta":{"content":"partial"},"finish_reason":null}]}`)
	tr.transformToCodexStreamChunk(ctx, chunk1, "gpt-4")

	// 发送 finish_reason=length（带 usage）
	chunk2 := []byte(`{"choices":[{"index":0,"delta":{},"finish_reason":"length"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`)
	events, _ := tr.transformToCodexStreamChunk(ctx, chunk2, "gpt-4")

	var lastEvt map[string]any
	json.Unmarshal(events[len(events)-1], &lastEvt)
	if lastEvt["type"] != "response.completed" {
		t.Fatalf("最后事件应为 response.completed")
	}
	resp := lastEvt["response"].(map[string]any)
	if resp["status"] != "incomplete" {
		t.Errorf("finish_reason=length 时 status 应为 incomplete，实际 %v", resp["status"])
	}
}

func TestTransformToCodexStreamChunk_EmptyContent(t *testing.T) {
	tr := &Transformer{}
	ctx, _ := makeCodexCtxWithState("gpt-4")

	chunk := []byte(`{"choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`)
	events, err := tr.transformToCodexStreamChunk(ctx, chunk, "gpt-4")
	if err != nil {
		t.Fatalf("不应返回错误: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("空 content 不应产生事件，实际产生 %d 个", len(events))
	}
}

func TestTransformToCodexStreamChunk_NoChoices(t *testing.T) {
	tr := &Transformer{}
	ctx, _ := makeCodexCtxWithState("gpt-4")

	chunk := []byte(`{"choices":[]}`)
	events, err := tr.transformToCodexStreamChunk(ctx, chunk, "gpt-4")
	if err != nil {
		t.Fatalf("不应返回错误: %v", err)
	}
	if events != nil {
		t.Error("空 choices 应返回 nil")
	}
}

func TestTransformToCodexStreamChunk_InvalidJSON(t *testing.T) {
	tr := &Transformer{}
	ctx, _ := makeCodexCtxWithState("gpt-4")

	chunk := []byte(`not json`)
	events, err := tr.transformToCodexStreamChunk(ctx, chunk, "gpt-4")
	if err != nil {
		t.Fatalf("不应返回错误: %v", err)
	}
	if events != nil {
		t.Error("无效 JSON 应返回 nil")
	}
}

func TestTransformToCodexStreamChunk_NilState(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCodexCtx("gpt-4") // 没有 state

	chunk := []byte(`{"choices":[{"index":0,"delta":{"content":"Hi"},"finish_reason":null}]}`)
	events, err := tr.transformToCodexStreamChunk(ctx, chunk, "gpt-4")
	if err != nil {
		t.Fatalf("不应返回错误: %v", err)
	}
	if events != nil {
		t.Error("无 state 时应返回 nil")
	}
}

func TestTransformToCodexStreamChunk_UsageExtraction(t *testing.T) {
	tr := &Transformer{}
	ctx, state := makeCodexCtxWithState("gpt-4")

	// 带 usage 的 chunk（通常是最后一个）
	chunk := []byte(`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":15,"completion_tokens":8}}`)
	tr.transformToCodexStreamChunk(ctx, chunk, "gpt-4")

	if state.InputTokens != 15 {
		t.Errorf("InputTokens 应为 15，实际 %d", state.InputTokens)
	}
	if state.OutputTokens != 8 {
		t.Errorf("OutputTokens 应为 8，实际 %d", state.OutputTokens)
	}
}

func TestTransformToCodexStreamChunk_ToolCallStart(t *testing.T) {
	tr := &Transformer{}
	ctx, state := makeCodexCtxWithState("gpt-4")

	chunk := []byte(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_abc","type":"function","function":{"name":"bash","arguments":""}}]},"finish_reason":null}]}`)
	events, err := tr.transformToCodexStreamChunk(ctx, chunk, "gpt-4")
	if err != nil {
		t.Fatalf("不应返回错误: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("tool call 开始应产生事件")
	}

	// 应包含 output_item.added 事件
	var evt map[string]any
	json.Unmarshal(events[0], &evt)
	if evt["type"] != "response.output_item.added" {
		t.Errorf("第一个事件应为 response.output_item.added，实际 %v", evt["type"])
	}
	item := evt["item"].(map[string]any)
	if item["type"] != "function_call" {
		t.Errorf("item.type 应为 function_call，实际 %v", item["type"])
	}
	if item["name"] != "bash" {
		t.Errorf("item.name 应为 bash，实际 %v", item["name"])
	}
	if item["call_id"] != "call_abc" {
		t.Errorf("item.call_id 应为 call_abc")
	}

	if len(state.FunctionCalls) != 1 {
		t.Fatalf("FunctionCalls 应有 1 个，实际 %d", len(state.FunctionCalls))
	}
	if state.FunctionCalls[0].Name != "bash" {
		t.Errorf("FunctionCalls[0].Name 应为 bash")
	}
}

func TestTransformToCodexStreamChunk_ToolCallArgsDelta(t *testing.T) {
	tr := &Transformer{}
	ctx, state := makeCodexCtxWithState("gpt-4")

	// 先发送 tool call 开始
	chunk1 := []byte(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"bash","arguments":""}}]},"finish_reason":null}]}`)
	tr.transformToCodexStreamChunk(ctx, chunk1, "gpt-4")

	// 发送 arguments 增量
	chunk2 := []byte(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"cmd\":"}}]},"finish_reason":null}]}`)
	events, err := tr.transformToCodexStreamChunk(ctx, chunk2, "gpt-4")
	if err != nil {
		t.Fatalf("不应返回错误: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("args delta 应产生 1 个事件，实际 %d", len(events))
	}

	var evt map[string]any
	json.Unmarshal(events[0], &evt)
	if evt["type"] != "response.function_call_arguments.delta" {
		t.Errorf("事件类型应为 response.function_call_arguments.delta，实际 %v", evt["type"])
	}
	if evt["delta"] != `{"cmd":` {
		t.Errorf("delta 应为 '{\"cmd\":'，实际 %v", evt["delta"])
	}

	if state.FunctionCalls[0].Arguments != `{"cmd":` {
		t.Errorf("累积 arguments 不正确: %q", state.FunctionCalls[0].Arguments)
	}
}

func TestTransformToCodexStreamChunk_ToolCallComplete(t *testing.T) {
	tr := &Transformer{}
	ctx, _ := makeCodexCtxWithState("gpt-4")

	// tool call 开始
	chunk1 := []byte(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"bash","arguments":""}}]},"finish_reason":null}]}`)
	tr.transformToCodexStreamChunk(ctx, chunk1, "gpt-4")

	// arguments 增量
	chunk2 := []byte(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"command\":\"ls\"}"}}]},"finish_reason":null}]}`)
	tr.transformToCodexStreamChunk(ctx, chunk2, "gpt-4")

	// finish
	chunk3 := []byte(`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":20,"completion_tokens":15}}`)
	events, _ := tr.transformToCodexStreamChunk(ctx, chunk3, "gpt-4")

	// 应包含 function_call_arguments.done, output_item.done, response.completed
	foundArgsDone := false
	foundItemDone := false
	foundCompleted := false
	for _, e := range events {
		var evt map[string]any
		json.Unmarshal(e, &evt)
		switch evt["type"] {
		case "response.function_call_arguments.done":
			foundArgsDone = true
			if evt["arguments"] != `{"command":"ls"}` {
				t.Errorf("arguments 应为完整 JSON，实际 %v", evt["arguments"])
			}
		case "response.output_item.done":
			foundItemDone = true
			item := evt["item"].(map[string]any)
			if item["status"] != "completed" {
				t.Errorf("item.status 应为 completed")
			}
			if item["name"] != "bash" {
				t.Errorf("item.name 应为 bash")
			}
		case "response.completed":
			foundCompleted = true
			resp := evt["response"].(map[string]any)
			if resp["status"] != "completed" {
				t.Errorf("response.status 应为 completed")
			}
		}
	}
	if !foundArgsDone {
		t.Error("应包含 function_call_arguments.done 事件")
	}
	if !foundItemDone {
		t.Error("应包含 output_item.done 事件")
	}
	if !foundCompleted {
		t.Error("应包含 response.completed 事件")
	}
}

func TestTransformToCodexStreamChunk_TextThenToolCall(t *testing.T) {
	tr := &Transformer{}
	ctx, state := makeCodexCtxWithState("gpt-4")

	// 先发送文本
	chunk1 := []byte(`{"choices":[{"index":0,"delta":{"content":"Let me run that."},"finish_reason":null}]}`)
	tr.transformToCodexStreamChunk(ctx, chunk1, "gpt-4")

	if !state.OutputItemStarted || !state.ContentPartStarted {
		t.Fatal("文本后 OutputItemStarted 和 ContentPartStarted 应为 true")
	}

	// 然后发送 tool call - 应先关闭文本 output item
	chunk2 := []byte(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"bash","arguments":"{}"}}]},"finish_reason":null}]}`)
	events, _ := tr.transformToCodexStreamChunk(ctx, chunk2, "gpt-4")

	// 应包含关闭文本的事件 + 新 tool call 的事件
	foundTextDone := false
	foundContentPartDone := false
	foundOutputItemDone := false
	foundToolItemAdded := false
	for _, e := range events {
		var evt map[string]any
		json.Unmarshal(e, &evt)
		switch evt["type"] {
		case "response.output_text.done":
			foundTextDone = true
		case "response.content_part.done":
			foundContentPartDone = true
		case "response.output_item.done":
			foundOutputItemDone = true
		case "response.output_item.added":
			item := evt["item"].(map[string]any)
			if item["type"] == "function_call" {
				foundToolItemAdded = true
			}
		}
	}
	if !foundTextDone {
		t.Error("应包含 output_text.done 事件")
	}
	if !foundContentPartDone {
		t.Error("应包含 content_part.done 事件")
	}
	if !foundOutputItemDone {
		t.Error("应包含 output_item.done 事件")
	}
	if !foundToolItemAdded {
		t.Error("应包含 function_call output_item.added 事件")
	}

	// 文本状态应被重置
	if state.OutputItemStarted {
		t.Error("tool call 后 OutputItemStarted 应为 false")
	}
	if state.ContentPartStarted {
		t.Error("tool call 后 ContentPartStarted 应为 false")
	}
}

func TestTransformToCodexStreamChunk_SequenceNumbers(t *testing.T) {
	tr := &Transformer{}
	ctx, state := makeCodexCtxWithState("gpt-4")

	chunk := []byte(`{"choices":[{"index":0,"delta":{"content":"A"},"finish_reason":null}]}`)
	events, _ := tr.transformToCodexStreamChunk(ctx, chunk, "gpt-4")

	// 验证序列号递增
	prevSeq := 0
	for _, e := range events {
		var evt map[string]any
		json.Unmarshal(e, &evt)
		seq := int(evt["sequence_number"].(float64))
		if seq <= prevSeq {
			t.Errorf("序列号应递增，前一个 %d，当前 %d", prevSeq, seq)
		}
		prevSeq = seq
	}

	if state.SequenceNumber != prevSeq {
		t.Errorf("state.SequenceNumber 应为 %d，实际 %d", prevSeq, state.SequenceNumber)
	}
}

func TestTransformToCodexStreamChunk_CompletedEventUsage(t *testing.T) {
	tr := &Transformer{}
	ctx, _ := makeCodexCtxWithState("gpt-4")

	// 发送文本
	chunk1 := []byte(`{"choices":[{"index":0,"delta":{"content":"Hi"},"finish_reason":null}]}`)
	tr.transformToCodexStreamChunk(ctx, chunk1, "gpt-4")

	// finish 带 usage（模拟 usage 和 finish 在同一个 chunk）
	chunk2 := []byte(`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":10}}`)
	events, _ := tr.transformToCodexStreamChunk(ctx, chunk2, "gpt-4")

	// 找到 response.completed 事件
	for _, e := range events {
		var evt map[string]any
		json.Unmarshal(e, &evt)
		if evt["type"] == "response.completed" {
			resp := evt["response"].(map[string]any)
			usage := resp["usage"].(map[string]any)
			if usage["input_tokens"].(float64) != 20 {
				t.Errorf("input_tokens 应为 20，实际 %v", usage["input_tokens"])
			}
			if usage["output_tokens"].(float64) != 10 {
				t.Errorf("output_tokens 应为 10，实际 %v", usage["output_tokens"])
			}
			if usage["total_tokens"].(float64) != 30 {
				t.Errorf("total_tokens 应为 30，实际 %v", usage["total_tokens"])
			}
			return
		}
	}
	t.Error("未找到 response.completed 事件")
}

func TestTransformToCodexStreamChunk_CompletedEventOutput(t *testing.T) {
	tr := &Transformer{}
	ctx, _ := makeCodexCtxWithState("gpt-4")

	// 发送文本
	chunk1 := []byte(`{"choices":[{"index":0,"delta":{"content":"Hello World"},"finish_reason":null}]}`)
	tr.transformToCodexStreamChunk(ctx, chunk1, "gpt-4")

	// finish 带 usage
	chunk2 := []byte(`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`)
	events, _ := tr.transformToCodexStreamChunk(ctx, chunk2, "gpt-4")

	// 找到 response.completed 事件，验证 output 包含完整文本
	for _, e := range events {
		var evt map[string]any
		json.Unmarshal(e, &evt)
		if evt["type"] == "response.completed" {
			resp := evt["response"].(map[string]any)
			output := resp["output"].([]any)
			if len(output) != 1 {
				t.Fatalf("output 应有 1 个 item，实际 %d", len(output))
			}
			msg := output[0].(map[string]any)
			if msg["type"] != "message" {
				t.Errorf("output[0].type 应为 message")
			}
			content := msg["content"].([]any)
			part := content[0].(map[string]any)
			if part["text"] != "Hello World" {
				t.Errorf("output text 应为 'Hello World'，实际 %v", part["text"])
			}
			return
		}
	}
	t.Error("未找到 response.completed 事件")
}

func TestTransformToCodexStreamChunk_ContentFilterFinish(t *testing.T) {
	tr := &Transformer{}
	ctx, _ := makeCodexCtxWithState("gpt-4")

	// 发送文本
	chunk1 := []byte(`{"choices":[{"index":0,"delta":{"content":"bad"},"finish_reason":null}]}`)
	tr.transformToCodexStreamChunk(ctx, chunk1, "gpt-4")

	// content_filter finish 带 usage
	chunk2 := []byte(`{"choices":[{"index":0,"delta":{},"finish_reason":"content_filter"}],"usage":{"prompt_tokens":5,"completion_tokens":1}}`)
	events, _ := tr.transformToCodexStreamChunk(ctx, chunk2, "gpt-4")

	for _, e := range events {
		var evt map[string]any
		json.Unmarshal(e, &evt)
		if evt["type"] == "response.completed" {
			resp := evt["response"].(map[string]any)
			if resp["status"] != "failed" {
				t.Errorf("content_filter 时 status 应为 failed，实际 %v", resp["status"])
			}
			return
		}
	}
	t.Error("未找到 response.completed 事件")
}

func TestBuildCodexCreatedEvent(t *testing.T) {
	state := &CodexStreamState{
		ResponseID: "resp_test",
		Model:      "gpt-4",
	}

	event := BuildCodexCreatedEvent(state)

	if event["type"] != "response.created" {
		t.Errorf("type 应为 response.created，实际 %v", event["type"])
	}
	if event["sequence_number"].(int) != 1 {
		t.Errorf("sequence_number 应为 1，实际 %v", event["sequence_number"])
	}

	resp := event["response"].(map[string]any)
	if resp["id"] != "resp_test" {
		t.Errorf("response.id 应为 resp_test")
	}
	if resp["model"] != "gpt-4" {
		t.Errorf("response.model 应为 gpt-4")
	}
	if resp["status"] != "in_progress" {
		t.Errorf("response.status 应为 in_progress")
	}
}

func TestBuildCodexInProgressEvent(t *testing.T) {
	state := &CodexStreamState{
		ResponseID:     "resp_test",
		Model:          "gpt-4",
		SequenceNumber: 1, // 模拟 created 已发送
	}

	event := BuildCodexInProgressEvent(state)

	if event["type"] != "response.in_progress" {
		t.Errorf("type 应为 response.in_progress，实际 %v", event["type"])
	}
	if event["sequence_number"].(int) != 2 {
		t.Errorf("sequence_number 应为 2，实际 %v", event["sequence_number"])
	}

	resp := event["response"].(map[string]any)
	if resp["status"] != "in_progress" {
		t.Errorf("response.status 应为 in_progress")
	}
}

func TestTransformCodexStream_Interface(t *testing.T) {
	tr := &Transformer{}
	ctx, _ := makeCodexCtxWithState("gpt-4")

	chunk := []byte(`{"choices":[{"index":0,"delta":{"content":"test"},"finish_reason":null}]}`)
	events, err := tr.TransformCodexStream(ctx, chunk)
	if err != nil {
		t.Fatalf("TransformCodexStream 不应返回错误: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("应返回事件")
	}

	// 验证通过接口调用也能正常工作
	var evt map[string]any
	json.Unmarshal(events[len(events)-1], &evt)
	if evt["type"] != "response.output_text.delta" {
		t.Errorf("最后一个事件应为 response.output_text.delta，实际 %v", evt["type"])
	}
}

func TestTransformToCodexStreamChunk_NoDelta(t *testing.T) {
	tr := &Transformer{}
	ctx, _ := makeCodexCtxWithState("gpt-4")

	// choice 中没有 delta 字段
	chunk := []byte(`{"choices":[{"index":0}]}`)
	events, err := tr.transformToCodexStreamChunk(ctx, chunk, "gpt-4")
	if err != nil {
		t.Fatalf("不应返回错误: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("无 delta 时不应产生事件，实际 %d 个", len(events))
	}
}

func TestTransformToCodexStreamChunk_OnlyFinishNoContent(t *testing.T) {
	tr := &Transformer{}
	ctx, state := makeCodexCtxWithState("gpt-4")

	// 直接 finish 没有任何内容（边界情况）- 不带 usage，应标记为 Finished
	chunk := []byte(`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
	events, err := tr.transformToCodexStreamChunk(ctx, chunk, "gpt-4")
	if err != nil {
		t.Fatalf("不应返回错误: %v", err)
	}

	// 没有 usage 时不应立即产生 response.completed，而是标记 Finished
	if len(events) != 0 {
		t.Fatalf("无 usage 时 finish 不应立即产生事件，实际 %d", len(events))
	}
	if !state.Finished {
		t.Error("state.Finished 应为 true")
	}
	if state.FinishStatus != "completed" {
		t.Errorf("state.FinishStatus 应为 completed，实际 %v", state.FinishStatus)
	}

	// 模拟后续收到 usage chunk（choices 为空）
	usageChunk := []byte(`{"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":2}}`)
	events2, _ := tr.transformToCodexStreamChunk(ctx, usageChunk, "gpt-4")
	if len(events2) != 1 {
		t.Fatalf("收到 usage 后应产生 1 个事件，实际 %d", len(events2))
	}
	var evt map[string]any
	json.Unmarshal(events2[0], &evt)
	if evt["type"] != "response.completed" {
		t.Errorf("事件应为 response.completed，实际 %v", evt["type"])
	}
}

func TestTransformToCodexStreamChunk_MultipleToolCalls(t *testing.T) {
	tr := &Transformer{}
	ctx, _ := makeCodexCtxWithState("gpt-4")

	// 第一个 tool call
	chunk1 := []byte(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read_file","arguments":""}}]},"finish_reason":null}]}`)
	events1, _ := tr.transformToCodexStreamChunk(ctx, chunk1, "gpt-4")
	if len(events1) == 0 {
		t.Fatal("第一个 tool call 应产生事件")
	}

	// 第一个 tool call 的 arguments
	chunk2 := []byte(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":\"main.go\"}"}}]},"finish_reason":null}]}`)
	events2, _ := tr.transformToCodexStreamChunk(ctx, chunk2, "gpt-4")
	if len(events2) != 1 {
		t.Fatalf("args delta 应产生 1 个事件，实际 %d", len(events2))
	}

	// 第二个 tool call
	chunk3 := []byte(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_2","type":"function","function":{"name":"write_file","arguments":"{\"path\":\"out.txt\"}"}}]},"finish_reason":null}]}`)
	events3, _ := tr.transformToCodexStreamChunk(ctx, chunk3, "gpt-4")

	// 应包含新的 output_item.added 和 args delta
	foundNewItem := false
	for _, e := range events3 {
		var evt map[string]any
		json.Unmarshal(e, &evt)
		if evt["type"] == "response.output_item.added" {
			item := evt["item"].(map[string]any)
			if item["name"] == "write_file" {
				foundNewItem = true
			}
		}
	}
	if !foundNewItem {
		t.Error("第二个 tool call 应产生 output_item.added 事件")
	}

	// finish 带 usage
	chunk4 := []byte(`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":30,"completion_tokens":20}}`)
	events4, _ := tr.transformToCodexStreamChunk(ctx, chunk4, "gpt-4")

	// 验证 completed 事件包含两个 function calls
	for _, e := range events4 {
		var evt map[string]any
		json.Unmarshal(e, &evt)
		if evt["type"] == "response.completed" {
			resp := evt["response"].(map[string]any)
			output := resp["output"].([]any)
			fcCount := 0
			for _, o := range output {
				item := o.(map[string]any)
				if item["type"] == "function_call" {
					fcCount++
				}
			}
			if fcCount != 2 {
				t.Errorf("completed 事件应包含 2 个 function_call，实际 %d", fcCount)
			}
			return
		}
	}
	t.Error("未找到 response.completed 事件")
}

func TestTransformCodexRequest_StreamOptions(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/responses", "real-model", "")

	body := []byte(`{"model":"gpt-4","input":"hello","stream":true}`)
	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("TransformRequest 失败: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(result, &parsed)

	streamOpts, ok := parsed["stream_options"].(map[string]any)
	if !ok {
		t.Fatal("stream=true 时应包含 stream_options")
	}
	if streamOpts["include_usage"] != true {
		t.Errorf("stream_options.include_usage 应为 true")
	}
}

func TestTransformCodexRequest_NoStreamOptions_WhenNotStreaming(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/responses", "real-model", "")

	body := []byte(`{"model":"gpt-4","input":"hello","stream":false}`)
	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("TransformRequest 失败: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(result, &parsed)

	if _, ok := parsed["stream_options"]; ok {
		t.Error("stream=false 时不应包含 stream_options")
	}
}

// === Codex 请求转换增强测试 ===

func TestTransformCodexRequest_StructuredInput(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/responses", "real-model", "")

	// 模拟 Codex CLI 发送的结构化 input
	body := []byte(`{
		"model": "gpt-4",
		"instructions": "You are helpful.",
		"input": [
			{"type": "message", "role": "developer", "content": [{"type": "input_text", "text": "System context here"}]},
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "Hello world"}]}
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
	// 应有: instructions(system) + developer(system) + user
	if len(msgs) < 3 {
		t.Fatalf("应有至少 3 条消息，实际 %d", len(msgs))
	}

	// 第一条是 instructions -> system
	first := msgs[0].(map[string]any)
	if first["role"] != "system" {
		t.Errorf("第一条应为 system (instructions)，实际 %v", first["role"])
	}

	// 第二条是 developer -> system
	second := msgs[1].(map[string]any)
	if second["role"] != "system" {
		t.Errorf("第二条应为 system (developer)，实际 %v", second["role"])
	}
	if !strings.Contains(second["content"].(string), "System context here") {
		t.Errorf("developer 消息内容应包含 'System context here'")
	}

	// 第三条是 user
	third := msgs[2].(map[string]any)
	if third["role"] != "user" {
		t.Errorf("第三条应为 user，实际 %v", third["role"])
	}
	if third["content"] != "Hello world" {
		t.Errorf("user 消息内容应为 'Hello world'，实际 %v", third["content"])
	}
}

func TestTransformCodexRequest_WithTools(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/responses", "real-model", "")

	body := []byte(`{
		"model": "gpt-4",
		"input": "hello",
		"tools": [{"type": "function", "name": "bash", "description": "run bash", "parameters": {"type": "object"}, "strict": false}],
		"tool_choice": "auto",
		"parallel_tool_calls": false,
		"stream": true
	}`)

	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("TransformRequest 失败: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(result, &parsed)

	// tools 应被转换为 Chat Completions 格式
	tools, ok := parsed["tools"].([]any)
	if !ok || len(tools) == 0 {
		t.Fatal("tools 应被转发")
	}
	tool0 := tools[0].(map[string]any)
	if tool0["type"] != "function" {
		t.Errorf("tool type 应为 function")
	}
	fn := tool0["function"].(map[string]any)
	if fn["name"] != "bash" {
		t.Errorf("function.name 应为 bash，实际 %v", fn["name"])
	}
	if fn["description"] != "run bash" {
		t.Errorf("function.description 应为 'run bash'")
	}

	// tool_choice 应被转发
	if parsed["tool_choice"] != "auto" {
		t.Errorf("tool_choice 应为 auto，实际 %v", parsed["tool_choice"])
	}

	// parallel_tool_calls 应被转发
	if parsed["parallel_tool_calls"] != false {
		t.Errorf("parallel_tool_calls 应为 false")
	}
}

func TestTransformCodexRequest_FunctionCallHistory(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/responses", "real-model", "")

	// 模拟包含 function_call 和 function_call_output 的历史
	body := []byte(`{
		"model": "gpt-4",
		"input": [
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "run ls"}]},
			{"type": "function_call", "call_id": "call_1", "name": "bash", "arguments": "{\"command\":\"ls\"}"},
			{"type": "function_call_output", "call_id": "call_1", "output": "file1.txt\nfile2.txt"},
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "now run pwd"}]}
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
	if len(msgs) != 4 {
		t.Fatalf("应有 4 条消息，实际 %d", len(msgs))
	}

	// 第一条: user
	m0 := msgs[0].(map[string]any)
	if m0["role"] != "user" {
		t.Errorf("msgs[0] role 应为 user，实际 %v", m0["role"])
	}

	// 第二条: assistant with tool_calls
	m1 := msgs[1].(map[string]any)
	if m1["role"] != "assistant" {
		t.Errorf("msgs[1] role 应为 assistant，实际 %v", m1["role"])
	}
	toolCallsAny, ok := m1["tool_calls"].([]any)
	if !ok || len(toolCallsAny) == 0 {
		t.Fatal("msgs[1] 应包含 tool_calls")
	}
	tc := toolCallsAny[0].(map[string]any)
	if tc["id"] != "call_1" {
		t.Errorf("tool_calls[0].id 应为 call_1")
	}

	// 第三条: tool
	m2 := msgs[2].(map[string]any)
	if m2["role"] != "tool" {
		t.Errorf("msgs[2] role 应为 tool，实际 %v", m2["role"])
	}
	if m2["tool_call_id"] != "call_1" {
		t.Errorf("msgs[2] tool_call_id 应为 call_1")
	}

	// 第四条: user
	m3 := msgs[3].(map[string]any)
	if m3["role"] != "user" {
		t.Errorf("msgs[3] role 应为 user，实际 %v", m3["role"])
	}
}

func TestTransformCodexRequest_MultipleInputTexts(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/responses", "real-model", "")

	// 一个 message 中有多个 input_text content parts
	body := []byte(`{
		"model": "gpt-4",
		"input": [
			{"type": "message", "role": "developer", "content": [
				{"type": "input_text", "text": "Part 1"},
				{"type": "input_text", "text": "Part 2"}
			]}
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
	if len(msgs) != 1 {
		t.Fatalf("应有 1 条消息，实际 %d", len(msgs))
	}

	msg := msgs[0].(map[string]any)
	content := msg["content"].(string)
	if !strings.Contains(content, "Part 1") || !strings.Contains(content, "Part 2") {
		t.Errorf("content 应包含两个 part，实际 %q", content)
	}
}

func TestTransformCodexRequest_PlainMessageFormat(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/responses", "real-model", "")

	// input 是已经是 Chat Completions 格式的消息数组（无 type 字段）
	body := []byte(`{
		"model": "gpt-4",
		"input": [
			{"role": "user", "content": "hello"}
		],
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
		t.Fatalf("应有 1 条消息，实际 %d", len(msgs))
	}

	msg := msgs[0].(map[string]any)
	if msg["role"] != "user" {
		t.Errorf("role 应为 user")
	}
	if msg["content"] != "hello" {
		t.Errorf("content 应为 hello")
	}
}

func TestTransformCodexRequest_NamespaceTools(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/responses", "real-model", "")

	body := []byte(`{
		"model": "gpt-4",
		"input": "hello",
		"tools": [
			{"type": "function", "name": "exec_command", "description": "Run a command", "strict": false, "parameters": {"type": "object", "properties": {"cmd": {"type": "string"}}, "required": ["cmd"]}},
			{"type": "namespace", "name": "multi_agent_v1", "description": "Tools for sub-agents.", "tools": [
				{"type": "function", "name": "spawn_agent", "description": "Spawn a sub-agent", "strict": false, "parameters": {"type": "object", "properties": {"message": {"type": "string"}}, "required": ["message"]}},
				{"type": "function", "name": "close_agent", "description": "Close an agent", "strict": false, "parameters": {"type": "object", "properties": {"target": {"type": "string"}}, "required": ["target"]}}
			]},
			{"type": "web_search", "external_web_access": false},
			{"type": "image_generation", "output_format": "png"}
		],
		"stream": true
	}`)

	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("TransformRequest 失败: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(result, &parsed)

	tools := parsed["tools"].([]any)
	// 应有: exec_command + multi_agent_v1__spawn_agent + multi_agent_v1__close_agent = 3
	// web_search 和 image_generation 应被跳过
	if len(tools) != 3 {
		t.Fatalf("应有 3 个 tools（1 function + 2 from namespace），实际 %d", len(tools))
	}

	// 第一个: exec_command (从 Responses API 扁平格式转换为 Chat Completions 嵌套格式)
	t0 := tools[0].(map[string]any)
	if t0["type"] != "function" {
		t.Errorf("tools[0].type 应为 function")
	}
	fn0 := t0["function"].(map[string]any)
	if fn0["name"] != "exec_command" {
		t.Errorf("tools[0].function.name 应为 exec_command，实际 %v", fn0["name"])
	}
	if fn0["description"] != "Run a command" {
		t.Errorf("tools[0].function.description 应为 'Run a command'")
	}

	// 第二个: multi_agent_v1__spawn_agent (从 namespace 展开)
	t1 := tools[1].(map[string]any)
	if t1["type"] != "function" {
		t.Errorf("tools[1].type 应为 function")
	}
	fn1 := t1["function"].(map[string]any)
	if fn1["name"] != "multi_agent_v1__spawn_agent" {
		t.Errorf("tools[1].function.name 应为 multi_agent_v1__spawn_agent，实际 %v", fn1["name"])
	}
	if fn1["description"] != "Spawn a sub-agent" {
		t.Errorf("tools[1].function.description 应为 'Spawn a sub-agent'")
	}
	params1 := fn1["parameters"].(map[string]any)
	if params1["type"] != "object" {
		t.Errorf("tools[1] parameters.type 应为 object")
	}

	// 第三个: multi_agent_v1__close_agent
	t2 := tools[2].(map[string]any)
	fn2 := t2["function"].(map[string]any)
	if fn2["name"] != "multi_agent_v1__close_agent" {
		t.Errorf("tools[2].function.name 应为 multi_agent_v1__close_agent，实际 %v", fn2["name"])
	}
}

func TestTransformCodexRequest_EmptyNamespace(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/responses", "real-model", "")

	body := []byte(`{
		"model": "gpt-4",
		"input": "hello",
		"tools": [
			{"type": "namespace", "name": "empty_ns", "description": "Empty", "tools": []}
		],
		"stream": true
	}`)

	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("TransformRequest 失败: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(result, &parsed)

	// 空 namespace 展开后没有 function tools，tools 字段可能为 null 或空数组
	if tools, ok := parsed["tools"].([]any); ok && len(tools) != 0 {
		t.Errorf("空 namespace 不应产生 tools，实际 %d", len(tools))
	}
}

func TestTransformCodexRequest_OnlyBuiltinTools(t *testing.T) {
	tr := &Transformer{}
	ctx := makeCtx("/v1/responses", "real-model", "")

	body := []byte(`{
		"model": "gpt-4",
		"input": "hello",
		"tools": [
			{"type": "web_search"},
			{"type": "image_generation", "output_format": "png"}
		],
		"stream": true
	}`)

	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("TransformRequest 失败: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(result, &parsed)

	// 内置工具应被跳过，tools 字段可能为 null 或空数组
	if tools, ok := parsed["tools"].([]any); ok && len(tools) != 0 {
		t.Errorf("内置工具应被跳过，实际 %d 个 tools", len(tools))
	}
}

// === 延迟 usage 场景测试 ===

func TestTransformToCodexStreamChunk_DelayedUsage(t *testing.T) {
	// 模拟真实场景：finish_reason 和 usage 在不同的 chunk 中
	// 这是很多 OpenAI 兼容 provider（如天翼云 GLM）的实际行为
	tr := &Transformer{}
	ctx, state := makeCodexCtxWithState("gpt-4")

	// 发送文本
	chunk1 := []byte(`{"choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`)
	tr.transformToCodexStreamChunk(ctx, chunk1, "gpt-4")

	// 发送 finish_reason（不带 usage）
	chunk2 := []byte(`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
	events2, _ := tr.transformToCodexStreamChunk(ctx, chunk2, "gpt-4")

	// 不应包含 response.completed（因为还没收到 usage）
	for _, e := range events2 {
		var evt map[string]any
		json.Unmarshal(e, &evt)
		if evt["type"] == "response.completed" {
			t.Fatal("finish 时没有 usage 不应立即发送 response.completed")
		}
	}

	// 验证状态标记
	if !state.Finished {
		t.Error("state.Finished 应为 true")
	}

	// 后续收到单独的 usage chunk（choices 为空）
	chunk3 := []byte(`{"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":50}}`)
	events3, _ := tr.transformToCodexStreamChunk(ctx, chunk3, "gpt-4")

	// 现在应该发送 response.completed
	if len(events3) != 1 {
		t.Fatalf("收到 usage 后应产生 1 个事件，实际 %d", len(events3))
	}
	var evt map[string]any
	json.Unmarshal(events3[0], &evt)
	if evt["type"] != "response.completed" {
		t.Errorf("事件应为 response.completed，实际 %v", evt["type"])
	}

	resp := evt["response"].(map[string]any)
	usage := resp["usage"].(map[string]any)
	if usage["input_tokens"].(float64) != 100 {
		t.Errorf("input_tokens 应为 100，实际 %v", usage["input_tokens"])
	}
	if usage["output_tokens"].(float64) != 50 {
		t.Errorf("output_tokens 应为 50，实际 %v", usage["output_tokens"])
	}
	if usage["total_tokens"].(float64) != 150 {
		t.Errorf("total_tokens 应为 150，实际 %v", usage["total_tokens"])
	}

	// Finished 应被重置
	if state.Finished {
		t.Error("发送 completed 后 state.Finished 应为 false")
	}
}

func TestTransformToCodexStreamChunk_DelayedUsageWithToolCalls(t *testing.T) {
	// 模拟 tool call 场景下 usage 延迟到达
	tr := &Transformer{}
	ctx, state := makeCodexCtxWithState("gpt-4")

	// tool call 开始
	chunk1 := []byte(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"bash","arguments":""}}]},"finish_reason":null}]}`)
	tr.transformToCodexStreamChunk(ctx, chunk1, "gpt-4")

	// tool call arguments
	chunk2 := []byte(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"cmd\":\"ls\"}"}}]},"finish_reason":null}]}`)
	tr.transformToCodexStreamChunk(ctx, chunk2, "gpt-4")

	// finish（不带 usage）
	chunk3 := []byte(`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`)
	events3, _ := tr.transformToCodexStreamChunk(ctx, chunk3, "gpt-4")

	// 不应包含 response.completed
	for _, e := range events3 {
		var evt map[string]any
		json.Unmarshal(e, &evt)
		if evt["type"] == "response.completed" {
			t.Fatal("finish 时没有 usage 不应立即发送 response.completed")
		}
	}
	if !state.Finished {
		t.Error("state.Finished 应为 true")
	}

	// 收到 usage
	chunk4 := []byte(`{"choices":[],"usage":{"prompt_tokens":200,"completion_tokens":30}}`)
	events4, _ := tr.transformToCodexStreamChunk(ctx, chunk4, "gpt-4")

	if len(events4) != 1 {
		t.Fatalf("收到 usage 后应产生 1 个事件，实际 %d", len(events4))
	}
	var evt map[string]any
	json.Unmarshal(events4[0], &evt)
	if evt["type"] != "response.completed" {
		t.Errorf("事件应为 response.completed，实际 %v", evt["type"])
	}

	resp := evt["response"].(map[string]any)
	usage := resp["usage"].(map[string]any)
	if usage["input_tokens"].(float64) != 200 {
		t.Errorf("input_tokens 应为 200，实际 %v", usage["input_tokens"])
	}
	if usage["output_tokens"].(float64) != 30 {
		t.Errorf("output_tokens 应为 30，实际 %v", usage["output_tokens"])
	}
}

func TestBuildCodexFinalCompletedEvent(t *testing.T) {
	// 测试 proxy 层兜底函数
	state := &CodexStreamState{
		ResponseID:      "resp_test",
		Model:           "gpt-4",
		AccumulatedText: "Hello",
		InputTokens:     50,
		OutputTokens:    25,
		SequenceNumber:  10,
		FinishStatus:    "completed",
	}

	event := BuildCodexFinalCompletedEvent(state)

	if event["type"] != "response.completed" {
		t.Errorf("type 应为 response.completed，实际 %v", event["type"])
	}

	resp := event["response"].(map[string]any)
	if resp["status"] != "completed" {
		t.Errorf("status 应为 completed，实际 %v", resp["status"])
	}

	usage := resp["usage"].(map[string]any)
	if usage["input_tokens"] != 50 {
		t.Errorf("input_tokens 应为 50，实际 %v", usage["input_tokens"])
	}
	if usage["output_tokens"] != 25 {
		t.Errorf("output_tokens 应为 25，实际 %v", usage["output_tokens"])
	}
	if usage["total_tokens"] != 75 {
		t.Errorf("total_tokens 应为 75，实际 %v", usage["total_tokens"])
	}

	output := resp["output"].([]map[string]any)
	if len(output) != 1 {
		t.Fatalf("output 应有 1 个 item，实际 %d", len(output))
	}
	if output[0]["type"] != "message" {
		t.Errorf("output[0].type 应为 message")
	}
}

// === Reasoning (reasoning_content -> reasoning_summary) 测试 ===

func TestTransformToCodexStreamChunk_ReasoningDelta(t *testing.T) {
	tr := &Transformer{}
	ctx, state := makeCodexCtxWithState("deepseek-r1")

	// 发送 reasoning_content
	chunk := []byte(`{"choices":[{"index":0,"delta":{"reasoning_content":"Let me think about this..."},"finish_reason":null}]}`)
	events, err := tr.transformToCodexStreamChunk(ctx, chunk, "deepseek-r1")
	if err != nil {
		t.Fatalf("不应返回错误: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("reasoning_content 应产生事件")
	}

	// 第一个事件应为 output_item.added (reasoning)
	var evt map[string]any
	json.Unmarshal(events[0], &evt)
	if evt["type"] != "response.output_item.added" {
		t.Errorf("第一个事件应为 response.output_item.added，实际 %v", evt["type"])
	}
	item := evt["item"].(map[string]any)
	if item["type"] != "reasoning" {
		t.Errorf("item.type 应为 reasoning，实际 %v", item["type"])
	}

	// 第二个事件应为 reasoning_summary_part.added
	json.Unmarshal(events[1], &evt)
	if evt["type"] != "response.reasoning_summary_part.added" {
		t.Errorf("第二个事件应为 response.reasoning_summary_part.added，实际 %v", evt["type"])
	}
	part := evt["part"].(map[string]any)
	if part["type"] != "summary_text" {
		t.Errorf("part.type 应为 summary_text，实际 %v", part["type"])
	}
	if part["text"] != "" {
		t.Errorf("part.text 初始应为空字符串")
	}

	// 第三个事件应为 reasoning_summary_text.delta
	json.Unmarshal(events[2], &evt)
	if evt["type"] != "response.reasoning_summary_text.delta" {
		t.Errorf("第三个事件应为 response.reasoning_summary_text.delta，实际 %v", evt["type"])
	}
	if evt["delta"] != "Let me think about this..." {
		t.Errorf("delta 应为 'Let me think about this...'，实际 %v", evt["delta"])
	}
	if int(evt["output_index"].(float64)) != 0 {
		t.Errorf("output_index 应为 0")
	}
	if int(evt["summary_index"].(float64)) != 0 {
		t.Errorf("summary_index 应为 0")
	}

	// 验证状态
	if !state.ReasoningStarted {
		t.Error("ReasoningStarted 应为 true")
	}
	if state.AccumulatedReasoning != "Let me think about this..." {
		t.Errorf("AccumulatedReasoning 不正确: %q", state.AccumulatedReasoning)
	}
}

func TestTransformToCodexStreamChunk_MultipleReasoningDeltas(t *testing.T) {
	tr := &Transformer{}
	ctx, state := makeCodexCtxWithState("deepseek-r1")

	// 第一个 reasoning chunk
	chunk1 := []byte(`{"choices":[{"index":0,"delta":{"reasoning_content":"Step 1: "},"finish_reason":null}]}`)
	events1, _ := tr.transformToCodexStreamChunk(ctx, chunk1, "deepseek-r1")
	if len(events1) != 3 {
		t.Fatalf("第一个 reasoning chunk 应产生 3 个事件（output_item_added + part_added + delta），实际 %d", len(events1))
	}

	// 第二个 reasoning chunk - 不应再发送 part_added
	chunk2 := []byte(`{"choices":[{"index":0,"delta":{"reasoning_content":"analyze the problem."},"finish_reason":null}]}`)
	events2, _ := tr.transformToCodexStreamChunk(ctx, chunk2, "deepseek-r1")
	if len(events2) != 1 {
		t.Fatalf("后续 reasoning chunk 应只产生 1 个 delta 事件，实际 %d", len(events2))
	}

	var evt map[string]any
	json.Unmarshal(events2[0], &evt)
	if evt["type"] != "response.reasoning_summary_text.delta" {
		t.Errorf("事件类型应为 response.reasoning_summary_text.delta，实际 %v", evt["type"])
	}
	if evt["delta"] != "analyze the problem." {
		t.Errorf("delta 不正确: %v", evt["delta"])
	}

	if state.AccumulatedReasoning != "Step 1: analyze the problem." {
		t.Errorf("AccumulatedReasoning 应为 'Step 1: analyze the problem.'，实际 %q", state.AccumulatedReasoning)
	}
}

func TestTransformToCodexStreamChunk_ReasoningThenText(t *testing.T) {
	tr := &Transformer{}
	ctx, state := makeCodexCtxWithState("deepseek-r1")

	// 先发送 reasoning
	chunk1 := []byte(`{"choices":[{"index":0,"delta":{"reasoning_content":"thinking..."},"finish_reason":null}]}`)
	tr.transformToCodexStreamChunk(ctx, chunk1, "deepseek-r1")

	if !state.ReasoningStarted {
		t.Fatal("reasoning 后 ReasoningStarted 应为 true")
	}

	// 然后发送 text content - 应先关闭 reasoning
	chunk2 := []byte(`{"choices":[{"index":0,"delta":{"content":"The answer is 42."},"finish_reason":null}]}`)
	events, _ := tr.transformToCodexStreamChunk(ctx, chunk2, "deepseek-r1")

	// 应包含: reasoning_summary_text.done, reasoning_summary_part.done, output_item.done(reasoning), output_item.added, content_part.added, text.delta
	if len(events) < 6 {
		t.Fatalf("reasoning->text 转换应产生至少 6 个事件，实际 %d", len(events))
	}

	// 验证事件顺序
	var evtTypes []string
	for _, e := range events {
		var evt map[string]any
		json.Unmarshal(e, &evt)
		evtTypes = append(evtTypes, evt["type"].(string))
	}

	expectedOrder := []string{
		"response.reasoning_summary_text.done",
		"response.reasoning_summary_part.done",
		"response.output_item.done",
		"response.output_item.added",
		"response.content_part.added",
		"response.output_text.delta",
	}
	for i, expected := range expectedOrder {
		if i >= len(evtTypes) {
			t.Fatalf("事件不足，缺少 %s", expected)
		}
		if evtTypes[i] != expected {
			t.Errorf("事件[%d] 应为 %s，实际 %s", i, expected, evtTypes[i])
		}
	}

	// 验证 output_item.done (reasoning) 的 item 类型
	var reasoningDoneEvt map[string]any
	json.Unmarshal(events[2], &reasoningDoneEvt)
	if item, ok := reasoningDoneEvt["item"].(map[string]any); ok {
		if item["type"] != "reasoning" {
			t.Errorf("output_item.done 的 item.type 应为 reasoning，实际 %s", item["type"])
		}
	} else {
		t.Error("output_item.done 应包含 item 字段")
	}

	// 验证 reasoning 状态已关闭
	if state.ReasoningStarted {
		t.Error("text 开始后 ReasoningStarted 应为 false")
	}
	// 验证 text 状态已开始
	if !state.OutputItemStarted {
		t.Error("OutputItemStarted 应为 true")
	}
	if !state.ContentPartStarted {
		t.Error("ContentPartStarted 应为 true")
	}
}

func TestTransformToCodexStreamChunk_ReasoningThenToolCall(t *testing.T) {
	tr := &Transformer{}
	ctx, state := makeCodexCtxWithState("deepseek-r1")

	// 先发送 reasoning
	chunk1 := []byte(`{"choices":[{"index":0,"delta":{"reasoning_content":"I need to run a command."},"finish_reason":null}]}`)
	tr.transformToCodexStreamChunk(ctx, chunk1, "deepseek-r1")

	// 然后发送 tool call - 应先关闭 reasoning
	chunk2 := []byte(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"bash","arguments":""}}]},"finish_reason":null}]}`)
	events, _ := tr.transformToCodexStreamChunk(ctx, chunk2, "deepseek-r1")

	// 应包含: reasoning_summary_text.done, reasoning_summary_part.done, output_item.done (reasoning), output_item.added (function_call)
	foundReasoningTextDone := false
	foundReasoningPartDone := false
	foundReasoningItemDone := false
	foundToolItemAdded := false
	for _, e := range events {
		var evt map[string]any
		json.Unmarshal(e, &evt)
		switch evt["type"] {
		case "response.reasoning_summary_text.done":
			foundReasoningTextDone = true
			if evt["text"] != "I need to run a command." {
				t.Errorf("reasoning text done 内容不正确: %v", evt["text"])
			}
		case "response.reasoning_summary_part.done":
			foundReasoningPartDone = true
			part := evt["part"].(map[string]any)
			if part["text"] != "I need to run a command." {
				t.Errorf("reasoning part done 内容不正确: %v", part["text"])
			}
		case "response.output_item.done":
			item, ok := evt["item"].(map[string]any)
			if ok && item["type"] == "reasoning" {
				foundReasoningItemDone = true
			}
		case "response.output_item.added":
			item := evt["item"].(map[string]any)
			if item["type"] == "function_call" {
				foundToolItemAdded = true
			}
		}
	}

	if !foundReasoningTextDone {
		t.Error("应包含 reasoning_summary_text.done 事件")
	}
	if !foundReasoningPartDone {
		t.Error("应包含 reasoning_summary_part.done 事件")
	}
	if !foundReasoningItemDone {
		t.Error("应包含 reasoning output_item.done 事件")
	}
	if !foundToolItemAdded {
		t.Error("应包含 function_call output_item.added 事件")
	}

	if state.ReasoningStarted {
		t.Error("tool call 后 ReasoningStarted 应为 false")
	}
}

func TestTransformToCodexStreamChunk_ReasoningThenFinish(t *testing.T) {
	tr := &Transformer{}
	ctx, _ := makeCodexCtxWithState("deepseek-r1")

	// 发送 reasoning
	chunk1 := []byte(`{"choices":[{"index":0,"delta":{"reasoning_content":"done thinking"},"finish_reason":null}]}`)
	tr.transformToCodexStreamChunk(ctx, chunk1, "deepseek-r1")

	// 发送 text
	chunk2 := []byte(`{"choices":[{"index":0,"delta":{"content":"result"},"finish_reason":null}]}`)
	tr.transformToCodexStreamChunk(ctx, chunk2, "deepseek-r1")

	// 发送 finish 带 usage
	chunk3 := []byte(`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":50}}`)
	events, _ := tr.transformToCodexStreamChunk(ctx, chunk3, "deepseek-r1")

	// 找到 response.completed 事件，验证 output 包含 reasoning
	for _, e := range events {
		var evt map[string]any
		json.Unmarshal(e, &evt)
		if evt["type"] == "response.completed" {
			resp := evt["response"].(map[string]any)
			output := resp["output"].([]any)
			if len(output) < 2 {
				t.Fatalf("output 应至少有 2 个 item（reasoning + message），实际 %d", len(output))
			}
			// 第一个应为 reasoning
			reasoning := output[0].(map[string]any)
			if reasoning["type"] != "reasoning" {
				t.Errorf("output[0].type 应为 reasoning，实际 %v", reasoning["type"])
			}
			summary := reasoning["summary"].([]any)
			summaryPart := summary[0].(map[string]any)
			if summaryPart["text"] != "done thinking" {
				t.Errorf("reasoning summary text 应为 'done thinking'，实际 %v", summaryPart["text"])
			}
			// 第二个应为 message
			msg := output[1].(map[string]any)
			if msg["type"] != "message" {
				t.Errorf("output[1].type 应为 message，实际 %v", msg["type"])
			}
			return
		}
	}
	t.Error("未找到 response.completed 事件")
}

func TestTransformToCodexStreamChunk_OnlyReasoningThenFinish(t *testing.T) {
	tr := &Transformer{}
	ctx, state := makeCodexCtxWithState("deepseek-r1")

	// 只发送 reasoning，没有 text content
	chunk1 := []byte(`{"choices":[{"index":0,"delta":{"reasoning_content":"just thinking"},"finish_reason":null}]}`)
	tr.transformToCodexStreamChunk(ctx, chunk1, "deepseek-r1")

	// 直接 finish 带 usage
	chunk2 := []byte(`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`)
	events, _ := tr.transformToCodexStreamChunk(ctx, chunk2, "deepseek-r1")

	// 应包含 reasoning 关闭事件和 completed
	foundReasoningTextDone := false
	foundReasoningPartDone := false
	foundReasoningItemDone := false
	foundCompleted := false
	for _, e := range events {
		var evt map[string]any
		json.Unmarshal(e, &evt)
		switch evt["type"] {
		case "response.reasoning_summary_text.done":
			foundReasoningTextDone = true
		case "response.reasoning_summary_part.done":
			foundReasoningPartDone = true
		case "response.output_item.done":
			item, ok := evt["item"].(map[string]any)
			if ok && item["type"] == "reasoning" {
				foundReasoningItemDone = true
			}
		case "response.completed":
			foundCompleted = true
			resp := evt["response"].(map[string]any)
			output := resp["output"].([]any)
			// 只有 reasoning，没有 message
			if len(output) != 1 {
				t.Errorf("output 应有 1 个 item（只有 reasoning），实际 %d", len(output))
			}
			if len(output) > 0 {
				item := output[0].(map[string]any)
				if item["type"] != "reasoning" {
					t.Errorf("output[0].type 应为 reasoning，实际 %v", item["type"])
				}
			}
		}
	}

	if !foundReasoningTextDone {
		t.Error("应包含 reasoning_summary_text.done 事件")
	}
	if !foundReasoningPartDone {
		t.Error("应包含 reasoning_summary_part.done 事件")
	}
	if !foundReasoningItemDone {
		t.Error("应包含 reasoning output_item.done 事件")
	}
	if !foundCompleted {
		t.Error("应包含 response.completed 事件")
	}
	if state.ReasoningStarted {
		t.Error("finish 后 ReasoningStarted 应为 false")
	}
}

func TestTransformToCodexStreamChunk_ReasoningSequenceNumbers(t *testing.T) {
	tr := &Transformer{}
	ctx, _ := makeCodexCtxWithState("deepseek-r1")

	// 发送 reasoning
	chunk1 := []byte(`{"choices":[{"index":0,"delta":{"reasoning_content":"think"},"finish_reason":null}]}`)
	events1, _ := tr.transformToCodexStreamChunk(ctx, chunk1, "deepseek-r1")

	// 发送 text
	chunk2 := []byte(`{"choices":[{"index":0,"delta":{"content":"answer"},"finish_reason":null}]}`)
	events2, _ := tr.transformToCodexStreamChunk(ctx, chunk2, "deepseek-r1")

	// 验证所有事件的序列号严格递增
	allEvents := append(events1, events2...)
	prevSeq := 0
	for i, e := range allEvents {
		var evt map[string]any
		json.Unmarshal(e, &evt)
		seq := int(evt["sequence_number"].(float64))
		if seq <= prevSeq {
			t.Errorf("事件[%d] 序列号应递增，前一个 %d，当前 %d (type=%v)", i, prevSeq, seq, evt["type"])
		}
		prevSeq = seq
	}
}

func TestTransformToCodexStreamChunk_ReasoningDelayedUsage(t *testing.T) {
	tr := &Transformer{}
	ctx, state := makeCodexCtxWithState("deepseek-r1")

	// 发送 reasoning + text
	chunk1 := []byte(`{"choices":[{"index":0,"delta":{"reasoning_content":"think"},"finish_reason":null}]}`)
	tr.transformToCodexStreamChunk(ctx, chunk1, "deepseek-r1")
	chunk2 := []byte(`{"choices":[{"index":0,"delta":{"content":"answer"},"finish_reason":null}]}`)
	tr.transformToCodexStreamChunk(ctx, chunk2, "deepseek-r1")

	// finish 不带 usage
	chunk3 := []byte(`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
	events3, _ := tr.transformToCodexStreamChunk(ctx, chunk3, "deepseek-r1")

	// 不应有 completed 事件
	for _, e := range events3 {
		var evt map[string]any
		json.Unmarshal(e, &evt)
		if evt["type"] == "response.completed" {
			t.Fatal("无 usage 时不应发送 response.completed")
		}
	}
	if !state.Finished {
		t.Error("state.Finished 应为 true")
	}

	// 后续收到 usage chunk
	usageChunk := []byte(`{"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":50}}`)
	events4, _ := tr.transformToCodexStreamChunk(ctx, usageChunk, "deepseek-r1")

	if len(events4) != 1 {
		t.Fatalf("收到 usage 后应产生 1 个事件，实际 %d", len(events4))
	}
	var evt map[string]any
	json.Unmarshal(events4[0], &evt)
	if evt["type"] != "response.completed" {
		t.Errorf("事件应为 response.completed，实际 %v", evt["type"])
	}

	// 验证 completed 事件包含 reasoning
	resp := evt["response"].(map[string]any)
	output := resp["output"].([]any)
	if len(output) < 2 {
		t.Fatalf("output 应至少有 2 个 item，实际 %d", len(output))
	}
	reasoning := output[0].(map[string]any)
	if reasoning["type"] != "reasoning" {
		t.Errorf("output[0] 应为 reasoning")
	}
}

func TestBuildCodexFinalCompletedEvent_WithReasoning(t *testing.T) {
	state := &CodexStreamState{
		ResponseID:           "resp_test",
		Model:                "deepseek-r1",
		AccumulatedText:      "The answer",
		AccumulatedReasoning: "I thought about it",
		InputTokens:          50,
		OutputTokens:         25,
		SequenceNumber:       10,
		FinishStatus:         "completed",
	}

	event := BuildCodexFinalCompletedEvent(state)

	resp := event["response"].(map[string]any)
	output := resp["output"].([]map[string]any)
	if len(output) != 2 {
		t.Fatalf("output 应有 2 个 item（reasoning + message），实际 %d", len(output))
	}

	// 第一个是 reasoning
	if output[0]["type"] != "reasoning" {
		t.Errorf("output[0].type 应为 reasoning，实际 %v", output[0]["type"])
	}
	summary := output[0]["summary"].([]map[string]any)
	if summary[0]["text"] != "I thought about it" {
		t.Errorf("reasoning summary text 不正确: %v", summary[0]["text"])
	}

	// 第二个是 message
	if output[1]["type"] != "message" {
		t.Errorf("output[1].type 应为 message，实际 %v", output[1]["type"])
	}
}

func TestBuildCodexFinalCompletedEvent_OnlyReasoning(t *testing.T) {
	state := &CodexStreamState{
		ResponseID:           "resp_test",
		Model:                "deepseek-r1",
		AccumulatedText:      "",
		AccumulatedReasoning: "just thinking",
		InputTokens:          10,
		OutputTokens:         5,
		SequenceNumber:       5,
		FinishStatus:         "completed",
	}

	event := BuildCodexFinalCompletedEvent(state)

	resp := event["response"].(map[string]any)
	output := resp["output"].([]map[string]any)
	if len(output) != 1 {
		t.Fatalf("output 应有 1 个 item（只有 reasoning），实际 %d", len(output))
	}
	if output[0]["type"] != "reasoning" {
		t.Errorf("output[0].type 应为 reasoning")
	}
}

func TestTransformToCodexStreamChunk_EmptyReasoningContent(t *testing.T) {
	tr := &Transformer{}
	ctx, state := makeCodexCtxWithState("deepseek-r1")

	// 空 reasoning_content 不应产生事件
	chunk := []byte(`{"choices":[{"index":0,"delta":{"reasoning_content":""},"finish_reason":null}]}`)
	events, err := tr.transformToCodexStreamChunk(ctx, chunk, "deepseek-r1")
	if err != nil {
		t.Fatalf("不应返回错误: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("空 reasoning_content 不应产生事件，实际 %d 个", len(events))
	}
	if state.ReasoningStarted {
		t.Error("空 reasoning_content 不应启动 reasoning")
	}
}

func TestTransformToCodexStreamChunk_ReasoningWithToolCallAndText(t *testing.T) {
	// 完整流程: reasoning -> text -> tool_call -> finish
	tr := &Transformer{}
	ctx, _ := makeCodexCtxWithState("deepseek-r1")

	// 1. reasoning
	chunk1 := []byte(`{"choices":[{"index":0,"delta":{"reasoning_content":"Let me analyze..."},"finish_reason":null}]}`)
	events1, _ := tr.transformToCodexStreamChunk(ctx, chunk1, "deepseek-r1")
	if len(events1) != 3 {
		t.Fatalf("reasoning 应产生 3 个事件（output_item_added + part_added + delta），实际 %d", len(events1))
	}

	// 2. more reasoning
	chunk2 := []byte(`{"choices":[{"index":0,"delta":{"reasoning_content":" I should run ls."},"finish_reason":null}]}`)
	events2, _ := tr.transformToCodexStreamChunk(ctx, chunk2, "deepseek-r1")
	if len(events2) != 1 {
		t.Fatalf("后续 reasoning 应产生 1 个事件，实际 %d", len(events2))
	}

	// 3. text content
	chunk3 := []byte(`{"choices":[{"index":0,"delta":{"content":"I'll run ls for you."},"finish_reason":null}]}`)
	events3, _ := tr.transformToCodexStreamChunk(ctx, chunk3, "deepseek-r1")
	// 应包含: reasoning_text_done, reasoning_part_done, output_item_done(reasoning), output_item_added, content_part_added, text_delta
	if len(events3) != 6 {
		t.Fatalf("reasoning->text 应产生 6 个事件，实际 %d", len(events3))
	}

	// 4. tool call
	chunk4 := []byte(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"bash","arguments":"{\"cmd\":\"ls\"}"}}]},"finish_reason":null}]}`)
	events4, _ := tr.transformToCodexStreamChunk(ctx, chunk4, "deepseek-r1")
	// 应包含: text_done, content_part_done, output_item_done, tool_item_added, args_delta
	foundTextDone := false
	foundToolAdded := false
	for _, e := range events4 {
		var evt map[string]any
		json.Unmarshal(e, &evt)
		if evt["type"] == "response.output_text.done" {
			foundTextDone = true
		}
		if evt["type"] == "response.output_item.added" {
			item := evt["item"].(map[string]any)
			if item["type"] == "function_call" {
				foundToolAdded = true
			}
		}
	}
	if !foundTextDone {
		t.Error("应包含 output_text.done")
	}
	if !foundToolAdded {
		t.Error("应包含 function_call output_item.added")
	}

	// 5. finish
	chunk5 := []byte(`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":50,"completion_tokens":30}}`)
	events5, _ := tr.transformToCodexStreamChunk(ctx, chunk5, "deepseek-r1")

	// 验证 completed 事件
	for _, e := range events5 {
		var evt map[string]any
		json.Unmarshal(e, &evt)
		if evt["type"] == "response.completed" {
			resp := evt["response"].(map[string]any)
			output := resp["output"].([]any)
			// 应有: reasoning, message, function_call
			if len(output) < 3 {
				t.Fatalf("output 应有 3 个 item，实际 %d", len(output))
			}
			types := make([]string, len(output))
			for i, o := range output {
				types[i] = o.(map[string]any)["type"].(string)
			}
			if types[0] != "reasoning" {
				t.Errorf("output[0] 应为 reasoning，实际 %v", types[0])
			}
			if types[1] != "message" {
				t.Errorf("output[1] 应为 message，实际 %v", types[1])
			}
			if types[2] != "function_call" {
				t.Errorf("output[2] 应为 function_call，实际 %v", types[2])
			}
			return
		}
	}
	t.Error("未找到 response.completed 事件")
}
