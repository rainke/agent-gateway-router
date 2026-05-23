package openai

import (
	"encoding/json"
	"fmt"
)

// transformCodexRequest 将 OpenAI Responses 风格请求转换为 OpenAI Chat Completions 格式
func (t *Transformer) transformCodexRequest(body []byte, upstreamModel string) ([]byte, error) {
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
