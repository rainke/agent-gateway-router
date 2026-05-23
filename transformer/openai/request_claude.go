package openai

import (
	"encoding/json"
	"fmt"
	"strings"
)

// transformClaudeRequest 将 Claude/Anthropic 风格请求转换为 OpenAI Chat Completions 格式
func (t *Transformer) transformClaudeRequest(body []byte, upstreamModel string) ([]byte, error) {
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
		transformed["tool_choice"] = t.ConvertClaudeToolChoice(toolChoice)
	}

	// 转换其他参数
	if maxTokens, ok := req["max_tokens"]; ok {
		transformed["max_tokens"] = maxTokens
	}
	if stream, ok := req["stream"]; ok {
		transformed["stream"] = stream
		// 如果是流式请求，添加 stream_options 以获取 usage 统计
		if streamBool, isBool := stream.(bool); isBool && streamBool {
			transformed["stream_options"] = map[string]any{
				"include_usage": true,
			}
		}
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
func (t *Transformer) convertClaudeMessages(messages []any) []any {
	var converted []any

	for _, msg := range messages {
		m, ok := msg.(map[string]any)
		if !ok {
			continue
		}

		role, _ := m["role"].(string)

		switch role {
		case "user":
			converted = append(converted, t.ConvertClaudeUserMessage(m)...)
		case "assistant":
			converted = append(converted, t.ConvertClaudeAssistantMessage(m)...)
		default:
			// 其他角色直接透传
			converted = append(converted, m)
		}
	}

	return converted
}

// ConvertClaudeUserMessage 转换 Claude user 消息，返回一条或多条 OpenAI 格式消息
func (t *Transformer) ConvertClaudeUserMessage(m map[string]any) []any {
	content := m["content"]

	switch c := content.(type) {
	case string:
		return []any{map[string]any{"role": "user", "content": c}}
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
			// 包含 tool_result，拆分为独立的 tool 消息
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
					resultContent := ExtractToolResultContent(p["content"])
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

			// 如果有文本内容，放在 tool 消息之前
			var msgs []any
			if len(textParts) > 0 {
				msgs = append(msgs, map[string]any{"role": "user", "content": strings.Join(textParts, "")})
			}
			msgs = append(msgs, results...)
			return msgs
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
		return []any{map[string]any{"role": "user", "content": strings.Join(textParts, "")}}
	default:
		return []any{map[string]any{"role": "user", "content": fmt.Sprintf("%v", c)}}
	}
}

// ConvertClaudeAssistantMessage 转换 Claude assistant 消息（可能包含 tool_use）
func (t *Transformer) ConvertClaudeAssistantMessage(m map[string]any) []any {
	content := m["content"]

	switch c := content.(type) {
	case string:
		return []any{map[string]any{"role": "assistant", "content": c}}
	case []any:
		// 检查是否包含 tool_use
		var textParts []string
		var thinkingParts []string
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
			case "thinking":
				if thinking, ok := p["thinking"].(string); ok {
					thinkingParts = append(thinkingParts, thinking)
				} else if text, ok := p["text"].(string); ok {
					thinkingParts = append(thinkingParts, text)
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
		if len(thinkingParts) > 0 {
			assistantMsg["reasoning_content"] = strings.Join(thinkingParts, "\n")
		}

		return []any{assistantMsg}
	default:
		return []any{map[string]any{"role": "assistant", "content": fmt.Sprintf("%v", c)}}
	}
}

// convertClaudeTools 将 Anthropic tools 格式转换为 OpenAI tools 格式
func (t *Transformer) convertClaudeTools(tools []any) []map[string]any {
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

// ConvertClaudeToolChoice 转换 Anthropic tool_choice 为 OpenAI 格式
func (t *Transformer) ConvertClaudeToolChoice(toolChoice any) any {
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
