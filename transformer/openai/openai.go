package openai

import (
	"context"
	"strings"
	"time"

	"agr/transformer/tctx"
)

// Transformer 将不同客户端协议转换为上游 Provider 可接受的格式
type Transformer struct{}

func (t *Transformer) TransformRequest(ctx context.Context, clientBody []byte) ([]byte, error) {
	path, _ := ctx.Value(tctx.RequestPathKey).(string)
	upstreamModel, _ := ctx.Value(tctx.UpstreamModelKey).(string)

	switch {
	case strings.Contains(path, "/v1/messages"):
		return t.transformClaudeRequest(ctx, clientBody, upstreamModel)
	case strings.Contains(path, "/v1/responses"):
		return t.transformCodexRequest(ctx, clientBody, upstreamModel)
	default:
		return clientBody, nil
	}
}

func markReasoningIncluded(ctx context.Context) {
	if metadata, ok := ctx.Value(tctx.RequestMetadataKey).(*tctx.RequestMetadata); ok && metadata != nil {
		metadata.ReasoningIncluded = true
	}
}

func (t *Transformer) TransformResponse(ctx context.Context, body []byte) ([]byte, error) {
	path, _ := ctx.Value(tctx.RequestPathKey).(string)
	clientModel, _ := ctx.Value(tctx.ClientModelKey).(string)

	switch {
	case strings.Contains(path, "/v1/messages"):
		return t.transformToClaudeResponse(body, clientModel)
	case strings.Contains(path, "/v1/responses"):
		return t.transformToCodexResponse(body, clientModel)
	default:
		return body, nil
	}
}

func (t *Transformer) TransformStream(ctx context.Context, chunk []byte) ([]byte, error) {
	path, _ := ctx.Value(tctx.RequestPathKey).(string)
	clientModel, _ := ctx.Value(tctx.ClientModelKey).(string)

	switch {
	case strings.Contains(path, "/v1/messages"):
		return t.transformToClaudeStreamChunk(ctx, chunk, clientModel)
	case strings.Contains(path, "/v1/responses"):
		// Codex 流式由 TransformCodexStream 处理
		return chunk, nil
	default:
		return chunk, nil
	}
}

// TransformCodexStream 实现 CodexStreamTransformer 接口
func (t *Transformer) TransformCodexStream(ctx context.Context, chunk []byte) ([][]byte, error) {
	clientModel, _ := ctx.Value(tctx.ClientModelKey).(string)
	return t.transformToCodexStreamChunk(ctx, chunk, clientModel)
}

// NowISO 返回当前时间的 ISO 格式字符串
func NowISO() string {
	return time.Now().UTC().Format(time.RFC3339)
}
