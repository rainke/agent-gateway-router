package transformer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// OpenAIToCustomTransformer 将不同客户端协议转换为上游 Provider 可接受的格式
type OpenAIToCustomTransformer struct{}

// contextKey 自定义 context key 类型
type contextKey string

const (
	// RequestPathKey 请求路径 context key
	RequestPathKey contextKey = "request_path"
	// UpstreamModelKey 上游真实模型名 context key
	UpstreamModelKey contextKey = "upstream_model"
	// ClientModelKey 客户端请求的模型名 context key
	ClientModelKey contextKey = "client_model"
	// StreamStateKey 流式状态 context key
	StreamStateKey contextKey = "stream_state"
)

// StreamState 流式响应状态，用于跨 chunk 追踪 tool_calls 组装
type StreamState struct {
	// 当前正在组装的 tool calls
	ToolCalls []toolCallAccumulator
	// 当前 content block 索引（text 占 0，tool_use 从 1 开始）
	BlockIndex int
	// 是否已经输出过 text content
	HasTextContent bool
}

type toolCallAccumulator struct {
	Index    int
	ID       string
	Name     string
	Args     string
	Complete bool
}

func (t *OpenAIToCustomTransformer) TransformRequest(ctx context.Context, clientBody []byte) ([]byte, error) {
	path, _ := ctx.Value(RequestPathKey).(string)
	upstreamModel, _ := ctx.Value(UpstreamModelKey).(string)

	switch {
	case strings.Contains(path, "/v1/messages"):
		return t.transformClaudeRequest(clientBody, upstreamModel)
	case strings.Contains(path, "/v1/responses"):
		return t.transformCodexRequest(clientBody, upstreamModel)
	default:
		return clientBody, nil
	}
}

func (t *OpenAIToCustomTransformer) TransformResponse(ctx context.Context, body []byte) ([]byte, error) {
	path, _ := ctx.Value(RequestPathKey).(string)
	clientModel, _ := ctx.Value(ClientModelKey).(string)

	switch {
	case strings.Contains(path, "/v1/messages"):
		return t.transformToClaudeResponse(body, clientModel)
	case strings.Contains(path, "/v1/responses"):
		return t.transformToCodexResponse(body, clientModel)
	default:
		return body, nil
	}
}

func (t *OpenAIToCustomTransformer) TransformStream(ctx context.Context, chunk []byte) ([]byte, error) {
	path, _ := ctx.Value(RequestPathKey).(string)
	clientModel, _ := ctx.Value(ClientModelKey).(string)

	switch {
	case strings.Contains(path, "/v1/messages"):
		return t.transformToClaudeStreamChunk(ctx, chunk, clientModel)
	case strings.Contains(path, "/v1/responses"):
		return chunk, nil
	default:
		return chunk, nil
	}
}

// transformToClaudeResponse 将 OpenAI 非流式响应转换为 Anthropic Messages 格式
func (t *OpenAIToCustomTransformer) transformToClaudeResponse(body []byte, clientModel string) ([]byte, error) {
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		return body, nil
	}

	var contentBlocks []map[string]any
	stopReason := "end_turn"

	if choices, ok := resp["choices"].([]any); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]any); ok {
			if msg, ok := choice["message"].(map[string]any); ok {
				// 提取文本内容
				if content, ok := msg["content"].(string); ok && content != "" {
					contentBlocks = append(contentBlocks, map[string]any{
						"type": "text",
						"text": content,
					})
				}

				// 提取 tool_calls
				if toolCalls, ok := msg["tool_calls"].([]any); ok && len(toolCalls) > 0 {
					stopReason = "tool_use"
					for _, tc := range toolCalls {
						tcMap, ok := tc.(map[string]any)
						if !ok {
							continue
						}
						fn, _ := tcMap["function"].(map[string]any)
						fnName, _ := fn["name"].(string)
						fnArgs, _ := fn["arguments"].(string)
						tcID, _ := tcMap["id"].(string)

						// 解析 arguments JSON
						var argsObj any
						if err := json.Unmarshal([]byte(fnArgs), &argsObj); err != nil {
							argsObj = map[string]any{}
						}

						contentBlocks = append(contentBlocks, map[string]any{
							"type":  "tool_use",
							"id":    tcID,
							"name":  fnName,
							"input": argsObj,
						})
					}
				}
			}

			// 检查 finish_reason
			if fr, ok := choice["finish_reason"].(string); ok {
				switch fr {
				case "tool_calls", "function_call":
					stopReason = "tool_use"
				case "length":
					stopReason = "max_tokens"
				case "stop":
					if stopReason != "tool_use" {
						stopReason = "end_turn"
					}
				}
			}
		}
	}

	if len(contentBlocks) == 0 {
		contentBlocks = []map[string]any{{"type": "text", "text": ""}}
	}

	// 提取 usage
	inputTokens := 0
	outputTokens := 0
	if usage, ok := resp["usage"].(map[string]any); ok {
		if pt, ok := usage["prompt_tokens"].(float64); ok {
			inputTokens = int(pt)
		}
		if ct, ok := usage["completion_tokens"].(float64); ok {
			outputTokens = int(ct)
		}
	}

	anthropicResp := map[string]any{
		"id":            fmt.Sprintf("msg_%v", resp["id"]),
		"type":          "message",
		"role":          "assistant",
		"model":         clientModel,
		"content":       contentBlocks,
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":  inputTokens,
			"output_tokens": outputTokens,
		},
	}

	return json.Marshal(anthropicResp)
}

// transformToClaudeStreamChunk 将 OpenAI 流式 chunk 转换为 Anthropic SSE 事件
// 返回值可能是多个事件的 JSON 数组（用特殊标记分隔），由 proxy 层处理
func (t *OpenAIToCustomTransformer) transformToClaudeStreamChunk(ctx context.Context, chunk []byte, clientModel string) ([]byte, error) {
	var data map[string]any
	if err := json.Unmarshal(chunk, &data); err != nil {
		return chunk, nil
	}

	choices, ok := data["choices"].([]any)
	if !ok || len(choices) == 0 {
		return nil, nil
	}

	choice, ok := choices[0].(map[string]any)
	if !ok {
		return nil, nil
	}

	// 检查是否是结束 chunk
	finishReason, _ := choice["finish_reason"].(string)
	if finishReason == "stop" || finishReason == "tool_calls" || finishReason == "function_call" {
		return nil, nil
	}

	delta, ok := choice["delta"].(map[string]any)
	if !ok {
		return nil, nil
	}

	// 处理 tool_calls delta
	if toolCallsRaw, ok := delta["tool_calls"].([]any); ok && len(toolCallsRaw) > 0 {
		return t.handleToolCallDelta(ctx, toolCallsRaw, clientModel)
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

	return json.Marshal(event)
}

// handleToolCallDelta 处理流式 tool_calls delta，组装为 Anthropic tool_use 事件
func (t *OpenAIToCustomTransformer) handleToolCallDelta(ctx context.Context, toolCallsRaw []any, clientModel string) ([]byte, error) {
	state, _ := ctx.Value(StreamStateKey).(*StreamState)

	var events []map[string]any

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
			// 计算 block index（text 占 0，tool_use 从 1 开始）
			blockIdx := idx + 1
			if state != nil {
				blockIdx = state.BlockIndex + 1
				state.BlockIndex = blockIdx
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

// transformToCodexResponse 将 OpenAI Chat Completions 响应转换为 Responses API 格式
func (t *OpenAIToCustomTransformer) transformToCodexResponse(body []byte, clientModel string) ([]byte, error) {
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		return body, nil
	}

	content := ""
	if choices, ok := resp["choices"].([]any); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]any); ok {
			if msg, ok := choice["message"].(map[string]any); ok {
				if c, ok := msg["content"].(string); ok {
					content = c
				}
			}
		}
	}

	responsesResp := map[string]any{
		"id":     fmt.Sprintf("resp_%v", resp["id"]),
		"object": "response",
		"model":  clientModel,
		"output": []map[string]any{
			{
				"type":    "message",
				"role":    "assistant",
				"content": []map[string]any{{"type": "output_text", "text": content}},
			},
		},
		"status": "completed",
	}

	return json.Marshal(responsesResp)
}

// transformClaudeRequest 将 Claude/Anthropic 风格请求转换为 OpenAI Chat Completions 格式
func (t *OpenAIToCustomTransformer) transformClaudeRequest(body []byte, upstreamModel string) ([]byte, error) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("解析 Claude 请求失败: %w", err)
	}

	if upstreamModel != "" {
		req["model"] = upstreamModel
	}

	transformed := map[string]any{
		"model": req["model"],
	}

	// 转换 messages
	if messages, ok := req["messages"].([]any); ok {
		convertedMsgs := t.convertClaudeMessages(messages)
		transformed["messages"] = convertedMsgs
	}

	// 转换 system 消息
	if system, ok := req["system"]; ok {
		var systemContent string
		switch s := system.(type) {
		case string:
			systemContent = s
		case []any:
			var parts []string
			for _, part := range s {
				if p, ok := part.(map[string]any); ok {
					if text, ok := p["text"].(string); ok {
						parts = append(parts, text)
					}
				}
			}
			systemContent = strings.Join(parts, "\n")
		}
		if systemContent != "" {
			msgs, _ := transformed["messages"].([]any)
			systemMsg := map[string]any{
				"role":    "system",
				"content": systemContent,
			}
			allMsgs := make([]any, 0, len(msgs)+1)
			allMsgs = append(allMsgs, systemMsg)
			allMsgs = append(allMsgs, msgs...)
			transformed["messages"] = allMsgs
		}
	}

	// 转换 tools（Anthropic 格式 -> OpenAI 格式）
	if tools, ok := req["tools"].([]any); ok && len(tools) > 0 {
		transformed["tools"] = t.convertClaudeTools(tools)
	}

	// 转换 tool_choice
	if toolChoice, ok := req["tool_choice"]; ok {
		transformed["tool_choice"] = t.convertClaudeToolChoice(toolChoice)
	}

	// 转换其他参数
	if maxTokens, ok := req["max_tokens"]; ok {
		transformed["max_tokens"] = maxTokens
	}
	if stream, ok := req["stream"]; ok {
		transformed["stream"] = stream
	}
	if temp, ok := req["temperature"]; ok {
		transformed["temperature"] = temp
	}
	if topP, ok := req["top_p"]; ok {
		transformed["top_p"] = topP
	}

	result, err := json.Marshal(transformed)
	if err != nil {
		return nil, fmt.Errorf("序列化转换后的请求失败: %w", err)
	}
	return result, nil
}

// convertClaudeMessages 转换 Claude 消息列表为 OpenAI 格式
func (t *OpenAIToCustomTransformer) convertClaudeMessages(messages []any) []any {
	var converted []any

	for _, msg := range messages {
		m, ok := msg.(map[string]any)
		if !ok {
			continue
		}

		role, _ := m["role"].(string)

		switch role {
		case "user":
			converted = append(converted, t.convertClaudeUserMessage(m))
		case "assistant":
			converted = append(converted, t.convertClaudeAssistantMessage(m)...)
		default:
			// 其他角色直接透传
			converted = append(converted, m)
		}
	}

	return converted
}

// convertClaudeUserMessage 转换 Claude user 消息
func (t *OpenAIToCustomTransformer) convertClaudeUserMessage(m map[string]any) any {
	content := m["content"]

	switch c := content.(type) {
	case string:
		return map[string]any{"role": "user", "content": c}
	case []any:
		// 检查是否包含 tool_result
		hasToolResult := false
		for _, part := range c {
			if p, ok := part.(map[string]any); ok {
				if p["type"] == "tool_result" {
					hasToolResult = true
					break
				}
			}
		}

		if hasToolResult {
			// 包含 tool_result，需要拆分为多条消息
			// 但 OpenAI 格式中 tool 消息是独立的，这里返回 tool 消息
			// 实际上需要在上层处理，这里先提取 tool_result
			var results []any
			var textParts []string

			for _, part := range c {
				p, ok := part.(map[string]any)
				if !ok {
					continue
				}
				switch p["type"] {
				case "tool_result":
					toolUseID, _ := p["tool_use_id"].(string)
					resultContent := extractToolResultContent(p["content"])
					results = append(results, map[string]any{
						"role":         "tool",
						"tool_call_id": toolUseID,
						"content":      resultContent,
					})
				case "text":
					if text, ok := p["text"].(string); ok {
						textParts = append(textParts, text)
					}
				}
			}

			// 如果只有 tool_result，返回第一个（多个会在外层处理）
			if len(textParts) == 0 && len(results) > 0 {
				if len(results) == 1 {
					return results[0]
				}
				// 多个 tool_result，返回数组标记
				return results
			}

			// 混合内容，先返回 text
			if len(textParts) > 0 {
				return map[string]any{"role": "user", "content": strings.Join(textParts, "")}
			}
			return results
		}

		// 普通内容数组，提取文本
		var textParts []string
		for _, part := range c {
			if p, ok := part.(map[string]any); ok {
				if p["type"] == "text" {
					if text, ok := p["text"].(string); ok {
						textParts = append(textParts, text)
					}
				}
			}
		}
		return map[string]any{"role": "user", "content": strings.Join(textParts, "")}
	default:
		return map[string]any{"role": "user", "content": fmt.Sprintf("%v", c)}
	}
}

// convertClaudeAssistantMessage 转换 Claude assistant 消息（可能包含 tool_use）
func (t *OpenAIToCustomTransformer) convertClaudeAssistantMessage(m map[string]any) []any {
	content := m["content"]

	switch c := content.(type) {
	case string:
		return []any{map[string]any{"role": "assistant", "content": c}}
	case []any:
		// 检查是否包含 tool_use
		var textParts []string
		var toolCalls []map[string]any

		for _, part := range c {
			p, ok := part.(map[string]any)
			if !ok {
				continue
			}
			switch p["type"] {
			case "text":
				if text, ok := p["text"].(string); ok {
					textParts = append(textParts, text)
				}
			case "tool_use":
				id, _ := p["id"].(string)
				name, _ := p["name"].(string)
				input := p["input"]

				argsJSON, _ := json.Marshal(input)
				toolCalls = append(toolCalls, map[string]any{
					"id":   id,
					"type": "function",
					"function": map[string]any{
						"name":      name,
						"arguments": string(argsJSON),
					},
				})
			}
		}

		assistantMsg := map[string]any{
			"role": "assistant",
		}

		if len(textParts) > 0 {
			assistantMsg["content"] = strings.Join(textParts, "")
		} else {
			assistantMsg["content"] = nil
		}

		if len(toolCalls) > 0 {
			assistantMsg["tool_calls"] = toolCalls
		}

		return []any{assistantMsg}
	default:
		return []any{map[string]any{"role": "assistant", "content": fmt.Sprintf("%v", c)}}
	}
}

// convertClaudeTools 将 Anthropic tools 格式转换为 OpenAI tools 格式
func (t *OpenAIToCustomTransformer) convertClaudeTools(tools []any) []map[string]any {
	var openaiTools []map[string]any

	for _, tool := range tools {
		toolMap, ok := tool.(map[string]any)
		if !ok {
			continue
		}

		name, _ := toolMap["name"].(string)
		description, _ := toolMap["description"].(string)
		inputSchema := toolMap["input_schema"]

		openaiTool := map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        name,
				"description": description,
			},
		}

		if inputSchema != nil {
			openaiTool["function"].(map[string]any)["parameters"] = inputSchema
		}

		openaiTools = append(openaiTools, openaiTool)
	}

	return openaiTools
}

// convertClaudeToolChoice 转换 Anthropic tool_choice 为 OpenAI 格式
func (t *OpenAIToCustomTransformer) convertClaudeToolChoice(toolChoice any) any {
	switch tc := toolChoice.(type) {
	case map[string]any:
		tcType, _ := tc["type"].(string)
		switch tcType {
		case "auto":
			return "auto"
		case "any":
			return "required"
		case "tool":
			name, _ := tc["name"].(string)
			return map[string]any{
				"type":     "function",
				"function": map[string]any{"name": name},
			}
		default:
			return "auto"
		}
	case string:
		return tc
	default:
		return "auto"
	}
}

// extractToolResultContent 从 tool_result 的 content 中提取文本
func extractToolResultContent(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []any:
		var parts []string
		for _, part := range c {
			if p, ok := part.(map[string]any); ok {
				if p["type"] == "text" {
					if text, ok := p["text"].(string); ok {
						parts = append(parts, text)
					}
				}
			}
		}
		return strings.Join(parts, "")
	default:
		if c == nil {
			return ""
		}
		return fmt.Sprintf("%v", c)
	}
}

// transformCodexRequest 将 OpenAI Responses 风格请求转换为 OpenAI Chat Completions 格式
func (t *OpenAIToCustomTransformer) transformCodexRequest(body []byte, upstreamModel string) ([]byte, error) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("解析 Codex 请求失败: %w", err)
	}

	if upstreamModel != "" {
		req["model"] = upstreamModel
	}

	transformed := map[string]any{
		"model": req["model"],
	}

	if input, ok := req["input"]; ok {
		switch v := input.(type) {
		case string:
			transformed["messages"] = []map[string]any{
				{"role": "user", "content": v},
			}
		case []any:
			transformed["messages"] = v
		default:
			transformed["messages"] = []map[string]any{
				{"role": "user", "content": fmt.Sprintf("%v", v)},
			}
		}
	}

	if stream, ok := req["stream"]; ok {
		transformed["stream"] = stream
	}
	if maxTokens, ok := req["max_output_tokens"]; ok {
		transformed["max_tokens"] = maxTokens
	}
	if temp, ok := req["temperature"]; ok {
		transformed["temperature"] = temp
	}

	if instructions, ok := req["instructions"]; ok {
		msgs, _ := transformed["messages"].([]map[string]any)
		systemMsg := map[string]any{
			"role":    "system",
			"content": instructions,
		}
		allMsgs := make([]any, 0, len(msgs)+1)
		allMsgs = append(allMsgs, systemMsg)
		for _, m := range msgs {
			allMsgs = append(allMsgs, m)
		}
		transformed["messages"] = allMsgs
	}

	result, err := json.Marshal(transformed)
	if err != nil {
		return nil, fmt.Errorf("序列化转换后的请求失败: %w", err)
	}
	return result, nil
}

// nowISO 返回当前时间的 ISO 格式字符串
func nowISO() string {
	return time.Now().UTC().Format(time.RFC3339)
}
