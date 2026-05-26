package transformer

import (
	"context"
	"encoding/json"
	"testing"
)

func makeCtx(path, upstreamModel, clientModel string) context.Context {
	ctx := context.Background()
	ctx = context.WithValue(ctx, RequestPathKey, path)
	ctx = context.WithValue(ctx, UpstreamModelKey, upstreamModel)
	ctx = context.WithValue(ctx, ClientModelKey, clientModel)
	return ctx
}

func TestDeepSeekTransformRequest_DisablesThinkingForNonClaudeMessages(t *testing.T) {
	tr := &DeepSeekTransformer{}
	body := []byte(`{"model":"deepseek-reasoner","messages":[{"role":"user","content":"hi"}]}`)

	result, err := tr.TransformRequest(context.Background(), body)
	if err != nil {
		t.Fatalf("TransformRequest 失败: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("解析结果失败: %v", err)
	}

	thinking, ok := parsed["thinking"].(map[string]any)
	if !ok {
		t.Fatal("期望 thinking 字段存在")
	}
	if thinking["type"] != "disabled" {
		t.Fatalf("期望 thinking.type 为 disabled，实际 %v", thinking["type"])
	}
}

func TestDeepSeekTransformRequest_DoesNotDisableThinkingForClaudeMessages(t *testing.T) {
	tr := &DeepSeekTransformer{}
	ctx := makeCtx("/v1/messages", "deepseek-reasoner", "claude-3")
	body := []byte(`{"model":"deepseek-reasoner","messages":[{"role":"user","content":"hi"}]}`)

	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("TransformRequest 失败: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("解析结果失败: %v", err)
	}

	if _, ok := parsed["thinking"]; ok {
		t.Fatal("Claude Code /v1/messages 请求不应被写入 thinking: disabled")
	}
}

func TestDeepSeekTransformRequest_MovesClaudeThinkingToReasoningContent(t *testing.T) {
	tr := &DeepSeekTransformer{}
	ctx := makeCtx("/v1/messages", "deepseek-reasoner", "claude-3")
	body := []byte(`{
		"model": "deepseek-reasoner",
		"messages": [
			{
				"role": "assistant",
				"content": [
					{"type": "thinking", "thinking": "need to inspect files"},
					{"type": "text", "text": "I will check that."}
				]
			}
		]
	}`)

	result, err := tr.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("TransformRequest 失败: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("解析结果失败: %v", err)
	}

	messages := parsed["messages"].([]any)
	assistant := messages[0].(map[string]any)
	if assistant["reasoning_content"] != "need to inspect files" {
		t.Fatalf("期望 reasoning_content 保留 thinking，实际 %v", assistant["reasoning_content"])
	}
}

func TestDeepSeekChain_PreservesClaudeThinkingAsReasoningContent(t *testing.T) {
	chain, err := NewChain([]string{"openai", "deepseek"})
	if err != nil {
		t.Fatalf("创建 Chain 失败: %v", err)
	}
	ctx := makeCtx("/v1/messages", "deepseek-reasoner", "claude-3")
	body := []byte(`{
		"model": "claude-3",
		"messages": [
			{
				"role": "assistant",
				"content": [
					{"type": "thinking", "thinking": "prior reasoning"},
					{"type": "text", "text": "prior answer"}
				]
			},
			{"role": "user", "content": "continue"}
		],
		"max_tokens": 100
	}`)

	result, err := chain.TransformRequest(ctx, body)
	if err != nil {
		t.Fatalf("TransformRequest 失败: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("解析结果失败: %v", err)
	}
	if _, ok := parsed["thinking"]; ok {
		t.Fatal("Claude Code /v1/messages 请求不应被写入 thinking: disabled")
	}

	messages := parsed["messages"].([]any)
	assistant := messages[0].(map[string]any)
	if assistant["reasoning_content"] != "prior reasoning" {
		t.Fatalf("期望 reasoning_content 为 prior reasoning，实际 %v", assistant["reasoning_content"])
	}
	if assistant["content"] != "prior answer" {
		t.Fatalf("期望普通文本仍保留在 content，实际 %v", assistant["content"])
	}
}
