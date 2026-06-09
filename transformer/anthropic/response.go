package anthropic

import (
	"encoding/json"
	"fmt"
	"time"
)

// transformMessagesToCodexResponse 将 Anthropic Messages API 非流式响应
// 转换为 Codex (OpenAI Responses API) 响应格式。
//
// Anthropic Messages 响应:
//   { "id": "msg_1", "role": "assistant", "content": [
//       {"type": "text", "text": "..."},
//       {"type": "tool_use", "id": "toolu_1", "name": "...", "input": {...}}
//     ],
//     "stop_reason": "end_turn" | "tool_use" | "max_tokens" | ...,
//     "usage": {"input_tokens": N, "output_tokens": M}
//   }
//
// Codex 响应:
//   { "id": "resp_1", "object": "response", "model": "client-model",
//     "output": [
//       {"type": "message", "role": "assistant", "content": [
//         {"type": "output_text", "text": "..."}
//       ]},
//       {"type": "function_call", "id": "...", "call_id": "...", "name": "...", "arguments": "..."}
//     ],
//     "status": "completed" | "incomplete" | "failed",
//     "usage": {"input_tokens": N, "output_tokens": M, "total_tokens": ...}
//   }
func transformMessagesToCodexResponse(body []byte, clientModel string) ([]byte, error) {
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		// 无效 JSON 透传
		return body, nil
	}

	output := buildCodexOutputFromMessagesContent(resp["content"])
	status := mapAnthropicStopReasonToCodexStatus(stringField(resp, "stop_reason"), outputHasToolCalls(output))

	usage := buildCodexUsage(resp["usage"])

	codexResp := map[string]any{
		"id":     codexResponseID(stringField(resp, "id")),
		"object": "response",
		"model":  clientModel,
		"output": output,
		"status": status,
	}
	if usage != nil {
		codexResp["usage"] = usage
	}

	return json.Marshal(codexResp)
}

// buildCodexOutputFromMessagesContent 将 Anthropic content 数组转换为 Codex output 数组
func buildCodexOutputFromMessagesContent(content any) []map[string]any {
	var output []map[string]any
	arr, ok := content.([]any)
	if !ok {
		// 兜底：content 不是数组时返回空
		return output
	}

	// 文本内容：合并为单个 message output item
	var textParts []string
	hasText := false
	for _, part := range arr {
		p, ok := part.(map[string]any)
		if !ok {
			continue
		}
		ptype, _ := p["type"].(string)
		if ptype == "text" {
			if text, ok := p["text"].(string); ok {
				textParts = append(textParts, text)
				hasText = true
			}
		}
	}
	if hasText {
		// Anthropic 的 text block 可能只有一个，也可能有多个，统一合并
		var contentArr []map[string]any
		for _, t := range textParts {
			contentArr = append(contentArr, map[string]any{
				"type": "output_text",
				"text": t,
			})
		}
		output = append(output, map[string]any{
			"type":    "message",
			"id":      fmt.Sprintf("msg_%d", time.Now().UnixNano()),
			"role":    "assistant",
			"status":  "completed",
			"content": contentArr,
		})
	}

	// tool_use -> function_call
	for _, part := range arr {
		p, ok := part.(map[string]any)
		if !ok {
			continue
		}
		ptype, _ := p["type"].(string)
		if ptype != "tool_use" {
			continue
		}
		id, _ := p["id"].(string)
		name, _ := p["name"].(string)
		input := p["input"]

		var argsStr string
		switch v := input.(type) {
		case nil:
			argsStr = "{}"
		case string:
			if v == "" {
				argsStr = "{}"
			} else {
				argsStr = v
			}
		default:
			if b, err := json.Marshal(v); err == nil {
				argsStr = string(b)
			} else {
				argsStr = "{}"
			}
		}

		output = append(output, map[string]any{
			"type":      "function_call",
			"id":        id,
			"call_id":   id,
			"name":      name,
			"arguments": argsStr,
			"status":    "completed",
		})
	}

	return output
}

// mapAnthropicStopReasonToCodexStatus 将 Anthropic stop_reason 映射到 Codex status
func mapAnthropicStopReasonToCodexStatus(stopReason string, hasToolCalls bool) string {
	if hasToolCalls {
		// Anthropic 通常设置 stop_reason=tool_use，Codex 视为 completed（tool 已发出）
		// 但 Responses API 的 "completed" 表示全部生成完成；tool_use 仍需客户端再发请求
		// Codex 约定：含 function_call 的响应是 "incomplete"
		return "incomplete"
	}
	switch stopReason {
	case "end_turn", "stop_sequence":
		return "completed"
	case "max_tokens":
		return "incomplete"
	case "tool_use":
		// 没有 tool_use content（异常）— 视作 incomplete
		return "incomplete"
	default:
		return "completed"
	}
}

// buildCodexUsage 构造 Codex usage 字段
func buildCodexUsage(raw any) map[string]any {
	if raw == nil {
		return nil
	}
	u, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	input := intField(u, "input_tokens")
	output := intField(u, "output_tokens")
	return map[string]any{
		"input_tokens":  input,
		"output_tokens": output,
		"total_tokens":  input + output,
	}
}

func outputHasToolCalls(output []map[string]any) bool {
	for _, item := range output {
		if item["type"] == "function_call" {
			return true
		}
	}
	return false
}

func codexResponseID(anthropicID string) string {
	if anthropicID == "" {
		return fmt.Sprintf("resp_%d", time.Now().UnixNano())
	}
	// Anthropic 的 id 通常是 "msg_xxx"，Codex 期望 "resp_xxx" 形式
	// 如果不是 msg_ 前缀，直接加 resp_ 前缀
	if len(anthropicID) > 4 && anthropicID[:4] == "msg_" {
		return "resp_" + anthropicID[4:]
	}
	return "resp_" + anthropicID
}

func stringField(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func intField(m map[string]any, key string) int {
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	default:
		return 0
	}
}
