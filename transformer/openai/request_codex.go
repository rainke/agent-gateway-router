package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// transformCodexRequest 将 OpenAI Responses 风格请求转换为 OpenAI Chat Completions 格式
func (t *Transformer) transformCodexRequest(ctx context.Context, body []byte, upstreamModel string) ([]byte, error) {
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

	// 转换 input 为 messages
	var messages []any
	if input, ok := req["input"]; ok {
		switch v := input.(type) {
		case string:
			messages = append(messages, map[string]any{
				"role": "user", "content": v,
			})
		case []any:
			messages = convertCodexInputToMessages(v)
		default:
			messages = append(messages, map[string]any{
				"role": "user", "content": fmt.Sprintf("%v", v),
			})
		}
	}

	// 处理 instructions -> system message（插入到最前面）
	if instructions, ok := req["instructions"]; ok {
		systemMsg := map[string]any{
			"role":    "system",
			"content": instructions,
		}
		messages = append([]any{systemMsg}, messages...)
	}

	transformed["messages"] = messages

	// 转发 stream 和 stream_options
	if stream, ok := req["stream"]; ok {
		transformed["stream"] = stream
		if streamBool, ok := stream.(bool); ok && streamBool {
			transformed["stream_options"] = map[string]any{
				"include_usage": true,
			}
		}
	}

	if maxTokens, ok := req["max_output_tokens"]; ok {
		transformed["max_tokens"] = maxTokens
	}
	if temp, ok := req["temperature"]; ok {
		transformed["temperature"] = temp
	}
	if topP, ok := req["top_p"]; ok {
		transformed["top_p"] = topP
	}

	// 转换 tools（Responses API 格式 -> Chat Completions 格式）
	if tools, ok := req["tools"].([]any); ok {
		converted := convertCodexTools(tools)
		if len(converted) > 0 {
			transformed["tools"] = converted
		}
	}
	// 转发 tool_choice
	if toolChoice, ok := req["tool_choice"]; ok {
		transformed["tool_choice"] = toolChoice
	}
	// 转发 parallel_tool_calls
	if ptc, ok := req["parallel_tool_calls"]; ok {
		transformed["parallel_tool_calls"] = ptc
	}

	// 转发 reasoning（effort / summary）
	if reasoning, ok := req["reasoning"].(map[string]any); ok {
		if effort, ok := reasoning["effort"].(string); ok {
			transformed["reasoning_effort"] = effort
		}
		// 请求中配置了 reasoning，标记为需要返回 reasoning
		markReasoningIncluded(ctx)
	}

	// 检查 include 字段是否包含 reasoning.encrypted_content
	if includes, ok := req["include"].([]any); ok {
		for _, inc := range includes {
			if s, ok := inc.(string); ok && strings.Contains(s, "reasoning") {
				markReasoningIncluded(ctx)
				break
			}
		}
	}

	if messagesContainReasoningContent(messages) {
		markReasoningIncluded(ctx)
	}

	result, err := json.Marshal(transformed)
	if err != nil {
		return nil, fmt.Errorf("序列化转换后的请求失败: %w", err)
	}
	return result, nil
}

// convertCodexInputToMessages 将 Responses API 的 input 数组转换为 Chat Completions messages
// 关键逻辑：连续的 function_call items 必须合并到同一个 assistant message 的 tool_calls 数组中，
// 否则 Chat Completions API 会报错 "tool_calls must be followed by tool messages"
func convertCodexInputToMessages(input []any) []any {
	var messages []any
	var pendingToolCalls []map[string]any
	var pendingReasoning string

	// flushPendingToolCalls 将累积的 tool_calls 合并为一个 assistant message
	flushPendingToolCalls := func() {
		if len(pendingToolCalls) == 0 {
			return
		}
		msg := map[string]any{
			"role":       "assistant",
			"tool_calls": pendingToolCalls,
		}
		if pendingReasoning != "" {
			msg["reasoning_content"] = pendingReasoning
			pendingReasoning = ""
		}
		messages = append(messages, msg)
		pendingToolCalls = nil
	}

	// flushPendingReasoning 将累积的 reasoning 附加到下一个 assistant message
	// 这个函数不直接 flush，reasoning 会被附加到紧随其后的 message 或 function_call
	// 如果 reasoning 后面没有跟随 assistant 内容，则单独生成一个空 assistant message
	flushPendingReasoningAsMessage := func() {
		if pendingReasoning == "" {
			return
		}
		messages = append(messages, map[string]any{
			"role":              "assistant",
			"content":           "",
			"reasoning_content": pendingReasoning,
		})
		pendingReasoning = ""
	}

	for _, item := range input {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}

		itemType, _ := m["type"].(string)

		switch itemType {
		case "message":
			flushPendingToolCalls()
			role, _ := m["role"].(string)
			if role == "assistant" {
				// assistant message 可以携带之前的 reasoning
				msg := convertCodexMessage(m)
				if msg != nil && pendingReasoning != "" {
					msg["reasoning_content"] = pendingReasoning
					pendingReasoning = ""
				}
				if msg != nil {
					messages = append(messages, msg)
				}
			} else {
				// 非 assistant 消息前，先 flush 残留的 reasoning
				flushPendingReasoningAsMessage()
				msg := convertCodexMessage(m)
				if msg != nil {
					messages = append(messages, msg)
				}
			}
		case "reasoning":
			// reasoning output item：提取 summary 文本，暂存等待附加到后续 assistant message
			flushPendingToolCalls()
			reasoning := extractReasoningSummaryText(m)
			if reasoning != "" {
				pendingReasoning = reasoning
			}
		case "function_call":
			// 累积 tool_call，不立即生成 message
			tc := buildToolCall(m)
			if tc != nil {
				pendingToolCalls = append(pendingToolCalls, tc)
			}
		case "function_call_output":
			// 遇到 output 前先 flush 累积的 tool_calls
			flushPendingToolCalls()
			msg := convertCodexFunctionCallOutput(m)
			if msg != nil {
				messages = append(messages, msg)
			}
		default:
			flushPendingToolCalls()
			flushPendingReasoningAsMessage()
			// 未知类型或已经是 Chat Completions 格式的消息，直接透传
			if _, hasRole := m["role"]; hasRole {
				messages = append(messages, m)
			}
		}
	}

	// 处理尾部残留的 function_call（理论上不应该出现没有 output 的情况，但防御性处理）
	flushPendingToolCalls()
	// 处理尾部残留的 reasoning
	flushPendingReasoningAsMessage()

	return messages
}

// extractReasoningSummaryText 从 reasoning output item 中提取 summary 文本
// reasoning item 格式: {"type":"reasoning","id":"rs_...","summary":[{"type":"summary_text","text":"..."}]}
func extractReasoningSummaryText(m map[string]any) string {
	summary, ok := m["summary"].([]any)
	if !ok {
		return ""
	}
	var parts []string
	for _, s := range summary {
		sp, ok := s.(map[string]any)
		if !ok {
			continue
		}
		if text, ok := sp["text"].(string); ok && text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func messagesContainReasoningContent(messages []any) bool {
	for _, msg := range messages {
		m, ok := msg.(map[string]any)
		if !ok {
			continue
		}
		if reasoning, ok := m["reasoning_content"].(string); ok && reasoning != "" {
			return true
		}
	}
	return false
}

// buildToolCall 从 Responses API function_call item 构建单个 tool_call 对象
func buildToolCall(m map[string]any) map[string]any {
	callID, _ := m["call_id"].(string)
	if callID == "" {
		callID, _ = m["id"].(string)
	}
	name, _ := m["name"].(string)
	arguments, _ := m["arguments"].(string)

	return map[string]any{
		"id":   callID,
		"type": "function",
		"function": map[string]any{
			"name":      name,
			"arguments": arguments,
		},
	}
}

// convertCodexMessage 将 Responses API message 转换为 Chat Completions message
func convertCodexMessage(m map[string]any) map[string]any {
	role, _ := m["role"].(string)
	if role == "" {
		return nil
	}

	// developer role 映射为 system
	if role == "developer" {
		role = "system"
	}

	content := m["content"]
	switch c := content.(type) {
	case string:
		return map[string]any{"role": role, "content": c}
	case []any:
		// 提取 content 数组中的文本
		text := extractCodexContentText(c)
		return map[string]any{"role": role, "content": text}
	default:
		if content == nil {
			return map[string]any{"role": role, "content": ""}
		}
		return map[string]any{"role": role, "content": fmt.Sprintf("%v", content)}
	}
}

// extractCodexContentText 从 Responses API content 数组中提取文本
func extractCodexContentText(content []any) string {
	var parts []string
	for _, part := range content {
		p, ok := part.(map[string]any)
		if !ok {
			continue
		}
		partType, _ := p["type"].(string)
		switch partType {
		case "input_text", "output_text":
			if text, ok := p["text"].(string); ok {
				parts = append(parts, text)
			}
		case "text":
			if text, ok := p["text"].(string); ok {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

// convertCodexFunctionCallOutput 将 Responses API function_call_output 转换为 tool message
func convertCodexFunctionCallOutput(m map[string]any) map[string]any {
	callID, _ := m["call_id"].(string)
	output, _ := m["output"].(string)

	return map[string]any{
		"role":         "tool",
		"tool_call_id": callID,
		"content":      output,
	}
}

// convertCodexTools 将 Responses API tools 转换为 Chat Completions tools 格式
// Responses API function tool: {"type":"function","name":"...","description":"...","parameters":{...},"strict":false}
// Chat Completions function tool: {"type":"function","function":{"name":"...","description":"...","parameters":{...},"strict":false}}
// Responses API 还支持 namespace、web_search、image_generation 等类型
func convertCodexTools(tools []any) []any {
	var result []any
	for _, tool := range tools {
		t, ok := tool.(map[string]any)
		if !ok {
			continue
		}
		toolType, _ := t["type"].(string)
		switch toolType {
		case "function":
			// Responses API function 格式转换为 Chat Completions function 格式
			converted := convertCodexFunctionTool(t, "")
			if converted != nil {
				result = append(result, converted)
			}
		case "namespace":
			// namespace 类型需要展开嵌套的 tools，并给 function name 加前缀
			flattened := flattenNamespaceTools(t)
			result = append(result, flattened...)
			// web_search, image_generation 等内置工具类型跳过
			// 这些是 OpenAI 平台特有的，第三方 provider 不支持
		}
	}
	return result
}

// convertCodexFunctionTool 将 Responses API 扁平 function tool 转换为 Chat Completions 嵌套格式
// Responses API: {"type":"function","name":"exec_command","description":"...","parameters":{...},"strict":false}
// Chat Completions: {"type":"function","function":{"name":"exec_command","description":"...","parameters":{...},"strict":false}}
func convertCodexFunctionTool(t map[string]any, namePrefix string) map[string]any {
	fnName, _ := t["name"].(string)
	if fnName == "" {
		return nil
	}

	if namePrefix != "" {
		fnName = namePrefix + "__" + fnName
	}

	fnDef := map[string]any{
		"name": fnName,
	}
	if desc, ok := t["description"].(string); ok {
		fnDef["description"] = desc
	}
	if params, ok := t["parameters"]; ok {
		fnDef["parameters"] = params
	}
	if strict, ok := t["strict"]; ok {
		fnDef["strict"] = strict
	}

	return map[string]any{
		"type":     "function",
		"function": fnDef,
	}
}

// flattenNamespaceTools 将 namespace 工具展开为多个独立的 function 工具
// namespace 格式: {"type":"namespace","name":"ns_name","description":"...","tools":[...]}
// 展开后每个工具的 function.name 变为 "ns_name__tool_name"
func flattenNamespaceTools(ns map[string]any) []any {
	nsName, _ := ns["name"].(string)
	nestedTools, _ := ns["tools"].([]any)

	var result []any
	for _, tool := range nestedTools {
		t, ok := tool.(map[string]any)
		if !ok {
			continue
		}
		toolType, _ := t["type"].(string)
		if toolType != "function" {
			continue
		}

		converted := convertCodexFunctionTool(t, nsName)
		if converted != nil {
			result = append(result, converted)
		}
	}
	return result
}
