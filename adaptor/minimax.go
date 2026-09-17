package adaptor

import "encoding/json"

func init() {
	Register("minimax", minimax{})
}

// minimax 为 MiniMax OpenAI 兼容接口注入 reasoning_split。
// chat/completions 缺省开启后，thinking 内容拆分到 reasoning_content /
// reasoning_details（见 platform.minimax.cn text-chat-openai 文档）。
// 客户端已显式设置 reasoning_split 时保持不变。
type minimax struct{}

func (minimax) Apply(req *Request) error {
	if req.Path != "/v1/chat/completions" {
		return nil
	}
	if _, exists := req.Body["reasoning_split"]; exists {
		return nil
	}
	req.Body["reasoning_split"] = json.RawMessage("true")
	return nil
}
