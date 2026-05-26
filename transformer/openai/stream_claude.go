package openai

import (
	"context"
	"encoding/json"

	"agr/transformer/tctx"
)

// transformToClaudeStreamChunk 将 OpenAI 流式 chunk 转换为 Anthropic SSE 事件
// 返回值可能是多个事件的 JSON 数组（用特殊标记分隔），由 proxy 层处理
func (t *Transformer) transformToClaudeStreamChunk(ctx context.Context, chunk []byte, clientModel string) ([]byte, error) {
	var data map[string]any
	if err := json.Unmarshal(chunk, &data); err != nil {
		return chunk, nil
	}

	// 提取 usage 信息（OpenAI 在最后一个 chunk 中返回 usage）
	if usage, ok := data["usage"].(map[string]any); ok {
		if state, _ := ctx.Value(tctx.StreamStateKey).(*tctx.StreamState); state != nil {
			if pt, ok := usage["prompt_tokens"].(float64); ok {
				state.InputTokens = int(pt)
			}
			if ct, ok := usage["completion_tokens"].(float64); ok {
				state.OutputTokens = int(ct)
			}
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

	delta, ok := choice["delta"].(map[string]any)
	if !ok {
		return nil, nil
	}

	// 处理 tool_calls delta
	if toolCallsRaw, ok := delta["tool_calls"].([]any); ok && len(toolCallsRaw) > 0 {
		return t.HandleToolCallDelta(ctx, toolCallsRaw, clientModel)
	}

	// 处理 DeepSeek reasoning_content，转换为 Anthropic thinking delta
	if reasoningContent, _ := delta["reasoning_content"].(string); reasoningContent != "" {
		return t.handleReasoningContentDelta(ctx, reasoningContent)
	}

	// 处理普通文本 content
	content, _ := delta["content"].(string)
	if content == "" {
		return nil, nil
	}

	event := map[string]any{
		"type":  "content_block_delta",
		"index": 0,
		"delta": map[string]any{
			"type": "text_delta",
			"text": content,
		},
	}

	if state, _ := ctx.Value(tctx.StreamStateKey).(*tctx.StreamState); state != nil {
		events := t.ensureTextBlockStarted(state)
		events = append(events, eventWithIndex(event, state.TextBlockIndex))
		return json.Marshal(events)
	}

	return json.Marshal(event)
}

func (t *Transformer) handleReasoningContentDelta(ctx context.Context, reasoningContent string) ([]byte, error) {
	event := map[string]any{
		"type":  "content_block_delta",
		"index": 0,
		"delta": map[string]any{
			"type":     "thinking_delta",
			"thinking": reasoningContent,
		},
	}

	state, _ := ctx.Value(tctx.StreamStateKey).(*tctx.StreamState)
	if state == nil {
		return json.Marshal(event)
	}

	events := t.ensureThinkingBlockStarted(state)
	events = append(events, eventWithIndex(event, state.ThinkingBlockIndex))
	return json.Marshal(events)
}

func (t *Transformer) ensureTextBlockStarted(state *tctx.StreamState) []map[string]any {
	var events []map[string]any
	events = append(events, stopOpenThinkingBlock(state)...)
	if state.TextBlockStarted {
		return events
	}

	state.TextBlockIndex = allocateContentBlock(state)
	state.TextBlockStarted = true
	events = append(events, map[string]any{
		"type":          "content_block_start",
		"index":         state.TextBlockIndex,
		"content_block": map[string]any{"type": "text", "text": ""},
	})
	return events
}

func (t *Transformer) ensureThinkingBlockStarted(state *tctx.StreamState) []map[string]any {
	var events []map[string]any
	events = append(events, stopOpenTextBlock(state)...)
	if state.ThinkingBlockStarted {
		return events
	}

	state.ThinkingBlockIndex = allocateContentBlock(state)
	state.ThinkingBlockStarted = true
	events = append(events, map[string]any{
		"type":  "content_block_start",
		"index": state.ThinkingBlockIndex,
		"content_block": map[string]any{
			"type":     "thinking",
			"thinking": "",
		},
	})
	return events
}

// HandleToolCallDelta 处理流式 tool_calls delta，组装为 Anthropic tool_use 事件
func (t *Transformer) HandleToolCallDelta(ctx context.Context, toolCallsRaw []any, clientModel string) ([]byte, error) {
	state, _ := ctx.Value(tctx.StreamStateKey).(*tctx.StreamState)

	var events []map[string]any
	if state != nil {
		events = append(events, stopOpenThinkingBlock(state)...)
		events = append(events, stopOpenTextBlock(state)...)
	}

	for _, tcRaw := range toolCallsRaw {
		tc, ok := tcRaw.(map[string]any)
		if !ok {
			continue
		}

		idx := 0
		if idxF, ok := tc["index"].(float64); ok {
			idx = int(idxF)
		}

		// 获取 function 信息
		fn, _ := tc["function"].(map[string]any)
		fnName, _ := fn["name"].(string)
		fnArgs, _ := fn["arguments"].(string)
		tcID, _ := tc["id"].(string)

		// 如果有 id 和 name，说明是新的 tool call 开始
		if tcID != "" && fnName != "" {
			// 计算 block index
			blockIdx := idx + 1
			if state != nil {
				blockIdx = allocateContentBlock(state)
			}

			// 发送 content_block_start 事件
			startEvent := map[string]any{
				"type":  "content_block_start",
				"index": blockIdx,
				"content_block": map[string]any{
					"type":  "tool_use",
					"id":    tcID,
					"name":  fnName,
					"input": map[string]any{},
				},
			}
			events = append(events, startEvent)

			// 如果同时带了 arguments，发送 delta
			if fnArgs != "" {
				deltaEvent := map[string]any{
					"type":  "content_block_delta",
					"index": blockIdx,
					"delta": map[string]any{
						"type":         "input_json_delta",
						"partial_json": fnArgs,
					},
				}
				events = append(events, deltaEvent)
			}
		} else if fnArgs != "" {
			// 只有 arguments 增量
			blockIdx := idx + 1
			if state != nil {
				blockIdx = state.BlockIndex
			}

			deltaEvent := map[string]any{
				"type":  "content_block_delta",
				"index": blockIdx,
				"delta": map[string]any{
					"type":         "input_json_delta",
					"partial_json": fnArgs,
				},
			}
			events = append(events, deltaEvent)
		}
	}

	if len(events) == 0 {
		return nil, nil
	}

	// 将多个事件编码为 JSON 数组，由 proxy 层拆分输出
	return json.Marshal(events)
}
