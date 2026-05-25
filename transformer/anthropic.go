package transformer

import (
	"context"
	"fmt"
	"strings"

	"agr/transformer/openai"
)

// AnthropicTransformer 只允许 Anthropic Messages API 请求通过，
// 对 Codex (OpenAI Responses API) 请求返回未实现错误
type AnthropicTransformer struct{}

func (t *AnthropicTransformer) TransformRequest(ctx context.Context, body []byte) ([]byte, error) {
	if isCodexRequest(ctx) {
		return nil, fmt.Errorf("Codex (Responses API) 请求未实现，仅支持 Anthropic (Messages API) 请求")
	}
	return body, nil
}

func (t *AnthropicTransformer) TransformResponse(ctx context.Context, body []byte) ([]byte, error) {
	return body, nil
}

func (t *AnthropicTransformer) TransformStream(ctx context.Context, chunk []byte) ([]byte, error) {
	return chunk, nil
}

func isCodexRequest(ctx context.Context) bool {
	path, _ := ctx.Value(openai.RequestPathKey).(string)
	return strings.Contains(path, "/v1/responses")
}