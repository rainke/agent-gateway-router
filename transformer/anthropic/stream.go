package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agr/transformer/tctx"
)

// streamChunk 转换 Anthropic SSE chunk 为 Codex SSE chunk 列表。
//
// 输入：完整的 SSE 帧（可能包含 event/data 多行），以 \n\n 分隔
// 输出：JSON 数组字符串，包含 0..N 个 Codex Responses API 事件
//
// 返回值约定：
//   - 如果没有事件要发送，返回 (nil, nil)
//   - 单个事件返回  JSON 对象
//   - 多个事件返回  JSON 数组（由 proxy 层拆分）
func (t *Transformer) streamChunk(ctx context.Context, chunk []byte) ([]byte, error) {
	events, err := t.convertSSEChunk(ctx, chunk)
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return nil, nil
	}
	if len(events) == 1 {
		return events[0], nil
	}
	// 多事件：序列化为 JSON 数组
	var rawArr []json.RawMessage
	for _, e := range events {
		rawArr = append(rawArr, e)
	}
	return json.Marshal(rawArr)
}

// convertSSEChunk 转换 Anthropic SSE chunk 为多个 Codex 事件（实现 CodexStreamTransformer 接口）。
//
// 接受两种输入格式：
//   - 完整 SSE 帧（event: + data: 行，以 \n\n 分隔）
//   - 纯 JSON payload（proxy 层在 data: 行后剥去前缀的 JSON 字符串）
//
// 输出：0..N 个 Codex Responses API 事件。
func (t *Transformer) convertSSEChunk(ctx context.Context, chunk []byte) ([][]byte, error) {
	trimmed := bytes.TrimSpace(chunk)
	if len(trimmed) == 0 {
		return nil, nil
	}

	// 0) 非 SSE/JSON 数据：原样透传（不依赖 state 存在）
	if !bytes.HasPrefix(trimmed, []byte("event:")) &&
		!bytes.HasPrefix(trimmed, []byte("data:")) &&
		trimmed[0] != '{' {
		return [][]byte{chunk}, nil
	}

	state, _ := ctx.Value(openaiStreamStateKey).(*StreamState)
	if state == nil {
		// 没有 state 说明上游没在流式，跳过
		return nil, nil
	}

	clientModel, _ := ctx.Value(tctx.ClientModelKey).(string)
	if clientModel == "" {
		clientModel = state.Model
	}

	// 1) 纯 JSON payload：proxy 在每行 data: 后传入的就是这种格式
	if trimmed[0] == '{' {
		ensureStreamStateMeta(state, clientModel)
		return handleAnthropicSSE(state, inferAnthropicEventType(trimmed), trimmed)
	}

	// 2) 完整 SSE 帧（含 event: + data: 行）
	frames := splitSSEFrames(trimmed)
	if len(frames) == 0 {
		return nil, nil
	}
	var allEvents [][]byte
	for _, frame := range frames {
		eventType, data := parseSSEFrame(frame)
		if data == nil {
			continue
		}
		ensureStreamStateMeta(state, clientModel)
		events, err := handleAnthropicSSE(state, eventType, data)
		if err != nil {
			return nil, err
		}
		allEvents = append(allEvents, events...)
	}
	return allEvents, nil
}

// inferAnthropicEventType 从纯 JSON payload 推断 Anthropic 事件类型。
// 当 proxy 逐行 data: 调用时，event 字段已被剥离，
// 只能从 JSON 的 "type" 字段推断。Anthropic 与 OpenAI/Codex 都使用 "type" 字段，
// 直接读取即可。
func inferAnthropicEventType(payload []byte) string {
	var p map[string]any
	if err := json.Unmarshal(payload, &p); err != nil {
		return ""
	}
	t, _ := p["type"].(string)
	return t
}

// splitSSEFrames 将完整的 SSE 数据按 \n\n 切分为多个 frame
func splitSSEFrames(data []byte) [][]byte {
	var frames [][]byte
	start := 0
	for i := 0; i < len(data); i++ {
		if i+1 < len(data) && data[i] == '\n' && data[i+1] == '\n' {
			frames = append(frames, data[start:i])
			start = i + 2
			i++
		}
	}
	if start < len(data) {
		// 收尾残帧
		frames = append(frames, data[start:])
	}
	return frames
}

// parseSSEFrame 解析单个 SSE 帧，返回 (eventType, dataJSON)
func parseSSEFrame(frame []byte) (string, []byte) {
	var eventType, data string
	lines := bytes.Split(frame, []byte("\n"))
	for _, line := range lines {
		line = bytes.TrimRight(line, "\r")
		if len(line) == 0 {
			continue
		}
		if bytes.HasPrefix(line, []byte("event:")) {
			eventType = strings.TrimSpace(string(line[6:]))
		} else if bytes.HasPrefix(line, []byte("data:")) {
			payload := strings.TrimSpace(string(line[5:]))
			if data == "" {
				data = payload
			} else {
				data += "\n" + payload
			}
		}
	}
	return eventType, []byte(data)
}

// ensureStreamStateMeta 确保 stream state 的 response id / model 已设置
func ensureStreamStateMeta(state *StreamState, clientModel string) {
	if state.ResponseID == "" {
		state.ResponseID = fmt.Sprintf("resp_%d", time.Now().UnixNano())
	}
	if state.Model == "" {
		state.Model = clientModel
	}
}

// handleAnthropicSSE 处理单个 Anthropic SSE 事件，返回 Codex 事件列表
func handleAnthropicSSE(state *StreamState, eventType string, data []byte) ([][]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		// 解析失败，跳过这一帧
		return nil, nil
	}

	switch eventType {
	case "message_start":
		return handleMessageStart(state, payload)
	case "content_block_start":
		return handleContentBlockStart(state, payload)
	case "content_block_delta":
		return handleContentBlockDelta(state, payload)
	case "content_block_stop":
		return handleContentBlockStop(state, payload)
	case "message_delta":
		return handleMessageDelta(state, payload)
	case "message_stop":
		return handleMessageStop(state, payload)
	case "ping":
		return nil, nil
	case "error":
		return nil, fmt.Errorf("anthropic 流式错误: %s", string(data))
	default:
		return nil, nil
	}
}

// ====================
// Event Handlers
// ====================

// handleMessageStart 处理 message_start 事件
//
// 注意：response.created 与 response.in_progress 由 proxy 层在调用本转换器之前
// 提前发送，本函数只提取上游提供的初始 usage，不再发出重复事件。
func handleMessageStart(state *StreamState, payload map[string]any) ([][]byte, error) {
	if state.Started {
		return nil, nil
	}
	state.Started = true

	// 提取 usage（Anthropic 在 message_start.message.usage 中给出 input_tokens）
	if msg, ok := payload["message"].(map[string]any); ok {
		if usage, ok := msg["usage"].(map[string]any); ok {
			if pt, ok := usage["input_tokens"].(float64); ok {
				state.InputTokens = int(pt)
			}
			if ct, ok := usage["output_tokens"].(float64); ok {
				state.OutputTokens = int(ct)
			}
			// message_start 中 output_tokens 通常为 1
		}
	}

	return nil, nil
}

// handleContentBlockStart 处理 content_block_start 事件
// 发出对应类型的 output_item.added（reasoning / function_call / message-text）
func handleContentBlockStart(state *StreamState, payload map[string]any) ([][]byte, error) {
	indexF, _ := payload["index"].(float64)
	index := int(indexF)

	block, _ := payload["content_block"].(map[string]any)
	if block == nil {
		return nil, nil
	}
	blockType, _ := block["type"].(string)

	switch blockType {
	case "text":
		// 文本消息的 start：记录 message item 索引，等 content_block_delta 时再发 output_item.added
		state.MessageItemIndex = state.OutputIndex
		state.MessageStarted = true
		return nil, nil

	case "thinking":
		// thinking block 开始：发出 reasoning output_item.added + summary part added
		state.ReasoningItemIndex = state.OutputIndex
		state.ReasoningSummaryIndex = 0
		state.ReasoningStarted = true
		state.OutputIndex++

		itemAdded := buildCodexReasoningOutputItemAdded(state)
		summaryAdded := buildCodexReasoningSummaryPartAdded(state)
		return [][]byte{itemAdded, summaryAdded}, nil

	case "tool_use":
		// tool_use 开始：发出 function_call output_item.added
		id, _ := block["id"].(string)
		name, _ := block["name"].(string)

		state.OutputIndex++
		fc := FunctionCall{
			CallID:    id,
			Name:      name,
			Index:     index,
			ItemIndex: state.OutputIndex,
			Started:   true,
		}
		state.FunctionCalls = append(state.FunctionCalls, fc)

		itemAdded := buildCodexFunctionCallOutputItemAdded(state, fc)
		return [][]byte{itemAdded}, nil
	}

	return nil, nil
}

// handleContentBlockDelta 处理 content_block_delta 事件
func handleContentBlockDelta(state *StreamState, payload map[string]any) ([][]byte, error) {
	delta, _ := payload["delta"].(map[string]any)
	if delta == nil {
		return nil, nil
	}
	deltaType, _ := delta["type"].(string)

	switch deltaType {
	case "text_delta":
		text, _ := delta["text"].(string)
		if text == "" {
			return nil, nil
		}
		// 首次文本：先发 message output_item.added + content_part.added
		var events [][]byte
		if !state.MessageStarted {
			state.MessageItemIndex = state.OutputIndex
			state.OutputIndex++
			state.MessageStarted = true
			events = append(events, buildCodexOutputItemAdded(state))
			events = append(events, buildCodexContentPartAdded(state))
		}
		state.AccumulatedText += text
		state.Seq++
		events = append(events, mustMarshal(map[string]any{
			"type":            "response.output_text.delta",
			"sequence_number": state.Seq,
			"output_index":    state.MessageItemIndex,
			"content_index":   0,
			"delta":           text,
		}))
		return events, nil

	case "thinking_delta":
		thinking, _ := delta["thinking"].(string)
		if thinking == "" {
			return nil, nil
		}
		state.AccumulatedReasoning += thinking
		state.Seq++
		event := mustMarshal(map[string]any{
			"type":            "response.reasoning_summary_text.delta",
			"sequence_number": state.Seq,
			"output_index":    state.ReasoningItemIndex,
			"summary_index":   state.ReasoningSummaryIndex,
			"delta":           thinking,
		})
		return [][]byte{event}, nil

	case "input_json_delta":
		partial, _ := delta["partial_json"].(string)
		if partial == "" {
			return nil, nil
		}
		// 找到对应的 function call
		indexF, _ := payload["index"].(float64)
		index := int(indexF)
		fc := findFunctionCallByIndex(state, index)
		if fc == nil {
			return nil, nil
		}
		fc.Arguments += partial
		state.Seq++
		event := mustMarshal(map[string]any{
			"type":            "response.function_call_arguments.delta",
			"sequence_number": state.Seq,
			"output_index":    fc.ItemIndex,
			"delta":           partial,
		})
		return [][]byte{event}, nil
	}

	return nil, nil
}

// handleContentBlockStop 处理 content_block_stop
func handleContentBlockStop(state *StreamState, payload map[string]any) ([][]byte, error) {
	indexF, _ := payload["index"].(float64)
	index := int(indexF)

	block, _ := payload["content_block"].(map[string]any)
	_ = block // 实际上 Anthropic 在 content_block_stop 中也带 content_block

	var events [][]byte

	// 文本 message 关闭
	if state.MessageStarted {
		// 区分 text block 和 thinking block：MessageItemIndex 跟踪文本
		// 简化处理：如果当前 content block index 与 MessageItemIndex 匹配则关闭
		// 但实际上 Anthropic 的 index 是 content block 索引，与我们的 output 索引不一一对应
		// 改用：在 content_block_delta (text) 时设置 state.MessageItemIndex = 当前 output index
		// 这里不能直接判定 index 归属，所以采用一种保守方式：
		// 关闭由 message_stop 统一处理，content_block_stop 只关闭 thinking / function_call
	}

	// 关闭 thinking summary
	if state.ReasoningStarted && index == 0 {
		// 简化：reasoning 通常是第一个 content block（index=0）
		events = append(events, buildCodexReasoningSummaryTextDone(state))
		events = append(events, buildCodexReasoningSummaryPartDone(state))
		events = append(events, buildCodexReasoningOutputItemDone(state))
		state.ReasoningStarted = false
		state.AccumulatedReasoning = ""
	}

	// 关闭 function call
	if fc := findFunctionCallByIndex(state, index); fc != nil && fc.Started {
		state.Seq++
		events = append(events, mustMarshal(map[string]any{
			"type":            "response.function_call_arguments.done",
			"sequence_number": state.Seq,
			"output_index":    fc.ItemIndex,
			"arguments":       fc.Arguments,
		}))
		state.Seq++
		events = append(events, mustMarshal(map[string]any{
			"type":            "response.output_item.done",
			"sequence_number": state.Seq,
			"output_index":    fc.ItemIndex,
			"item": map[string]any{
				"type":      "function_call",
				"id":        fc.CallID,
				"call_id":   fc.CallID,
				"name":      fc.Name,
				"arguments": fc.Arguments,
				"status":    "completed",
			},
		}))
		fc.Started = false
	}

	return events, nil
}

// handleMessageDelta 处理 message_delta 事件（stop_reason 与 usage）
// 当收到 stop_reason 时，立即发出 response.completed
func handleMessageDelta(state *StreamState, payload map[string]any) ([][]byte, error) {
	// 提取 usage
	if usage, ok := payload["usage"].(map[string]any); ok {
		if pt, ok := usage["input_tokens"].(float64); ok {
			state.InputTokens = int(pt)
		}
		if ct, ok := usage["output_tokens"].(float64); ok {
			state.OutputTokens = int(ct)
		}
	}

	// 提取 stop_reason
	delta, _ := payload["delta"].(map[string]any)
	if delta == nil {
		return nil, nil
	}
	sr, _ := delta["stop_reason"].(string)
	if sr == "" {
		return nil, nil
	}

	state.FinishStatus = mapAnthropicStopReasonToCodexStatus(sr, len(state.FunctionCalls) > 0)
	state.Finished = true

	return emitCodexCompleted(state), nil
}

// handleMessageStop 处理 message_stop 事件
// 关闭所有打开的 output item，发出 response.completed
func handleMessageStop(state *StreamState, _ map[string]any) ([][]byte, error) {
	status := state.FinishStatus
	if status == "" {
		status = "completed"
	}
	state.FinishStatus = status
	return emitCodexCompleted(state), nil
}

// emitCodexCompleted 关闭文本 message 并发出 response.completed。
// 幂等：如果已发送过则跳过。
func emitCodexCompleted(state *StreamState) [][]byte {
	if state.CompletedEmitted {
		return nil
	}
	state.CompletedEmitted = true

	var events [][]byte

	// 关闭文本 message
	if state.MessageStarted {
		state.Seq++
		events = append(events, mustMarshal(map[string]any{
			"type":            "response.output_text.done",
			"sequence_number": state.Seq,
			"output_index":    state.MessageItemIndex,
			"content_index":   0,
			"text":            state.AccumulatedText,
		}))
		state.Seq++
		events = append(events, mustMarshal(map[string]any{
			"type":            "response.content_part.done",
			"sequence_number": state.Seq,
			"output_index":    state.MessageItemIndex,
			"content_index":   0,
			"part": map[string]any{
				"type": "output_text",
				"text": state.AccumulatedText,
			},
		}))
		state.Seq++
		events = append(events, mustMarshal(map[string]any{
			"type":            "response.output_item.done",
			"sequence_number": state.Seq,
			"output_index":    state.MessageItemIndex,
			"item": map[string]any{
				"type":   "message",
				"id":     fmt.Sprintf("msg_%d", time.Now().UnixNano()),
				"role":   "assistant",
				"status": "completed",
				"content": []map[string]any{
					{"type": "output_text", "text": state.AccumulatedText},
				},
			},
		}))
		state.MessageStarted = false
	}

	// 发出 response.completed
	state.Seq++
	completed := buildCodexCompletedEvent(state, state.FinishStatus)
	events = append(events, mustMarshal(completed))
	state.Finished = false

	return events
}

// findFunctionCallByIndex 查找匹配的 function call
func findFunctionCallByIndex(state *StreamState, index int) *FunctionCall {
	for i := range state.FunctionCalls {
		if state.FunctionCalls[i].Index == index {
			return &state.FunctionCalls[i]
		}
	}
	return nil
}

// ====================
// Event Builders
// ====================

func buildCodexCreatedEvent(state *StreamState) []byte {
	state.Seq++
	return mustMarshal(map[string]any{
		"type":            "response.created",
		"sequence_number": state.Seq,
		"response": map[string]any{
			"id":     state.ResponseID,
			"object": "response",
			"model":  state.Model,
			"output": []any{},
			"status": "in_progress",
		},
	})
}

func buildCodexInProgressEvent(state *StreamState) []byte {
	state.Seq++
	return mustMarshal(map[string]any{
		"type":            "response.in_progress",
		"sequence_number": state.Seq,
		"response": map[string]any{
			"id":     state.ResponseID,
			"object": "response",
			"model":  state.Model,
			"output": []any{},
			"status": "in_progress",
		},
	})
}

func buildCodexOutputItemAdded(state *StreamState) []byte {
	state.Seq++
	return mustMarshal(map[string]any{
		"type":            "response.output_item.added",
		"sequence_number": state.Seq,
		"output_index":    state.MessageItemIndex,
		"item": map[string]any{
			"type":    "message",
			"id":      fmt.Sprintf("msg_%d", time.Now().UnixNano()),
			"role":    "assistant",
			"status":  "in_progress",
			"content": []any{},
		},
	})
}

func buildCodexContentPartAdded(state *StreamState) []byte {
	state.Seq++
	return mustMarshal(map[string]any{
		"type":            "response.content_part.added",
		"sequence_number": state.Seq,
		"output_index":    state.MessageItemIndex,
		"content_index":   0,
		"part": map[string]any{
			"type": "output_text",
			"text": "",
		},
	})
}

func buildCodexReasoningOutputItemAdded(state *StreamState) []byte {
	state.Seq++
	return mustMarshal(map[string]any{
		"type":            "response.output_item.added",
		"sequence_number": state.Seq,
		"output_index":    state.ReasoningItemIndex,
		"item": map[string]any{
			"type":    "reasoning",
			"id":      fmt.Sprintf("rs_%d", time.Now().UnixNano()),
			"status":  "in_progress",
			"summary": []any{},
		},
	})
}

func buildCodexReasoningSummaryPartAdded(state *StreamState) []byte {
	state.Seq++
	return mustMarshal(map[string]any{
		"type":            "response.reasoning_summary_part.added",
		"sequence_number": state.Seq,
		"output_index":    state.ReasoningItemIndex,
		"summary_index":   state.ReasoningSummaryIndex,
		"part": map[string]any{
			"type": "summary_text",
			"text": "",
		},
	})
}

func buildCodexReasoningSummaryTextDone(state *StreamState) []byte {
	state.Seq++
	return mustMarshal(map[string]any{
		"type":            "response.reasoning_summary_text.done",
		"sequence_number": state.Seq,
		"output_index":    state.ReasoningItemIndex,
		"summary_index":   state.ReasoningSummaryIndex,
		"text":            state.AccumulatedReasoning,
	})
}

func buildCodexReasoningSummaryPartDone(state *StreamState) []byte {
	state.Seq++
	return mustMarshal(map[string]any{
		"type":            "response.reasoning_summary_part.done",
		"sequence_number": state.Seq,
		"output_index":    state.ReasoningItemIndex,
		"summary_index":   state.ReasoningSummaryIndex,
		"part": map[string]any{
			"type": "summary_text",
			"text": state.AccumulatedReasoning,
		},
	})
}

func buildCodexReasoningOutputItemDone(state *StreamState) []byte {
	state.Seq++
	return mustMarshal(map[string]any{
		"type":            "response.output_item.done",
		"sequence_number": state.Seq,
		"output_index":    state.ReasoningItemIndex,
		"item": map[string]any{
			"type": "reasoning",
			"id":   fmt.Sprintf("rs_%d", time.Now().UnixNano()),
			"summary": []map[string]any{
				{"type": "summary_text", "text": state.AccumulatedReasoning},
			},
		},
	})
}

func buildCodexFunctionCallOutputItemAdded(state *StreamState, fc FunctionCall) []byte {
	state.Seq++
	return mustMarshal(map[string]any{
		"type":            "response.output_item.added",
		"sequence_number": state.Seq,
		"output_index":    fc.ItemIndex,
		"item": map[string]any{
			"type":      "function_call",
			"id":        fc.CallID,
			"call_id":   fc.CallID,
			"name":      fc.Name,
			"arguments": "",
			"status":    "in_progress",
		},
	})
}

func buildCodexCompletedEvent(state *StreamState, status string) map[string]any {
	// 构建 output 数组
	var output []map[string]any

	if state.AccumulatedReasoning != "" {
		output = append(output, map[string]any{
			"type": "reasoning",
			"id":   fmt.Sprintf("rs_%d", time.Now().UnixNano()),
			"summary": []map[string]any{
				{"type": "summary_text", "text": state.AccumulatedReasoning},
			},
		})
	}

	if state.MessageStarted || state.AccumulatedText != "" {
		output = append(output, map[string]any{
			"type":   "message",
			"id":     fmt.Sprintf("msg_%d", time.Now().UnixNano()),
			"role":   "assistant",
			"status": "completed",
			"content": []map[string]any{
				{"type": "output_text", "text": state.AccumulatedText},
			},
		})
	}

	for _, fc := range state.FunctionCalls {
		if fc.CallID != "" {
			output = append(output, map[string]any{
				"type":      "function_call",
				"id":        fc.CallID,
				"call_id":   fc.CallID,
				"name":      fc.Name,
				"arguments": fc.Arguments,
				"status":    "completed",
			})
		}
	}

	return map[string]any{
		"type":            "response.completed",
		"sequence_number": state.Seq,
		"response": map[string]any{
			"id":     state.ResponseID,
			"object": "response",
			"model":  state.Model,
			"output": output,
			"status": status,
			"usage": map[string]any{
				"input_tokens":  state.InputTokens,
				"output_tokens": state.OutputTokens,
				"total_tokens":  state.InputTokens + state.OutputTokens,
			},
		},
	}
}

func mustMarshal(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
