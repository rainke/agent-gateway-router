package proxy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func setupUsageTestDir(t *testing.T) string {
	t.Helper()
	prevDir := usageDir
	dir := filepath.Join("/tmp", "agr-usage-test", t.Name())
	os.RemoveAll(dir)
	usageDir = dir
	t.Cleanup(func() {
		usageDir = prevDir // 恢复全局值，不置空
		os.RemoveAll(dir)
	})
	return dir
}

func TestExtractUsageFromMap_OpenAI_FullFields(t *testing.T) {
	usage := map[string]any{
		"total_tokens":      float64(20332),
		"prompt_tokens":     float64(10333),
		"completion_tokens": float64(17888),
		"prompt_tokens_details": map[string]any{
			"cached_tokens": float64(8633),
		},
		"completion_tokens_details": map[string]any{
			"reasoning_tokens": float64(23444),
		},
	}

	record := extractUsageFromMap(usage, "deepseek", "deepseek-v4-flash")

	if record.Provider != "deepseek" {
		t.Errorf("Provider = %q, want %q", record.Provider, "deepseek")
	}
	if record.Model != "deepseek-v4-flash" {
		t.Errorf("Model = %q, want %q", record.Model, "deepseek-v4-flash")
	}
	if record.TotalTokens != 20332 {
		t.Errorf("TotalTokens = %d, want %d", record.TotalTokens, 20332)
	}
	if record.InputTokens != 10333 {
		t.Errorf("InputTokens = %d, want %d", record.InputTokens, 10333)
	}
	if record.CachedTokens != 8633 {
		t.Errorf("CachedTokens = %d, want %d", record.CachedTokens, 8633)
	}
	if record.OutputTokens != 17888 {
		t.Errorf("OutputTokens = %d, want %d", record.OutputTokens, 17888)
	}
	if record.OutputReasoningTokens != 23444 {
		t.Errorf("OutputReasoningTokens = %d, want %d", record.OutputReasoningTokens, 23444)
	}
	if record.OutputTextTokens != 0 {
		t.Errorf("OutputTextTokens = %d, want %d", record.OutputTextTokens, 0)
	}
}

func TestExtractUsageFromMap_OpenAI_PartialFields(t *testing.T) {
	usage := map[string]any{
		"total_tokens":      float64(500),
		"prompt_tokens":     float64(200),
		"completion_tokens": float64(300),
	}

	record := extractUsageFromMap(usage, "mimo", "mimo-v2.5-pro")

	if record.TotalTokens != 500 {
		t.Errorf("TotalTokens = %d, want %d", record.TotalTokens, 500)
	}
	if record.InputTokens != 200 {
		t.Errorf("InputTokens = %d, want %d", record.InputTokens, 200)
	}
	if record.OutputTokens != 300 {
		t.Errorf("OutputTokens = %d, want %d", record.OutputTokens, 300)
	}
	if record.OutputTextTokens != 300 {
		t.Errorf("OutputTextTokens = %d, want %d", record.OutputTextTokens, 300)
	}
}

func TestExtractUsageFromMap_Anthropic_Format(t *testing.T) {
	usage := map[string]any{
		"input_tokens":  float64(100),
		"output_tokens": float64(50),
	}

	record := extractUsageFromMap(usage, "minimax", "MiniMax-M3")

	if record.InputTokens != 100 {
		t.Errorf("InputTokens = %d, want %d", record.InputTokens, 100)
	}
	if record.OutputTokens != 50 {
		t.Errorf("OutputTokens = %d, want %d", record.OutputTokens, 50)
	}
	if record.TotalTokens != 150 {
		t.Errorf("TotalTokens = %d, want %d (auto-calculated)", record.TotalTokens, 150)
	}
}

func TestExtractUsageFromMap_Anthropic_WithCache(t *testing.T) {
	usage := map[string]any{
		"input_tokens":                float64(200),
		"output_tokens":               float64(100),
		"cache_creation_input_tokens": float64(50),
		"cache_read_input_tokens":     float64(80),
	}

	record := extractUsageFromMap(usage, "minimax", "MiniMax-M3")

	// record_input_tokens = input_tokens + cache_creation_input_tokens + cache_read_input_tokens = 200 + 50 + 80 = 330
	if record.InputTokens != 330 {
		t.Errorf("InputTokens = %d, want %d", record.InputTokens, 330)
	}
	// CachedTokens = cache_creation_input_tokens + cache_read_input_tokens = 50 + 80 = 130
	if record.CachedTokens != 130 {
		t.Errorf("CachedTokens = %d, want %d", record.CachedTokens, 130)
	}
	if record.OutputTokens != 100 {
		t.Errorf("OutputTokens = %d, want %d", record.OutputTokens, 100)
	}
	// total_tokens = InputTokens + OutputTokens = 330 + 100 = 430
	if record.TotalTokens != 430 {
		t.Errorf("TotalTokens = %d, want %d", record.TotalTokens, 430)
	}
}

func TestExtractUsageFromMap_EmptyMap(t *testing.T) {
	usage := map[string]any{}
	record := extractUsageFromMap(usage, "test", "model")

	if record.TotalTokens != 0 || record.InputTokens != 0 || record.OutputTokens != 0 {
		t.Errorf("expected all zeros for empty usage map, got %+v", record)
	}
}

func TestExtractUsageFromMap_WithReasoningTokens(t *testing.T) {
	usage := map[string]any{
		"total_tokens":      float64(1000),
		"prompt_tokens":     float64(400),
		"completion_tokens": float64(600),
		"completion_tokens_details": map[string]any{
			"reasoning_tokens": float64(200),
		},
	}

	record := extractUsageFromMap(usage, "deepseek", "deepseek-v4-flash")

	if record.OutputTokens != 600 {
		t.Errorf("OutputTokens = %d, want %d", record.OutputTokens, 600)
	}
	if record.OutputReasoningTokens != 200 {
		t.Errorf("OutputReasoningTokens = %d, want %d", record.OutputReasoningTokens, 200)
	}
	if record.OutputTextTokens != 400 {
		t.Errorf("OutputTextTokens = %d, want %d", record.OutputTextTokens, 400)
	}
}

func TestRecordUsage_WritesJSONL(t *testing.T) {
	dir := setupUsageTestDir(t)

	record := UsageRecord{
		Provider:              "deepseek",
		Model:                 "deepseek-v4-flash",
		TotalTokens:           20332,
		InputTokens:           10333,
		CachedTokens:          8633,
		OutputTokens:          17888,
		OutputReasoningTokens: 23444,
		OutputTextTokens:      0,
	}

	recordUsage(record)

	path := filepath.Join(dir, "2026-06-05.jsonl")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("打开文件失败: %v", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		t.Fatal("文件为空")
	}

	var got UsageRecord
	if err := json.Unmarshal(scanner.Bytes(), &got); err != nil {
		t.Fatalf("解析 JSONL 失败: %v", err)
	}

	if got.Provider != record.Provider {
		t.Errorf("Provider = %q, want %q", got.Provider, record.Provider)
	}
	if got.Model != record.Model {
		t.Errorf("Model = %q, want %q", got.Model, record.Model)
	}
	if got.TotalTokens != record.TotalTokens {
		t.Errorf("TotalTokens = %d, want %d", got.TotalTokens, record.TotalTokens)
	}
	if got.InputTokens != record.InputTokens {
		t.Errorf("InputTokens = %d, want %d", got.InputTokens, record.InputTokens)
	}
	if got.CachedTokens != record.CachedTokens {
		t.Errorf("CachedTokens = %d, want %d", got.CachedTokens, record.CachedTokens)
	}
	if got.OutputTokens != record.OutputTokens {
		t.Errorf("OutputTokens = %d, want %d", got.OutputTokens, record.OutputTokens)
	}
	if got.OutputReasoningTokens != record.OutputReasoningTokens {
		t.Errorf("OutputReasoningTokens = %d, want %d", got.OutputReasoningTokens, record.OutputReasoningTokens)
	}
}

func TestRecordUsage_SetsTimestamp(t *testing.T) {
	dir := setupUsageTestDir(t)

	recordUsage(UsageRecord{Provider: "test", Model: "m", TotalTokens: 100, InputTokens: 40, OutputTokens: 60})

	path := filepath.Join(dir, "2026-06-05.jsonl")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("文件未创建: %v", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		t.Fatal("文件为空")
	}
	var rec UsageRecord
	if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if rec.Timestamp == "" {
		t.Error("Timestamp 不应为空")
	}
}

func TestRecordUsage_AppendsMultipleRecords(t *testing.T) {
	dir := setupUsageTestDir(t)

	recordUsage(UsageRecord{Provider: "a", Model: "m1", TotalTokens: 100, InputTokens: 40, OutputTokens: 60})
	recordUsage(UsageRecord{Provider: "b", Model: "m2", TotalTokens: 200, InputTokens: 80, OutputTokens: 120})

	path := filepath.Join(dir, "2026-06-05.jsonl")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("打开文件失败: %v", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lines := 0
	for scanner.Scan() {
		lines++
		var rec UsageRecord
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			t.Errorf("第 %d 行解析失败: %v", lines, err)
		}
	}
	if lines != 2 {
		t.Errorf("行数 = %d, want 2", lines)
	}
}

func TestRecordUsage_SkipsZeroUsage(t *testing.T) {
	record := UsageRecord{
		Provider:     "test",
		Model:        "model",
		TotalTokens:  0,
		InputTokens:  0,
		OutputTokens: 0,
	}
	recordUsage(record)
}

func TestExtractAndRecordUsageFromChunk_OpenAI(t *testing.T) {
	var lastUsage map[string]any

	chunk1 := []byte(`{"choices":[{"delta":{"content":"hello"}}]}`)
	extractAndRecordUsageFromChunk(chunk1, "test", "model", &lastUsage)
	if lastUsage != nil {
		t.Error("非 usage chunk 不应设置 lastUsage")
	}

	chunk2 := []byte(`{"usage":{"total_tokens":100,"prompt_tokens":40,"completion_tokens":60}}`)
	extractAndRecordUsageFromChunk(chunk2, "test", "model", &lastUsage)
	if lastUsage == nil {
		t.Fatal("usage chunk 应设置 lastUsage")
	}
	if lastUsage["total_tokens"].(float64) != 100 {
		t.Errorf("total_tokens = %v, want 100", lastUsage["total_tokens"])
	}
}

func TestExtractAndRecordUsageFromChunk_Anthropic_Streaming(t *testing.T) {
	var lastUsage map[string]any

	startChunk := []byte(`{"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":100}}}`)
	extractAndRecordUsageFromChunk(startChunk, "minimax", "MiniMax-M3", &lastUsage)
	if lastUsage == nil {
		t.Fatal("message_start 应设置 lastUsage")
	}
	if lastUsage["input_tokens"].(float64) != 100 {
		t.Errorf("input_tokens = %v, want 100", lastUsage["input_tokens"])
	}

	deltaChunk := []byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":50}}`)
	extractAndRecordUsageFromChunk(deltaChunk, "minimax", "MiniMax-M3", &lastUsage)
	if lastUsage["output_tokens"].(float64) != 50 {
		t.Errorf("output_tokens = %v, want 50", lastUsage["output_tokens"])
	}
	if lastUsage["input_tokens"].(float64) != 100 {
		t.Errorf("input_tokens 应保留 = %v, want 100", lastUsage["input_tokens"])
	}
}

func TestExtractUsageFromBody_OpenAI(t *testing.T) {
	dir := setupUsageTestDir(t)

	body := []byte(`{"choices":[{"message":{"content":"hi"}}],"usage":{"total_tokens":100,"prompt_tokens":40,"completion_tokens":60}}`)
	extractUsageFromBody(body, "deepseek", "deepseek-v4-flash")

	path := filepath.Join(dir, "2026-06-05.jsonl")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("文件未创建: %v", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		t.Fatal("文件为空")
	}
	var rec UsageRecord
	if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if rec.InputTokens != 40 || rec.OutputTokens != 60 {
		t.Errorf("got input=%d output=%d, want 40/60", rec.InputTokens, rec.OutputTokens)
	}
}

func TestExtractUsageFromBody_Anthropic(t *testing.T) {
	dir := setupUsageTestDir(t)

	body := []byte(`{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":100,"output_tokens":50}}`)
	extractUsageFromBody(body, "minimax", "MiniMax-M3")

	path := filepath.Join(dir, "2026-06-05.jsonl")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("文件未创建: %v", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		t.Fatal("文件为空")
	}
	var rec UsageRecord
	if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if rec.InputTokens != 100 || rec.OutputTokens != 50 {
		t.Errorf("got input=%d output=%d, want 100/50", rec.InputTokens, rec.OutputTokens)
	}
	if rec.TotalTokens != 150 {
		t.Errorf("TotalTokens = %d, want 150", rec.TotalTokens)
	}
}

func TestFlushUsageRecord_NilUsage(t *testing.T) {
	flushUsageRecord(nil, "test", "model")
}

func TestFlushUsageRecord_WithUsage(t *testing.T) {
	dir := setupUsageTestDir(t)

	usage := map[string]any{
		"total_tokens":      float64(100),
		"prompt_tokens":     float64(40),
		"completion_tokens": float64(60),
	}
	flushUsageRecord(usage, "test-provider", "test-model")

	path := filepath.Join(dir, "2026-06-05.jsonl")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("文件未创建: %v", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		t.Fatal("文件为空")
	}
	var rec UsageRecord
	if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if rec.Provider != "test-provider" {
		t.Errorf("Provider = %q, want %q", rec.Provider, "test-provider")
	}
}

func TestFlushUsageRecord_Anthropic_Merged(t *testing.T) {
	dir := setupUsageTestDir(t)

	usage := map[string]any{
		"input_tokens":  float64(100),
		"output_tokens": float64(50),
	}
	flushUsageRecord(usage, "minimax", "MiniMax-M3")

	path := filepath.Join(dir, "2026-06-05.jsonl")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("文件未创建: %v", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		t.Fatal("文件为空")
	}
	var rec UsageRecord
	if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if rec.InputTokens != 100 {
		t.Errorf("InputTokens = %d, want 100", rec.InputTokens)
	}
	if rec.OutputTokens != 50 {
		t.Errorf("OutputTokens = %d, want 50", rec.OutputTokens)
	}
	if rec.TotalTokens != 150 {
		t.Errorf("TotalTokens = %d, want 150", rec.TotalTokens)
	}
}

func TestExtractUsageFromMap_JSONRoundTrip(t *testing.T) {
	record := UsageRecord{
		Timestamp:             "2026-06-05T16:01:36+08:00",
		Provider:              "deepseek",
		Model:                 "deepseek-v4-flash",
		TotalTokens:           20332,
		InputTokens:           10333,
		CachedTokens:          8633,
		OutputTokens:          17888,
		OutputReasoningTokens: 23444,
		OutputTextTokens:      3352,
	}

	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("Marshal 失败: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("Unmarshal 失败: %v", err)
	}

	expected := map[string]string{
		"timestamp":               "2026-06-05T16:01:36+08:00",
		"provider":                "deepseek",
		"model":                   "deepseek-v4-flash",
		"total_tokens":            "20332",
		"input_tokens":            "10333",
		"cached_tokens":           "8633",
		"output_tokens":           "17888",
		"output_reasoning_tokens": "23444",
		"output_text_tokens":      "3352",
	}

	for key, want := range expected {
		got := m[key]
		if fmt.Sprintf("%v", got) != want {
			t.Errorf("JSON 字段 %q = %v, want %s", key, got, want)
		}
	}
}
