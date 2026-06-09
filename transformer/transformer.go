package transformer

import (
	"context"
	"fmt"
	"strings"

	"agr/transformer/anthropic"
	"agr/transformer/openai"
	"agr/transformer/tctx"
)

// 从 tctx 包重新导出共享类型和常量，保持公共 API 不变
type ContextKey = tctx.ContextKey

const (
	RequestPathKey     = tctx.RequestPathKey
	UpstreamModelKey   = tctx.UpstreamModelKey
	ClientModelKey     = tctx.ClientModelKey
	StreamStateKey     = tctx.StreamStateKey
	RequestMetadataKey = tctx.RequestMetadataKey
)

type RequestMetadata = tctx.RequestMetadata
type StreamState = tctx.StreamState
type ToolCallAccumulator = tctx.ToolCallAccumulator

// Transformer 请求/响应转换接口
type Transformer interface {
	// TransformRequest 转换请求体
	TransformRequest(ctx context.Context, body []byte) ([]byte, error)
	// TransformResponse 转换非流式响应体
	TransformResponse(ctx context.Context, body []byte) ([]byte, error)
	// TransformStream 转换流式响应的单个 chunk
	TransformStream(ctx context.Context, chunk []byte) ([]byte, error)
}

// Chain 是 Transformer 链，按顺序执行多个 Transformer
type Chain struct {
	transformers []Transformer
}

// Transformers 返回链中所有 Transformer 的副本（按正序），
// 主要用于 proxy 等上层判断链中是否包含特定类型的 transformer。
func (c *Chain) Transformers() []Transformer {
	out := make([]Transformer, len(c.transformers))
	copy(out, c.transformers)
	return out
}

// NewChain 根据 Transformer 名称列表创建链
func NewChain(names []string) (*Chain, error) {
	var ts []Transformer
	for _, name := range names {
		t, err := Get(name)
		if err != nil {
			return nil, err
		}
		ts = append(ts, t)
	}
	return &Chain{transformers: ts}, nil
}

// TransformRequest 按正序执行请求转换
func (c *Chain) TransformRequest(ctx context.Context, body []byte) ([]byte, error) {
	var err error
	for _, t := range c.transformers {
		body, err = t.TransformRequest(ctx, body)
		if err != nil {
			return nil, err
		}
	}
	return body, nil
}

// TransformResponse 按反序执行响应转换
func (c *Chain) TransformResponse(ctx context.Context, body []byte) ([]byte, error) {
	var err error
	for i := len(c.transformers) - 1; i >= 0; i-- {
		body, err = c.transformers[i].TransformResponse(ctx, body)
		if err != nil {
			return nil, err
		}
	}
	return body, nil
}

// TransformStream 按反序执行流式 chunk 转换
func (c *Chain) TransformStream(ctx context.Context, chunk []byte) ([]byte, error) {
	var err error
	for i := len(c.transformers) - 1; i >= 0; i-- {
		chunk, err = c.transformers[i].TransformStream(ctx, chunk)
		if err != nil {
			return nil, err
		}
	}
	return chunk, nil
}

// CodexStreamTransformer 支持 Codex 流式转换的接口
type CodexStreamTransformer interface {
	TransformCodexStream(ctx context.Context, chunk []byte) ([][]byte, error)
}

// TransformCodexStream 执行 Codex 流式 chunk 转换，返回多个事件
func (c *Chain) TransformCodexStream(ctx context.Context, chunk []byte) ([][]byte, error) {
	for i := len(c.transformers) - 1; i >= 0; i-- {
		if ct, ok := c.transformers[i].(CodexStreamTransformer); ok {
			return ct.TransformCodexStream(ctx, chunk)
		}
	}
	// 如果没有 transformer 支持 Codex 流式转换，返回原始 chunk
	return [][]byte{chunk}, nil
}

// registry 内置 Transformer 注册表
var registry = map[string]func() Transformer{
	"openai":           func() Transformer { return &openai.Transformer{} },
	"deepseek":         func() Transformer { return &DeepSeekTransformer{} },
	"anthropic":        func() Transformer { return anthropic.New() },
	"openai-responses": func() Transformer { return &OpenAIResponsesTransformer{} },
}

// Get 根据名称获取 Transformer 实例
func Get(name string) (Transformer, error) {
	factory, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("未知的 transformer: %s", name)
	}
	return factory(), nil
}

// isClaudeMessagesRequest 判断当前请求是否为 Anthropic Messages API (/v1/messages)
func isClaudeMessagesRequest(ctx context.Context) bool {
	path, _ := ctx.Value(tctx.RequestPathKey).(string)
	return strings.Contains(path, "/v1/messages")
}
