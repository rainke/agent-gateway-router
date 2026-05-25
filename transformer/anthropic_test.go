package transformer

import (
	"context"
	"testing"

	"agr/transformer/openai"
)

func TestAnthropic_AnthroMessagesRequest_PassThrough(t *testing.T) {
	tr := &AnthropicTransformer{}
	ctx := context.WithValue(context.Background(), openai.RequestPathKey, "/v1/messages")
	body := []byte(`{"model":"claude-3","messages":[{"role":"user","content":"hi"}]}`)

	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("Anthropic 请求应透传，但返回错误: %v", err)
	}
	if string(result) != string(body) {
		t.Errorf("Anthropic 请求应透传，期望 %s，实际 %s", body, result)
	}
}

func TestAnthropic_CodexRequest_Error(t *testing.T) {
	tr := &AnthropicTransformer{}
	ctx := context.WithValue(context.Background(), openai.RequestPathKey, "/v1/responses")
	body := []byte(`{"model":"gpt-4","input":"hello"}`)

	_, err := tr.TransformRequest(ctx, body)
	if err == nil {
		t.Fatal("Codex 请求应返回错误，但返回 nil")
	}
}

func TestAnthropic_Response_PassThrough(t *testing.T) {
	tr := &AnthropicTransformer{}
	ctx := context.Background()
	body := []byte(`{"id":"msg_1","content":[]}`)

	result, err := tr.TransformResponse(ctx, body)
	if err != nil {
		t.Fatalf("TransformResponse 应透传: %v", err)
	}
	if string(result) != string(body) {
		t.Errorf("TransformResponse 应透传")
	}
}

func TestAnthropic_Stream_PassThrough(t *testing.T) {
	tr := &AnthropicTransformer{}
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

func TestAnthropic_InChain_BlocksCodex(t *testing.T) {
	chain, err := NewChain([]string{"anthropic"})
	if err != nil {
		t.Fatalf("创建 Chain 失败: %v", err)
	}

	ctx := context.WithValue(context.Background(), openai.RequestPathKey, "/v1/responses")
	body := []byte(`{"model":"gpt-4"}`)

	_, err = chain.TransformRequest(ctx, body)
	if err == nil {
		t.Fatal("Chain 包含 anthropic 时，Codex 请求应返回错误")
	}
}

func TestAnthropic_InChain_AllowsAnthropic(t *testing.T) {
	chain, err := NewChain([]string{"anthropic"})
	if err != nil {
		t.Fatalf("创建 Chain 失败: %v", err)
	}

	ctx := context.WithValue(context.Background(), openai.RequestPathKey, "/v1/messages")
	body := []byte(`{"model":"claude-3"}`)

	result, err := chain.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("Chain 包含 anthropic 时，Anthropic 请求应透传: %v", err)
	}
	if string(result) != string(body) {
		t.Errorf("Anthropic 请求应透传")
	}
}

func TestAnthropic_Registry(t *testing.T) {
	tr, err := Get("anthropic")
	if err != nil {
		t.Fatalf("注册表中应能获取 anthropic: %v", err)
	}
	if _, ok := tr.(*AnthropicTransformer); !ok {
		t.Fatal("获取的 transformer 类型不正确")
	}
}

func TestIsCodexRequest(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/v1/responses", true},
		{"/v1/messages", false},
		{"/v1/chat/completions", false},
		{"", false},
	}

	for _, tt := range tests {
		ctx := context.WithValue(context.Background(), openai.RequestPathKey, tt.path)
		if got := isCodexRequest(ctx); got != tt.want {
			t.Errorf("isCodexRequest(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}