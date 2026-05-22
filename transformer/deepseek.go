package transformer

import (
	"context"
	"encoding/json"
)

// DeepSeekTransformer 处理 DeepSeek 特有的协议差异
// 主要职责：禁用 thinking mode，避免 reasoning_content 回传问题
type DeepSeekTransformer struct{}

func (t *DeepSeekTransformer) TransformRequest(ctx context.Context, body []byte) ([]byte, error) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return body, nil
	}

	// 禁用 thinking mode，避免 reasoning_content 回传问题
	req["thinking"] = map[string]any{"type": "disabled"}

	return json.Marshal(req)
}

func (t *DeepSeekTransformer) TransformResponse(ctx context.Context, body []byte) ([]byte, error) {
	return body, nil
}

func (t *DeepSeekTransformer) TransformStream(ctx context.Context, chunk []byte) ([]byte, error) {
	return chunk, nil
}
