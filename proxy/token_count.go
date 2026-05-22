package proxy

import (
	"encoding/json"
	"fmt"
	"strings"

	tiktoken "github.com/pkoukk/tiktoken-go"
)

func countClaudeMessageTokens(body []byte, model string) (int, error) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return 0, fmt.Errorf("解析请求体 JSON 失败: %w", err)
	}

	enc, err := tokenCountEncoding(model)
	if err != nil {
		return 0, err
	}

	var prompt strings.Builder
	appendPromptField(&prompt, "system", req["system"])
	appendMessages(&prompt, req["messages"])
	appendPromptField(&prompt, "tools", req["tools"])
	appendPromptField(&prompt, "tool_choice", req["tool_choice"])
	appendPromptField(&prompt, "thinking", req["thinking"])

	return len(enc.EncodeOrdinary(prompt.String())), nil
}

func tokenCountEncoding(model string) (*tiktoken.Tiktoken, error) {
	if model != "" {
		if enc, err := tiktoken.EncodingForModel(model); err == nil {
			return enc, nil
		}
	}
	return tiktoken.GetEncoding("cl100k_base")
}

func appendMessages(b *strings.Builder, messages any) {
	msgs, ok := messages.([]any)
	if !ok {
		return
	}

	for _, raw := range msgs {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		role, _ := msg["role"].(string)
		if role != "" {
			b.WriteString(role)
			b.WriteString(":\n")
		}
		appendPromptValue(b, msg["content"])
		b.WriteByte('\n')
	}
}

func appendPromptField(b *strings.Builder, name string, value any) {
	if value == nil {
		return
	}
	b.WriteString(name)
	b.WriteString(":\n")
	appendPromptValue(b, value)
	b.WriteByte('\n')
}

func appendPromptValue(b *strings.Builder, value any) {
	switch v := value.(type) {
	case string:
		b.WriteString(v)
	case []any:
		for _, item := range v {
			appendPromptValue(b, item)
			b.WriteByte('\n')
		}
	case map[string]any:
		appendContentBlock(b, v)
	default:
		appendJSON(b, v)
	}
}

func appendContentBlock(b *strings.Builder, block map[string]any) {
	blockType, _ := block["type"].(string)

	switch blockType {
	case "text":
		if text, _ := block["text"].(string); text != "" {
			b.WriteString(text)
			return
		}
	case "thinking":
		if thinking, _ := block["thinking"].(string); thinking != "" {
			b.WriteString(thinking)
			return
		}
		if text, _ := block["text"].(string); text != "" {
			b.WriteString(text)
			return
		}
	case "tool_result":
		if id, _ := block["tool_use_id"].(string); id != "" {
			b.WriteString(id)
			b.WriteByte('\n')
		}
		appendPromptValue(b, block["content"])
		return
	case "tool_use":
		if name, _ := block["name"].(string); name != "" {
			b.WriteString(name)
			b.WriteByte('\n')
		}
		appendPromptValue(b, block["input"])
		return
	}

	appendJSON(b, block)
}

func appendJSON(b *strings.Builder, value any) {
	if value == nil {
		return
	}
	data, err := json.Marshal(value)
	if err != nil {
		fmt.Fprintf(b, "%v", value)
		return
	}
	b.Write(data)
}
