package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// CodexStreamState 跟踪 Codex (Responses API) 流式响应的状态
type CodexStreamState struct {
	ResponseID string
	Model      string
	// 当前 output item 索引
	OutputIndex int
	// 当前 content part 索引
	ContentIndex int
	// 累积的文本内容
	AccumulatedText string
	// 是否已发送 output_item.added
	OutputItemStarted bool
	// 是否已发送 content_part.added
	ContentPartStarted bool
	// Token 使用统计
	InputTokens  int
	OutputTokens int
	// 序列号
	SequenceNumber int
	// function call 相关
	FunctionCalls []CodexFunctionCall
}

// CodexFunctionCall 跟踪流式 function call 的状态
type CodexFunctionCall struct {
	CallID    string
	Name      string
	Arguments string
	Index     int
	Started   bool
}

// CodexStreamStateKey context key
const CodexStreamStateKey ContextKey = "codex_stream_state"

// transformToCodexStreamChunk 将 OpenAI Chat Completions 流式 chunk 转换为 Responses API SSE 事件
// 返回多个事件的 JSON 数组，由 proxy 层拆分为独立 SSE 事件输出
func (t *Transformer) transformToCodexStreamChunk(ctx context.Context, chunk []byte, clientModel string) ([][]byte, error) {
	var data map[string]any
	if err := json.Unmarshal(chunk, &data); err != nil {
		return nil, nil
	}

	state, _ := ctx.Value(CodexStreamStateKey).(*CodexStreamState)
	if state == nil {
		return nil, nil
	}

	var events [][]byte

	// 提取 usage 信息（OpenAI 在最后一个 chunk 中返回 usage）
	if usage, ok := data["usage"].(map[string]any); ok {
		if pt, ok := usage["prompt_tokens"].(float64); ok {
			state.InputTokens = int(pt)
		}
		if ct, ok := usage["completion_tokens"].(float64); ok {
			state.OutputTokens = int(ct)
		}
	}

	choices, ok := data["choices"].([]any)
	if !ok || len(choices) == 0 {
		return nil, nil
	}

	choice, ok := choices[0].(map[string]any)
	if !ok {
		return nil, nil
	}

	delta, _ := choice["delta"].(map[string]any)
	finishReason, _ := choice["finish_reason"].(string)

	// 处理 tool_calls delta
	if delta != nil {
		if toolCallsRaw, ok := delta["tool_calls"].([]any); ok && len(toolCallsRaw) > 0 {
			toolEvents := t.handleCodexToolCallDelta(state, toolCallsRaw)
			events = append(events, toolEvents...)
		}
	}

	// 处理普通文本 content delta
	if delta != nil {
		if content, _ := delta["content"].(string); content != "" {
			textEvents := t.handleCodexTextDelta(state, content)
			events = append(events, textEvents...)
		}
	}

	// 处理 finish_reason
	if finishReason != "" {
		finishEvents := t.handleCodexFinish(state, finishReason)
		events = append(events, finishEvents...)
	}

	return events, nil
}

// handleCodexTextDelta 处理文本增量，生成 Responses API 文本事件
func (t *Transformer) handleCodexTextDelta(state *CodexStreamState, content string) [][]byte {
	var events [][]byte

	// 确保 output_item 和 content_part 已经开始
	if !state.OutputItemStarted {
		events = append(events, t.codexOutputItemAdded(state))
		state.OutputItemStarted = true
	}
	if !state.ContentPartStarted {
		events = append(events, t.codexContentPartAdded(state))
		state.ContentPartStarted = true
	}

	// 发送 text delta
	state.AccumulatedText += content
	state.SequenceNumber++
	event := map[string]any{
		"type":            "response.output_text.delta",
		"sequence_number": state.SequenceNumber,
		"output_index":    state.OutputIndex,
		"content_index":   state.ContentIndex,
		"delta":           content,
	}
	events = append(events, mustMarshal(event))

	return events
}

// handleCodexToolCallDelta 处理 tool call 增量
func (t *Transformer) handleCodexToolCallDelta(state *CodexStreamState, toolCallsRaw []any) [][]byte {
	var events [][]byte

	for _, tcRaw := range toolCallsRaw {
		tc, ok := tcRaw.(map[string]any)
		if !ok {
			continue
		}

		idx := 0
		if idxF, ok := tc["index"].(float64); ok {
			idx = int(idxF)
		}

		fn, _ := tc["function"].(map[string]any)
		fnName, _ := fn["name"].(string)
		fnArgs, _ := fn["arguments"].(string)
		tcID, _ := tc["id"].(string)

		// 确保 FunctionCalls 切片足够大
		for len(state.FunctionCalls) <= idx {
			state.FunctionCalls = append(state.FunctionCalls, CodexFunctionCall{})
		}

		fc := &state.FunctionCalls[idx]

		// 新的 function call 开始
		if tcID != "" && fnName != "" {
			fc.CallID = tcID
			fc.Name = fnName
			fc.Index = idx

			// 先关闭之前的 text output item（如果有）
			if state.OutputItemStarted && state.ContentPartStarted {
				events = append(events, t.codexTextDone(state))
				events = append(events, t.codexContentPartDone(state))
				events = append(events, t.codexOutputItemDone(state))
				state.ContentPartStarted = false
				state.OutputItemStarted = false
			}

			// 发送 function_call output_item.added
			state.OutputIndex++
			fc.Started = true
			state.SequenceNumber++
			itemAdded := map[string]any{
				"type":            "response.output_item.added",
				"sequence_number": state.SequenceNumber,
				"output_index":    state.OutputIndex,
				"item": map[string]any{
					"type":      "function_call",
					"id":        tcID,
					"call_id":   tcID,
					"name":      fnName,
					"arguments": "",
					"status":    "in_progress",
				},
			}
			events = append(events, mustMarshal(itemAdded))
		}

		// function call arguments delta
		if fnArgs != "" {
			fc.Arguments += fnArgs
			state.SequenceNumber++
			argDelta := map[string]any{
				"type":            "response.function_call_arguments.delta",
				"sequence_number": state.SequenceNumber,
				"output_index":    state.OutputIndex,
				"delta":           fnArgs,
			}
			events = append(events, mustMarshal(argDelta))
		}
	}

	return events
}

// handleCodexFinish 处理流结束，生成完成事件
func (t *Transformer) handleCodexFinish(state *CodexStreamState, finishReason string) [][]byte {
	var events [][]byte

	// 关闭打开的 text content
	if state.ContentPartStarted {
		events = append(events, t.codexTextDone(state))
		events = append(events, t.codexContentPartDone(state))
		state.ContentPartStarted = false
	}
	if state.OutputItemStarted {
		events = append(events, t.codexOutputItemDone(state))
		state.OutputItemStarted = false
	}

	// 关闭打开的 function calls
	for i := range state.FunctionCalls {
		fc := &state.FunctionCalls[i]
		if fc.Started {
			state.SequenceNumber++
			argsDone := map[string]any{
				"type":            "response.function_call_arguments.done",
				"sequence_number": state.SequenceNumber,
				"output_index":    state.OutputIndex - (len(state.FunctionCalls) - 1 - i),
				"arguments":       fc.Arguments,
			}
			events = append(events, mustMarshal(argsDone))

			state.SequenceNumber++
			itemDone := map[string]any{
				"type":            "response.output_item.done",
				"sequence_number": state.SequenceNumber,
				"output_index":    state.OutputIndex - (len(state.FunctionCalls) - 1 - i),
				"item": map[string]any{
					"type":      "function_call",
					"id":        fc.CallID,
					"call_id":   fc.CallID,
					"name":      fc.Name,
					"arguments": fc.Arguments,
					"status":    "completed",
				},
			}
			events = append(events, mustMarshal(itemDone))
			fc.Started = false
		}
	}

	// 映射 finish_reason 到 Responses API status
	status := "completed"
	switch finishReason {
	case "length":
		status = "incomplete"
	case "content_filter":
		status = "failed"
	}

	// 发送 response.completed
	state.SequenceNumber++
	completed := t.buildCodexCompletedEvent(state, status)
	events = append(events, mustMarshal(completed))

	return events
}

// codexOutputItemAdded 生成 response.output_item.added 事件
func (t *Transformer) codexOutputItemAdded(state *CodexStreamState) []byte {
	state.SequenceNumber++
	event := map[string]any{
		"type":            "response.output_item.added",
		"sequence_number": state.SequenceNumber,
		"output_index":    state.OutputIndex,
		"item": map[string]any{
			"type":    "message",
			"id":      fmt.Sprintf("msg_%d", time.Now().UnixNano()),
			"role":    "assistant",
			"status":  "in_progress",
			"content": []any{},
		},
	}
	return mustMarshal(event)
}

// codexContentPartAdded 生成 response.content_part.added 事件
func (t *Transformer) codexContentPartAdded(state *CodexStreamState) []byte {
	state.SequenceNumber++
	event := map[string]any{
		"type":            "response.content_part.added",
		"sequence_number": state.SequenceNumber,
		"output_index":    state.OutputIndex,
		"content_index":   state.ContentIndex,
		"part": map[string]any{
			"type": "output_text",
			"text": "",
		},
	}
	return mustMarshal(event)
}

// codexTextDone 生成 response.output_text.done 事件
func (t *Transformer) codexTextDone(state *CodexStreamState) []byte {
	state.SequenceNumber++
	event := map[string]any{
		"type":            "response.output_text.done",
		"sequence_number": state.SequenceNumber,
		"output_index":    state.OutputIndex,
		"content_index":   state.ContentIndex,
		"text":            state.AccumulatedText,
	}
	return mustMarshal(event)
}

// codexContentPartDone 生成 response.content_part.done 事件
func (t *Transformer) codexContentPartDone(state *CodexStreamState) []byte {
	state.SequenceNumber++
	event := map[string]any{
		"type":            "response.content_part.done",
		"sequence_number": state.SequenceNumber,
		"output_index":    state.OutputIndex,
		"content_index":   state.ContentIndex,
		"part": map[string]any{
			"type": "output_text",
			"text": state.AccumulatedText,
		},
	}
	return mustMarshal(event)
}

// codexOutputItemDone 生成 response.output_item.done 事件
func (t *Transformer) codexOutputItemDone(state *CodexStreamState) []byte {
	state.SequenceNumber++
	event := map[string]any{
		"type":            "response.output_item.done",
		"sequence_number": state.SequenceNumber,
		"output_index":    state.OutputIndex,
		"item": map[string]any{
			"type":   "message",
			"role":   "assistant",
			"status": "completed",
			"content": []map[string]any{
				{"type": "output_text", "text": state.AccumulatedText},
			},
		},
	}
	return mustMarshal(event)
}

// buildCodexCompletedEvent 构建 response.completed 事件
func (t *Transformer) buildCodexCompletedEvent(state *CodexStreamState, status string) map[string]any {
	// 构建 output 数组
	var output []map[string]any

	// 如果有文本内容
	if state.AccumulatedText != "" {
		output = append(output, map[string]any{
			"type":   "message",
			"role":   "assistant",
			"status": "completed",
			"content": []map[string]any{
				{"type": "output_text", "text": state.AccumulatedText},
			},
		})
	}

	// 如果有 function calls
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
		"sequence_number": state.SequenceNumber,
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

// BuildCodexCreatedEvent 构建 response.created 事件
func BuildCodexCreatedEvent(state *CodexStreamState) map[string]any {
	state.SequenceNumber++
	return map[string]any{
		"type":            "response.created",
		"sequence_number": state.SequenceNumber,
		"response": map[string]any{
			"id":     state.ResponseID,
			"object": "response",
			"model":  state.Model,
			"output": []any{},
			"status": "in_progress",
		},
	}
}

// BuildCodexInProgressEvent 构建 response.in_progress 事件
func BuildCodexInProgressEvent(state *CodexStreamState) map[string]any {
	state.SequenceNumber++
	return map[string]any{
		"type":            "response.in_progress",
		"sequence_number": state.SequenceNumber,
		"response": map[string]any{
			"id":     state.ResponseID,
			"object": "response",
			"model":  state.Model,
			"output": []any{},
			"status": "in_progress",
		},
	}
}

func mustMarshal(v any) []byte {
	data, _ := json.Marshal(v)
	return data
}
