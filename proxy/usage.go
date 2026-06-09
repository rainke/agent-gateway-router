package proxy

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// UsageRecord 上游 usage 统计记录
type UsageRecord struct {
	Timestamp             string `json:"timestamp"`
	Provider              string `json:"provider"`
	Model                 string `json:"model"`
	TotalTokens           int    `json:"total_tokens"`
	InputTokens           int    `json:"input_tokens"`
	CachedTokens          int    `json:"cached_tokens"`
	OutputTokens          int    `json:"output_tokens"`
	OutputReasoningTokens int    `json:"output_reasoning_tokens"`
	OutputTextTokens      int    `json:"output_text_tokens"`
}

// usageMu 保护文件写入并发安全
var usageMu sync.Mutex

// usageDir 自定义 usage 文件存储目录，为空时使用 ~/.agr/usage/
var usageDir string

// extractUsageFromMap 从上游 usage map 中提取 UsageRecord 字段
// 兼容 OpenAI 格式（prompt_tokens/completion_tokens）和 Anthropic 格式（input_tokens/output_tokens）
func extractUsageFromMap(usage map[string]any, provider, model string) UsageRecord {
	record := UsageRecord{
		Provider: provider,
		Model:    model,
	}

	if tt, ok := usage["total_tokens"].(float64); ok {
		record.TotalTokens = int(tt)
	}

	// OpenAI 格式：prompt_tokens / completion_tokens
	if pt, ok := usage["prompt_tokens"].(float64); ok {
		record.InputTokens = int(pt)
	}
	if ct, ok := usage["completion_tokens"].(float64); ok {
		record.OutputTokens = int(ct)
	}

	// Anthropic 格式：input_tokens / output_tokens（兜底）
	if record.InputTokens == 0 {
		if it, ok := usage["input_tokens"].(float64); ok {
			record.InputTokens = int(it)
		}
	}
	// record_input_tokens = input_tokens + cache_creation_input_tokens + cache_read_input_tokens
	var cacheCreation, cacheRead int
	if cc, ok := usage["cache_creation_input_tokens"].(float64); ok {
		cacheCreation = int(cc)
	}
	if cr, ok := usage["cache_read_input_tokens"].(float64); ok {
		cacheRead = int(cr)
	}
	record.InputTokens += cacheCreation + cacheRead
	record.CachedTokens = cacheCreation + cacheRead
	if record.OutputTokens == 0 {
		if ot, ok := usage["output_tokens"].(float64); ok {
			record.OutputTokens = int(ot)
		}
	}

	// OpenAI 格式：prompt_tokens_details.cached_tokens
	if details, ok := usage["prompt_tokens_details"].(map[string]any); ok {
		if cached, ok := details["cached_tokens"].(float64); ok {
			record.CachedTokens = int(cached)
		}
	}

	// completion_tokens_details.reasoning_tokens
	if details, ok := usage["completion_tokens_details"].(map[string]any); ok {
		if reasoning, ok := details["reasoning_tokens"].(float64); ok {
			record.OutputReasoningTokens = int(reasoning)
		}
	}

	// 总 token 兜底：如果上游没返回 total_tokens，用 input + output 计算
	if record.TotalTokens == 0 {
		record.TotalTokens = record.InputTokens + record.OutputTokens
	}

	// 文本 token = 总输出 - reasoning
	record.OutputTextTokens = record.OutputTokens - record.OutputReasoningTokens
	if record.OutputTextTokens < 0 {
		record.OutputTextTokens = 0
	}

	return record
}

// recordUsage 将 usage 记录追加到 ~/.agr/usage/<date>.jsonl
func recordUsage(record UsageRecord) {
	if record.TotalTokens == 0 && record.InputTokens == 0 && record.OutputTokens == 0 {
		return
	}

	record.Timestamp = time.Now().Format(time.RFC3339)

	dir := usageDir
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			slog.Error("获取用户目录失败，跳过 usage 记录", "error", err)
			return
		}
		dir = filepath.Join(home, ".agr", "usage")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		slog.Error("创建 usage 目录失败", "error", err)
		return
	}

	filename := time.Now().Format("2006-01-02") + ".jsonl"
	path := filepath.Join(dir, filename)

	line, err := json.Marshal(record)
	if err != nil {
		slog.Error("序列化 usage 记录失败", "error", err)
		return
	}
	line = append(line, '\n')

	usageMu.Lock()
	defer usageMu.Unlock()

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		slog.Error("打开 usage 文件失败", "path", path, "error", err)
		return
	}
	defer f.Close()

	if _, err := f.Write(line); err != nil {
		slog.Error("写入 usage 记录失败", "error", err)
		return
	}

	slog.Debug("已记录 usage",
		"provider", record.Provider,
		"model", record.Model,
		"total_tokens", record.TotalTokens,
	)
}

// extractAndRecordUsageFromChunk 从上游原始 SSE data 中提取 usage
// 兼容 OpenAI 格式和 Anthropic 流式格式（message_start / message_delta）
func extractAndRecordUsageFromChunk(data []byte, provider, model string, lastUsage *map[string]any) {
	var chunk map[string]any
	if err := json.Unmarshal(data, &chunk); err != nil {
		return
	}

	// OpenAI 格式：顶层 usage 对象
	if usage, ok := chunk["usage"].(map[string]any); ok {
		if *lastUsage == nil {
			*lastUsage = make(map[string]any)
		}
		for k, v := range usage {
			(*lastUsage)[k] = v
		}
		return
	}

	// Anthropic 流式格式：message_start 事件含 input_tokens
	chunkType, _ := chunk["type"].(string)
	if chunkType == "message_start" {
		if msg, ok := chunk["message"].(map[string]any); ok {
			if usage, ok := msg["usage"].(map[string]any); ok {
				if *lastUsage == nil {
					*lastUsage = make(map[string]any)
				}
				for k, v := range usage {
					(*lastUsage)[k] = v
				}
			}
		}
		return
	}

	// Anthropic 流式格式：message_delta 事件含 output_tokens
	if chunkType == "message_delta" {
		if usage, ok := chunk["usage"].(map[string]any); ok {
			if *lastUsage == nil {
				*lastUsage = make(map[string]any)
			}
			for k, v := range usage {
				(*lastUsage)[k] = v
			}
		}
		return
	}
}

// extractUsageFromBody 从非流式响应体中提取 usage
// 兼容 OpenAI 格式（顶层 usage 对象）和 Anthropic 格式（顶层 input_tokens/output_tokens）
func extractUsageFromBody(body []byte, provider, model string) {
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		return
	}

	// OpenAI 格式：顶层 usage 对象
	if usage, ok := resp["usage"].(map[string]any); ok {
		record := extractUsageFromMap(usage, provider, model)
		recordUsage(record)
		return
	}

	// Anthropic 格式：顶层 input_tokens / output_tokens
	if _, hasInput := resp["input_tokens"]; hasInput {
		record := extractUsageFromMap(resp, provider, model)
		recordUsage(record)
		return
	}
}

// flushUsageRecord 将最后一次提取到的 usage 写入 JSONL
func flushUsageRecord(lastUsage map[string]any, provider, model string) {
	if lastUsage == nil {
		return
	}
	record := extractUsageFromMap(lastUsage, provider, model)
	recordUsage(record)
}

// UsageDir 返回 usage 文件存储目录路径。
// 优先使用自定义目录（用于测试），否则返回 ~/.agr/usage/。
func UsageDir() string {
	if usageDir != "" {
		return usageDir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agr", "usage")
}
