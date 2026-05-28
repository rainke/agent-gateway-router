package transformer

import (
	"context"
	"encoding/json"
)

// DeepSeekTransformer 处理 DeepSeek 特有的协议差异
// 主要职责：当客户端未请求 reasoning 时禁用 thinking mode，避免 reasoning_content 回传问题
type DeepSeekTransformer struct{}

func (t *DeepSeekTransformer) TransformRequest(ctx context.Context, body []byte) ([]byte, error) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return body, nil
	}

	// Claude Code (/v1/messages) 请求由 openai transformer 处理 thinking → reasoning_content 转换，此处跳过
	if isClaudeMessagesRequest(ctx) {
		return body, nil
	}

	// 仅当客户端未请求 reasoning（reasoning 为空或 effort 为 none）时禁用 thinking mode
	if v, ok := req["reasoning_effort"].(string); ok && v == "xhigh" {
		req["reasoning_effort"] = "max"
	}
	effort, _ := req["reasoning_effort"].(string)
	if effort == "" || effort == "none" {
		req["thinking"] = map[string]any{"type": "disabled"}
	}

	return json.Marshal(req)
}

func (t *DeepSeekTransformer) TransformResponse(ctx context.Context, body []byte) ([]byte, error) {
	return body, nil
}

func (t *DeepSeekTransformer) TransformStream(ctx context.Context, chunk []byte) ([]byte, error) {
	return chunk, nil
}
