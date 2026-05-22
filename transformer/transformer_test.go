package transformer

import (
	"context"
	"fmt"
	"testing"
)

func TestGet_ValidTransformer(t *testing.T) {
	tr, err := Get("openai-to-custom")
	if err != nil {
		t.Fatalf("获取有效 Transformer 失败: %v", err)
	}
	if tr == nil {
		t.Fatal("返回的 Transformer 为 nil")
	}
}

func TestGet_InvalidTransformer(t *testing.T) {
	_, err := Get("nonexistent")
	if err == nil {
		t.Fatal("期望获取不存在的 Transformer 时返回错误")
	}
}

func TestNewChain_Valid(t *testing.T) {
	chain, err := NewChain([]string{"openai-to-custom"})
	if err != nil {
		t.Fatalf("创建有效 Chain 失败: %v", err)
	}
	if chain == nil {
		t.Fatal("返回的 Chain 为 nil")
	}
}

func TestNewChain_Empty(t *testing.T) {
	chain, err := NewChain([]string{})
	if err != nil {
		t.Fatalf("创建空 Chain 失败: %v", err)
	}
	if chain == nil {
		t.Fatal("返回的 Chain 为 nil")
	}
}

func TestNewChain_Invalid(t *testing.T) {
	_, err := NewChain([]string{"nonexistent"})
	if err == nil {
		t.Fatal("期望创建包含无效 Transformer 的 Chain 时返回错误")
	}
}

func TestChain_TransformRequest_Passthrough(t *testing.T) {
	chain, _ := NewChain([]string{})
	ctx := context.Background()
	body := []byte(`{"model":"test"}`)

	result, err := chain.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("空 Chain TransformRequest 失败: %v", err)
	}
	if string(result) != string(body) {
		t.Errorf("空 Chain 应该透传，期望 %s，实际 %s", body, result)
	}
}

func TestChain_TransformResponse_Passthrough(t *testing.T) {
	chain, _ := NewChain([]string{})
	ctx := context.Background()
	body := []byte(`{"result":"ok"}`)

	result, err := chain.TransformResponse(ctx, body)
	if err != nil {
		t.Fatalf("空 Chain TransformResponse 失败: %v", err)
	}
	if string(result) != string(body) {
		t.Errorf("空 Chain 应该透传")
	}
}

func TestChain_TransformStream_Passthrough(t *testing.T) {
	chain, _ := NewChain([]string{})
	ctx := context.Background()
	chunk := []byte(`{"delta":"hello"}`)

	result, err := chain.TransformStream(ctx, chunk)
	if err != nil {
		t.Fatalf("空 Chain TransformStream 失败: %v", err)
	}
	if string(result) != string(chunk) {
		t.Errorf("空 Chain 应该透传")
	}
}

func TestChain_TransformRequest_WithTransformer(t *testing.T) {
	chain, _ := NewChain([]string{"openai-to-custom"})
	ctx := context.WithValue(context.Background(), RequestPathKey, "/v1/messages")
	ctx = context.WithValue(ctx, UpstreamModelKey, "real-model")

	body := []byte(`{"model":"client-model","messages":[{"role":"user","content":"hello"}],"max_tokens":100,"stream":false}`)

	result, err := chain.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("Chain TransformRequest 失败: %v", err)
	}
	if len(result) == 0 {
		t.Fatal("转换结果为空")
	}
}

// 用于测试 Chain 错误传播的 mock Transformer
type errorTransformer struct{}

func (e *errorTransformer) TransformRequest(ctx context.Context, body []byte) ([]byte, error) {
	return nil, fmt.Errorf("mock request error")
}
func (e *errorTransformer) TransformResponse(ctx context.Context, body []byte) ([]byte, error) {
	return nil, fmt.Errorf("mock response error")
}
func (e *errorTransformer) TransformStream(ctx context.Context, chunk []byte) ([]byte, error) {
	return nil, fmt.Errorf("mock stream error")
}

func TestChain_TransformRequest_Error(t *testing.T) {
	chain := &Chain{transformers: []Transformer{&errorTransformer{}}}
	ctx := context.Background()

	_, err := chain.TransformRequest(ctx, []byte(`{}`))
	if err == nil {
		t.Fatal("期望返回错误")
	}
}

func TestChain_TransformResponse_Error(t *testing.T) {
	chain := &Chain{transformers: []Transformer{&errorTransformer{}}}
	ctx := context.Background()

	_, err := chain.TransformResponse(ctx, []byte(`{}`))
	if err == nil {
		t.Fatal("期望返回错误")
	}
}

func TestChain_TransformStream_Error(t *testing.T) {
	chain := &Chain{transformers: []Transformer{&errorTransformer{}}}
	ctx := context.Background()

	_, err := chain.TransformStream(ctx, []byte(`{}`))
	if err == nil {
		t.Fatal("期望返回错误")
	}
}

func TestChain_MultipleTransformers_Order(t *testing.T) {
	// 注册两个相同的 transformer 验证链式执行
	chain, err := NewChain([]string{"openai-to-custom", "openai-to-custom"})
	if err != nil {
		t.Fatalf("创建 Chain 失败: %v", err)
	}

	ctx := context.WithValue(context.Background(), RequestPathKey, "/unknown")
	body := []byte(`{"test": true}`)

	// 未知路径透传，两次透传结果应相同
	result, err := chain.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("TransformRequest 失败: %v", err)
	}
	if string(result) != string(body) {
		t.Errorf("透传结果不一致")
	}

	result, err = chain.TransformResponse(ctx, body)
	if err != nil {
		t.Fatalf("TransformResponse 失败: %v", err)
	}
	if string(result) != string(body) {
		t.Errorf("透传结果不一致")
	}

	result, err = chain.TransformStream(ctx, body)
	if err != nil {
		t.Fatalf("TransformStream 失败: %v", err)
	}
	if string(result) != string(body) {
		t.Errorf("透传结果不一致")
	}
}
