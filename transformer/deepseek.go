package transformer

import (
	"context"
	"encoding/json"
	"strings"

	"agr/transformer/openai"
)

// DeepSeekTransformer 处理 DeepSeek 特有的协议差异
// 主要职责：非 Claude Code 请求禁用 thinking mode；Claude Code 请求保留 thinking 回传
type DeepSeekTransformer struct{}

func (t *DeepSeekTransformer) TransformRequest(ctx context.Context, body []byte) ([]byte, error) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return body, nil
	}

	if isClaudeMessagesRequest(ctx) {
		t.moveClaudeThinkingToReasoningContent(req)
		return json.Marshal(req)
	}

	// 禁用 thinking mode，避免 reasoning_content 回传问题
	req["thinking"] = map[string]any{"type": "disabled"}

	return json.Marshal(req)
}

func (t *DeepSeekTransformer) TransformResponse(ctx context.Context, body []byte) ([]byte, error) {
	return body, nil
}

func (t *DeepSeekTransformer) TransformStream(ctx context.Context, chunk []byte) ([]byte, error) {
	return chunk, nil
}

func isClaudeMessagesRequest(ctx context.Context) bool {
	path, _ := ctx.Value(openai.RequestPathKey).(string)
	return strings.Contains(path, "/v1/messages")
}

func (t *DeepSeekTransformer) moveClaudeThinkingToReasoningContent(req map[string]any) {
	messages, ok := req["messages"].([]any)
	if !ok {
		return
	}

	for _, msg := range messages {
		m, ok := msg.(map[string]any)
		if !ok {
			continue
		}
		if role, _ := m["role"].(string); role != "assistant" {
			continue
		}

		if reasoning, ok := extractClaudeThinking(m["content"]); ok {
			m["reasoning_content"] = reasoning
		}
	}
}

func extractClaudeThinking(content any) (string, bool) {
	parts, ok := content.([]any)
	if !ok {
		return "", false
	}

	thinkingParts := make([]string, 0)
	for _, part := range parts {
		p, ok := part.(map[string]any)
		if !ok {
			continue
		}
		if typ, _ := p["type"].(string); typ != "thinking" {
			continue
		}
		if thinking, _ := p["thinking"].(string); thinking != "" {
			thinkingParts = append(thinkingParts, thinking)
			continue
		}
		if text, _ := p["text"].(string); text != "" {
			thinkingParts = append(thinkingParts, text)
		}
	}

	if len(thinkingParts) == 0 {
		return "", false
	}
	return strings.Join(thinkingParts, "\n"), true
}
