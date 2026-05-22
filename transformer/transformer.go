package transformer

import (
	"context"
	"fmt"
)

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

// registry 内置 Transformer 注册表
var registry = map[string]func() Transformer{
	"openai-to-custom": func() Transformer { return &OpenAIToCustomTransformer{} },
	"deepseek":         func() Transformer { return &DeepSeekTransformer{} },
}

// Get 根据名称获取 Transformer 实例
func Get(name string) (Transformer, error) {
	factory, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("未知的 transformer: %s", name)
	}
	return factory(), nil
}
