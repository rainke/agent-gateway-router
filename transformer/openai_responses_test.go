package transformer

import (
	"context"
	"testing"

	"agr/transformer/openai"
)

func TestOpenAIResponses_CodexRequest_PassThrough(t *testing.T) {
	tr := &OpenAIResponsesTransformer{}
	ctx := context.WithValue(context.Background(), openai.RequestPathKey, "/v1/responses")
	body := []byte(`{"model":"gpt-4","input":"hello"}`)

	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("Codex 请求应透传，但返回错误: %v", err)
	}
	if string(result) != string(body) {
		t.Errorf("Codex 请求应透传，期望 %s，实际 %s", body, result)
	}
}

func TestOpenAIResponses_ClaudeRequest_Error(t *testing.T) {
	tr := &OpenAIResponsesTransformer{}
	ctx := context.WithValue(context.Background(), openai.RequestPathKey, "/v1/messages")
	body := []byte(`{"model":"claude-3","messages":[{"role":"user","content":"hi"}]}`)

	_, err := tr.TransformRequest(ctx, body)
	if err == nil {
		t.Fatal("Claude 请求应返回错误，但返回 nil")
	}
}

func TestOpenAIResponses_Response_PassThrough(t *testing.T) {
	tr := &OpenAIResponsesTransformer{}
	ctx := context.Background()
	body := []byte(`{"id":"resp_1","output":[]}`)

	result, err := tr.TransformResponse(ctx, body)
	if err != nil {
		t.Fatalf("TransformResponse 应透传: %v", err)
	}
	if string(result) != string(body) {
		t.Errorf("TransformResponse 应透传")
	}
}

func TestOpenAIResponses_Stream_PassThrough(t *testing.T) {
	tr := &OpenAIResponsesTransformer{}
	ctx := context.Background()
	chunk := []byte(`{"choices":[],"usage":{}}`)

	result, err := tr.TransformStream(ctx, chunk)
	if err != nil {
		t.Fatalf("TransformStream 应透传: %v", err)
	}
	if string(result) != string(chunk) {
		t.Errorf("TransformStream 应透传")
	}
}

func TestOpenAIResponses_InChain_BlocksClaude(t *testing.T) {
	chain, err := NewChain([]string{"openai-responses"})
	if err != nil {
		t.Fatalf("创建 Chain 失败: %v", err)
	}

	ctx := context.WithValue(context.Background(), openai.RequestPathKey, "/v1/messages")
	body := []byte(`{"model":"claude-3"}`)

	_, err = chain.TransformRequest(ctx, body)
	if err == nil {
		t.Fatal("Chain 包含 openai-responses 时，Claude 请求应返回错误")
	}
}

func TestOpenAIResponses_InChain_AllowsCodex(t *testing.T) {
	chain, err := NewChain([]string{"openai-responses"})
	if err != nil {
		t.Fatalf("创建 Chain 失败: %v", err)
	}

	ctx := context.WithValue(context.Background(), openai.RequestPathKey, "/v1/responses")
	body := []byte(`{"model":"gpt-4"}`)

	result, err := chain.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("Chain 包含 openai-responses 时，Codex 请求应透传: %v", err)
	}
	if string(result) != string(body) {
		t.Errorf("Codex 请求应透传")
	}
}

func TestOpenAIResponses_Registry(t *testing.T) {
	tr, err := Get("openai-responses")
	if err != nil {
		t.Fatalf("注册表中应能获取 openai-responses: %v", err)
	}
	if _, ok := tr.(*OpenAIResponsesTransformer); !ok {
		t.Fatal("获取的 transformer 类型不正确")
	}
}