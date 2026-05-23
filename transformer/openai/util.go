package openai

import (
	"fmt"
	"strings"
)

// ExtractToolResultContent 从 tool_result 的 content 中提取文本
func ExtractToolResultContent(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []any:
		var parts []string
		for _, part := range c {
			if p, ok := part.(map[string]any); ok {
				if p["type"] == "text" {
					if text, ok := p["text"].(string); ok {
						parts = append(parts, text)
					}
				}
			}
		}
		return strings.Join(parts, "")
	default:
		if c == nil {
			return ""
		}
		return fmt.Sprintf("%v", c)
	}
}

func allocateContentBlock(state *StreamState) int {
	state.BlockIndex++
	ensureOpenBlocks(state)
	state.OpenBlocks[state.BlockIndex] = true
	return state.BlockIndex
}

func ensureOpenBlocks(state *StreamState) {
	if state.OpenBlocks == nil {
		state.OpenBlocks = make(map[int]bool)
	}
}

func eventWithIndex(event map[string]any, index int) map[string]any {
	event["index"] = index
	return event
}

func stopOpenTextBlock(state *StreamState) []map[string]any {
	if !state.TextBlockStarted {
		return nil
	}
	return stopOpenBlock(state, state.TextBlockIndex)
}

func stopOpenThinkingBlock(state *StreamState) []map[string]any {
	if !state.ThinkingBlockStarted {
		return nil
	}
	return stopOpenBlock(state, state.ThinkingBlockIndex)
}

func stopOpenBlock(state *StreamState, index int) []map[string]any {
	ensureOpenBlocks(state)
	if !state.OpenBlocks[index] {
		return nil
	}
	state.OpenBlocks[index] = false
	return []map[string]any{{"type": "content_block_stop", "index": index}}
}
