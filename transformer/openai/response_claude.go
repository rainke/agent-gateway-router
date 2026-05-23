package openai

import (
	"encoding/json"
	"fmt"
)

// transformToClaudeResponse 将 OpenAI 非流式响应转换为 Anthropic Messages 格式
func (t *Transformer) transformToClaudeResponse(body []byte, clientModel string) ([]byte, error) {
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
