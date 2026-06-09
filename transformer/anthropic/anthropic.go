// Package anthropic 实现 Codex (OpenAI Responses API) <-> Anthropic Messages API 的协议转换。
//
// 设计原则：完全独立，不依赖 transformer/openai、transformer/deepseek 等其他模块，
// 所有 Codex 端的 output item 事件、stream state 都由本包自行管理。
package anthropic

import (
	"context"

	"agr/transformer/tctx"
)

// StreamStateContextKey 是存放在 context 中的 Codex 流式状态 key
const openaiStreamStateKey tctx.ContextKey = "anthropic_codex_stream_state"

// StreamState 跟踪 Anthropic -> Codex 流式响应的累积状态。
type StreamState struct {
	ResponseID string
	Model      string
	Started    bool   // 是否已发送 response.created / in_progress
	Seq        int    // 序列号
	OutputIndex int   // 当前 output item 索引

	// 文本消息
	MessageItemIndex int  // 文本 message 在 output 中的索引
	MessageStarted   bool
	AccumulatedText  string

	// Reasoning
	ReasoningItemIndex int
	ReasoningStarted   bool
	AccumulatedReasoning string

	// Tool calls
	FunctionCalls []FunctionCall

	// Usage
	InputTokens  int
	OutputTokens int
	HasUsage     bool

	// 流结束状态
	Finished     bool
	FinishStatus string
}

// FunctionCall 跟踪单个 function call 的流式累积状态
type FunctionCall struct {
	CallID    string
	Name      string
	Arguments string
	Index     int
	ItemIndex int // 在 output 数组中的索引
	Started   bool
}

// Transformer 将 Codex 客户端请求转换为 Anthropic Messages API 请求，
// 并将 Anthropic 响应转回 Codex 客户端可消费的格式。
type Transformer struct{}

// New 创建一个新的 Anthropic Transformer
func New() *Transformer {
	return &Transformer{}
}

// TransformRequest 转换 Codex 请求到 Anthropic Messages API 请求格式
func (t *Transformer) TransformRequest(ctx context.Context, body []byte) ([]byte, error) {
	return body, nil
}

// TransformResponse 转换 Anthropic Messages API 响应到 Codex Responses API 响应
func (t *Transformer) TransformResponse(ctx context.Context, body []byte) ([]byte, error) {
	return body, nil
}

// TransformStream 转换 Anthropic SSE chunk 为 Codex SSE chunk。
// 返回特殊格式：单 JSON 对象或多 JSON 数组，proxy 层会拆分为独立 SSE 事件。
func (t *Transformer) TransformStream(ctx context.Context, chunk []byte) ([]byte, error) {
	return chunk, nil
}
