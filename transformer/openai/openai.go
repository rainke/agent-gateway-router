package openai

import (
	"context"
	"strings"
	"time"
)

// Transformer 将不同客户端协议转换为上游 Provider 可接受的格式
type Transformer struct{}

// ContextKey 自定义 context key 类型
type ContextKey string

const (
	// RequestPathKey 请求路径 context key
	RequestPathKey ContextKey = "request_path"
	// UpstreamModelKey 上游真实模型名 context key
	UpstreamModelKey ContextKey = "upstream_model"
	// ClientModelKey 客户端请求的模型名 context key
	ClientModelKey ContextKey = "client_model"
	// StreamStateKey 流式状态 context key
	StreamStateKey ContextKey = "stream_state"
)

// StreamState 流式响应状态，用于跨 chunk 追踪 tool_calls 组装
type StreamState struct {
	// 当前正在组装的 tool calls
	ToolCalls []ToolCallAccumulator
	// 当前 content block 索引
	BlockIndex int
	// 是否已经输出过 text content
	HasTextContent bool
	// 当前 text/thinking block 索引
	TextBlockIndex     int
	ThinkingBlockIndex int
	// text/thinking block 是否已经开始
	TextBlockStarted     bool
	ThinkingBlockStarted bool
	// 当前仍打开的 content block
	OpenBlocks map[int]bool
	// Token 使用统计（从上游 usage chunk 中提取）
	InputTokens  int
	OutputTokens int
}

type ToolCallAccumulator struct {
	Index    int
	ID       string
	Name     string
	Args     string
	Complete bool
}

func (t *Transformer) TransformRequest(ctx context.Context, clientBody []byte) ([]byte, error) {
	path, _ := ctx.Value(RequestPathKey).(string)
	upstreamModel, _ := ctx.Value(UpstreamModelKey).(string)

	switch {
	case strings.Contains(path, "/v1/messages"):
		return t.transformClaudeRequest(clientBody, upstreamModel)
	case strings.Contains(path, "/v1/responses"):
		return t.transformCodexRequest(clientBody, upstreamModel)
	default:
		return clientBody, nil
	}
}

func (t *Transformer) TransformResponse(ctx context.Context, body []byte) ([]byte, error) {
	path, _ := ctx.Value(RequestPathKey).(string)
	clientModel, _ := ctx.Value(ClientModelKey).(string)

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
	path, _ := ctx.Value(RequestPathKey).(string)
	clientModel, _ := ctx.Value(ClientModelKey).(string)

	switch {
	case strings.Contains(path, "/v1/messages"):
		return t.transformToClaudeStreamChunk(ctx, chunk, clientModel)
	case strings.Contains(path, "/v1/responses"):
		return chunk, nil
	default:
		return chunk, nil
	}
}

// NowISO 返回当前时间的 ISO 格式字符串
func NowISO() string {
	return time.Now().UTC().Format(time.RFC3339)
}
