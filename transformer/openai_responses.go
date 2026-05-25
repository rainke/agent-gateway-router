package transformer

import (
	"context"
	"fmt"
)

// OpenAIResponsesTransformer 只允许 Codex (OpenAI Responses API) 请求通过，
// 对 Anthropic Messages API 请求返回错误
type OpenAIResponsesTransformer struct{}

func (t *OpenAIResponsesTransformer) TransformRequest(ctx context.Context, body []byte) ([]byte, error) {
	if isClaudeMessagesRequest(ctx) {
		return nil, fmt.Errorf("Anthropic (Messages API) 请求未实现，仅支持 Codex (Responses API) 请求")
	}
	return body, nil
}

func (t *OpenAIResponsesTransformer) TransformResponse(ctx context.Context, body []byte) ([]byte, error) {
	return body, nil
}

func (t *OpenAIResponsesTransformer) TransformStream(ctx context.Context, chunk []byte) ([]byte, error) {
	return chunk, nil
}