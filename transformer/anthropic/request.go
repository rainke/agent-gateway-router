package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// transformCodexToMessagesRequest 将 Codex (OpenAI Responses API) 请求
// 转换为 Anthropic Messages API 请求。
//
// Codex 请求示例:
//
//	{ "model": "gpt-4", "input": "...", "instructions": "...",
//	  "tools": [...], "reasoning": {"effort":"high"} }
//
// Anthropic Messages 请求:
//
//	{ "model": "claude-...", "system": "...", "messages": [...],
//	  "tools": [...], "thinking": {...} }
func transformCodexToMessagesRequest(_ context.Context, body []byte, upstreamModel string) ([]byte, error) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		// 无效 JSON 直接透传，避免掩盖客户端问题
		return body, nil
	}

	out := map[string]any{
		"model": upstreamModel,
	}

	// 1) 转换 input + instructions -> messages / system
	// Anthropic 的 system 字段对应 Codex 的 instructions 字段和
	// input 数组中所有 role=developer 消息（合并为单个 system 字符串）。
	var systemParts []string
	if instructions, ok := req["instructions"].(string); ok && instructions != "" {
		systemParts = append(systemParts, instructions)
	}

	var messages []any
	if input, ok := req["input"]; ok {
		switch v := input.(type) {
		case string:
			messages = append(messages, map[string]any{
				"role":    "user",
				"content": v,
			})
		case []any:
			systemParts, messages = convertCodexInputToMessages(v, systemParts)
		default:
			messages = append(messages, map[string]any{
				"role":    "user",
				"content": fmt.Sprintf("%v", v),
			})
		}
	}
	if len(systemParts) > 0 {
		out["system"] = strings.Join(systemParts, "\n\n")
	}
	out["messages"] = messages

	// 2) tools: Responses API 扁平 -> Anthropic 扁平
	if tools, ok := req["tools"].([]any); ok && len(tools) > 0 {
		converted := convertCodexToolsToAnthropic(tools)
		if len(converted) > 0 {
			out["tools"] = converted
		}
	}

	// 3) tool_choice: Responses API -> Anthropic
	if tc, ok := req["tool_choice"]; ok {
		out["tool_choice"] = convertCodexToolChoiceToAnthropic(tc)
	}

	// 4) reasoning.effort -> thinking
	if reasoning, ok := req["reasoning"].(map[string]any); ok {
		if effort, ok := reasoning["effort"].(string); ok && effort != "" {
			// Codex 的 effort 与 Anthropic thinking 启用的映射：
			// "low" / "medium" / "high" / "xhigh" -> 启用 thinking
			// "none" / "" -> 不启用
			switch strings.ToLower(effort) {
			case "none", "":
				// 不添加 thinking
			default:
				out["thinking"] = map[string]any{
					"type":          "enabled",
					"budget_tokens": thinkingBudgetForEffort(effort),
				}
			}
		}
	}

	// 5) 通用参数
	if v, ok := req["max_output_tokens"]; ok {
		out["max_tokens"] = v
	}
	if v, ok := req["temperature"]; ok {
		out["temperature"] = v
	}
	if v, ok := req["top_p"]; ok {
		out["top_p"] = v
	}
	if v, ok := req["stream"]; ok {
		out["stream"] = v
	}
	if v, ok := req["stop"]; ok {
		out["stop_sequences"] = v
	}

	// 6) metadata（透传）
	if v, ok := req["metadata"]; ok {
		out["metadata"] = v
	}

	result, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("序列化 Anthropic 请求失败: %w", err)
	}
	return result, nil
}

func replaceMessagesRequestModel(body []byte, upstreamModel string) ([]byte, error) {
	if upstreamModel == "" {
		return body, nil
	}

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return body, nil
	}
	req["model"] = upstreamModel

	result, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("序列化 Anthropic Messages 请求失败: %w", err)
	}
	return result, nil
}

// thinkingBudgetForEffort 将 Codex reasoning effort 映射到 Anthropic thinking budget
func thinkingBudgetForEffort(effort string) int {
	switch strings.ToLower(effort) {
	case "low":
		return 1024
	case "medium":
		return 4096
	case "high":
		return 16384
	case "xhigh", "max":
		return 32768
	default:
		return 8192
	}
}

// convertCodexInputToMessages 将 Codex Responses API 的 input 数组转换为
// Anthropic Messages API 的 messages 数组。
//
// 关键规则：
//   - 连续的 function_call items 必须合并到同一个 assistant message 的
//     tool_use 数组中，function_call_output 必须紧跟 tool_result 消息。
//   - role=developer 消息会被合并到 systemParts，不会出现在 messages 中。
//
// 返回值：更新后的 systemParts（追加了 developer 内容）和 messages 数组。
func convertCodexInputToMessages(input []any, systemParts []string) ([]string, []any) {
	var messages []any
	var pendingToolUses []map[string]any

	flushToolUses := func() {
		if len(pendingToolUses) == 0 {
			return
		}
		// 构造 assistant message，content 数组里全是 tool_use
		content := make([]any, 0, len(pendingToolUses))
		for _, tu := range pendingToolUses {
			content = append(content, tu)
		}
		messages = append(messages, map[string]any{
			"role":    "assistant",
			"content": content,
		})
		pendingToolUses = nil
	}

	for _, item := range input {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}

		itemType, _ := m["type"].(string)

		switch itemType {
		case "message":
			flushToolUses()
			role, _ := m["role"].(string)
			if role == "" {
				role = "user"
			}
			if role == "developer" {
				// Codex developer 消息并入 Anthropic system 字段
				text := extractCodexMessageText(m)
				if text != "" {
					systemParts = append(systemParts, text)
				}
				continue
			}
			text := extractCodexMessageText(m)
			messages = append(messages, map[string]any{
				"role":    role,
				"content": text,
			})

		case "function_call":
			// 累积为下一个 assistant message 的 tool_use
			input := json.RawMessage(`{}`)
			if args, ok := m["arguments"].(string); ok && args != "" {
				input = json.RawMessage(args)
			}
			callID, _ := m["call_id"].(string)
			if callID == "" {
				callID, _ = m["id"].(string)
			}
			name, _ := m["name"].(string)
			tu := map[string]any{
				"type":  "tool_use",
				"id":    callID,
				"name":  name,
				"input": input,
			}
			pendingToolUses = append(pendingToolUses, tu)

		case "function_call_output":
			flushToolUses()
			callID, _ := m["call_id"].(string)
			output, _ := m["output"].(string)
			messages = append(messages, map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{
						"type":        "tool_result",
						"tool_use_id": callID,
						"content":     output,
					},
				},
			})

		case "reasoning":
			// reasoning 历史项忽略（Anthropic 通过 thinking 字段在请求时控制，不需要历史 reasoning）
			flushToolUses()

		default:
			flushToolUses()
			// 已经是 Messages 格式的消息（带 role 字段），直接透传
			if _, hasRole := m["role"]; hasRole {
				messages = append(messages, m)
			}
		}
	}

	flushToolUses()
	return systemParts, messages
}

// extractCodexMessageText 从 Codex message item 中提取文本
func extractCodexMessageText(m map[string]any) string {
	content := m["content"]
	switch c := content.(type) {
	case string:
		return c
	case []any:
		var parts []string
		for _, part := range c {
			p, ok := part.(map[string]any)
			if !ok {
				continue
			}
			ptype, _ := p["type"].(string)
			switch ptype {
			case "input_text", "output_text", "text":
				if text, ok := p["text"].(string); ok {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		if content == nil {
			return ""
		}
		return fmt.Sprintf("%v", content)
	}
}

// convertCodexToolsToAnthropic 将 Codex (Responses API) 工具定义转换为 Anthropic 格式
//
// Codex format:     {"type":"function","name":"...","description":"...","parameters":{...}}
// Anthropic format: {"name":"...","description":"...","input_schema":{...}}
func convertCodexToolsToAnthropic(tools []any) []any {
	var out []any
	for _, tool := range tools {
		t, ok := tool.(map[string]any)
		if !ok {
			continue
		}
		toolType, _ := t["type"].(string)
		if toolType != "function" {
			// namespace / web_search / image_generation 等 Anthropic 端无对应概念，跳过
			continue
		}

		name, _ := t["name"].(string)
		if name == "" {
			continue
		}
		converted := map[string]any{
			"name": name,
		}
		if desc, ok := t["description"].(string); ok {
			converted["description"] = desc
		}
		if params, ok := t["parameters"]; ok {
			converted["input_schema"] = params
		}
		out = append(out, converted)
	}
	return out
}

// convertCodexToolChoiceToAnthropic 将 Codex tool_choice 转换为 Anthropic 格式
//
// Codex: "auto" / "required" / "none" / {"type":"function","name":"..."}
// Anthropic: {"type":"auto"} / {"type":"any"} / {"type":"tool","name":"..."}
func convertCodexToolChoiceToAnthropic(tc any) any {
	switch v := tc.(type) {
	case string:
		switch v {
		case "auto":
			return map[string]any{"type": "auto"}
		case "required":
			return map[string]any{"type": "any"}
		case "none":
			return map[string]any{"type": "none"}
		default:
			return map[string]any{"type": "auto"}
		}
	case map[string]any:
		tcType, _ := v["type"].(string)
		switch tcType {
		case "function":
			name, _ := v["name"].(string)
			return map[string]any{"type": "tool", "name": name}
		case "auto":
			return map[string]any{"type": "auto"}
		case "required", "any":
			return map[string]any{"type": "any"}
		case "none":
			return map[string]any{"type": "none"}
		default:
			return map[string]any{"type": "auto"}
		}
	default:
		return map[string]any{"type": "auto"}
	}
}
