package transformer

import (
	"context"
	"encoding/json"
	"fmt"

	"agr/transformer/openai"
)

// OpenAIResponsesTransformer 只允许 Codex (OpenAI Responses API) 请求通过，
// 对 Anthropic Messages API 请求返回错误
type OpenAIResponsesTransformer struct{}

func (t *OpenAIResponsesTransformer) TransformRequest(ctx context.Context, body []byte) ([]byte, error) {
	if isClaudeMessagesRequest(ctx) {
		return nil, fmt.Errorf("Anthropic (Messages API) 请求未实现，仅支持 Codex (Responses API) 请求")
	}

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return body, nil
	}

	if upstreamModel, _ := ctx.Value(openai.UpstreamModelKey).(string); upstreamModel != "" {
		req["model"] = upstreamModel
		return json.Marshal(req)
	}

	return body, nil
}

func (t *OpenAIResponsesTransformer) TransformResponse(ctx context.Context, body []byte) ([]byte, error) {
	return body, nil
}

func (t *OpenAIResponsesTransformer) TransformStream(ctx context.Context, chunk []byte) ([]byte, error) {
	return chunk, nil
}