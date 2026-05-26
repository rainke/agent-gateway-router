package tctx

import "context"

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
	// RequestMetadataKey 请求转换期间收集的元信息 context key
	RequestMetadataKey ContextKey = "request_metadata"
)

// RequestMetadata 记录请求转换期间发现的属性，供 proxy 写响应头使用。
type RequestMetadata struct {
	ReasoningIncluded bool
}

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

// ToolCallAccumulator 追踪单个 tool call 的累积状态
type ToolCallAccumulator struct {
	Index    int
	ID       string
	Name     string
	Args     string
	Complete bool
}

// StreamStateFromContext 从 context 获取 StreamState
func StreamStateFromContext(ctx context.Context) *StreamState {
	s, _ := ctx.Value(StreamStateKey).(*StreamState)
	return s
}

// RequestMetadataFromContext 从 context 获取 RequestMetadata
func RequestMetadataFromContext(ctx context.Context) *RequestMetadata {
	m, _ := ctx.Value(RequestMetadataKey).(*RequestMetadata)
	return m
}
