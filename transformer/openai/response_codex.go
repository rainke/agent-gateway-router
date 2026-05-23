package openai

import (
	"encoding/json"
	"fmt"
)

// transformToCodexResponse 将 OpenAI Chat Completions 响应转换为 Responses API 格式
func (t *Transformer) transformToCodexResponse(body []byte, clientModel string) ([]byte, error) {
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
