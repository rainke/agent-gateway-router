package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agr/proxy"
)

// --- helpers ---

func writeJSONL(t *testing.T, dir, date string, records []proxy.UsageRecord) {
	t.Helper()
	os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, date+".jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("创建文件 %s 失败: %v", path, err)
	}
	defer f.Close()
	for _, r := range records {
		line, _ := json.Marshal(r)
		f.Write(line)
		f.WriteString("\n")
	}
}

func makeRecord(provider, model string, input, output, reasoning, cached, total int) proxy.UsageRecord {
	return proxy.UsageRecord{
		Provider:              provider,
		Model:                 model,
		InputTokens:           input,
		OutputTokens:          output,
		OutputReasoningTokens: reasoning,
		CachedTokens:          cached,
		TotalTokens:           total,
	}
}

// --- Date parsing tests ---

func TestParseUsageDateArg_Empty(t *testing.T) {
	start, end, err := parseUsageDateArg("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	today := time.Now().In(time.Local)
	expected := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.Local)
	if !start.Equal(expected) {
		t.Errorf("start = %v, want %v", start, expected)
	}
	if !end.Equal(expected) {
		t.Errorf("end = %v, want %v", end, expected)
	}
}

func TestParseUsageDateArg_SingleDate(t *testing.T) {
	start, end, err := parseUsageDateArg("2026-06-08")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := time.Date(2026, 6, 8, 0, 0, 0, 0, time.Local)
	if !start.Equal(expected) {
		t.Errorf("start = %v, want %v", start, expected)
	}
	if !end.Equal(expected) {
		t.Errorf("end = %v, want %v", end, expected)
	}
}

func TestParseUsageDateArg_Range(t *testing.T) {
	start, end, err := parseUsageDateArg("2026-06-05:2026-06-09")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expectedStart := time.Date(2026, 6, 5, 0, 0, 0, 0, time.Local)
	expectedEnd := time.Date(2026, 6, 9, 0, 0, 0, 0, time.Local)
	if !start.Equal(expectedStart) {
		t.Errorf("start = %v, want %v", start, expectedStart)
	}
	if !end.Equal(expectedEnd) {
		t.Errorf("end = %v, want %v", end, expectedEnd)
	}
}

func TestParseUsageDateArg_SameStartEnd(t *testing.T) {
	start, end, err := parseUsageDateArg("2026-06-05:2026-06-05")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !start.Equal(end) {
		t.Errorf("start %v != end %v for same-day range", start, end)
	}
}

func TestParseUsageDateArg_InvalidDate(t *testing.T) {
	_, _, err := parseUsageDateArg("not-a-date")
	if err == nil {
		t.Fatal("expected error for invalid date")
	}
	if !strings.Contains(err.Error(), "无效的日期格式") {
		t.Errorf("error = %q, want 包含 '无效的日期格式'", err.Error())
	}
}

func TestParseUsageDateArg_InvalidRangeStart(t *testing.T) {
	_, _, err := parseUsageDateArg("bad:2026-06-09")
	if err == nil {
		t.Fatal("expected error for invalid range start")
	}
	if !strings.Contains(err.Error(), "无效的起始日期格式") {
		t.Errorf("error = %q, want 包含 '无效的起始日期格式'", err.Error())
	}
}

func TestParseUsageDateArg_InvalidRangeEnd(t *testing.T) {
	_, _, err := parseUsageDateArg("2026-06-05:bad")
	if err == nil {
		t.Fatal("expected error for invalid range end")
	}
	if !strings.Contains(err.Error(), "无效的结束日期格式") {
		t.Errorf("error = %q, want 包含 '无效的结束日期格式'", err.Error())
	}
}

func TestParseUsageDateArg_StartAfterEnd(t *testing.T) {
	_, _, err := parseUsageDateArg("2026-06-09:2026-06-05")
	if err == nil {
		t.Fatal("expected error for start > end")
	}
	if !strings.Contains(err.Error(), "不能晚于结束日期") {
		t.Errorf("error = %q, want 包含 '不能晚于结束日期'", err.Error())
	}
}

// --- Aggregation tests ---

func TestAggregateUsage_NonExistentDir(t *testing.T) {
	summaries, err := aggregateUsage("/tmp/nonexistent-dir-xyz-123", time.Now(), time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summaries != nil {
		t.Errorf("expected nil, got %d summaries", len(summaries))
	}
}

func TestAggregateUsage_SingleRecord(t *testing.T) {
	dir := t.TempDir()
	writeJSONL(t, dir, "2026-06-09", []proxy.UsageRecord{
		makeRecord("minimax", "MiniMax-M3", 100, 50, 0, 20, 150),
	})

	start := time.Date(2026, 6, 9, 0, 0, 0, 0, time.Local)
	summaries, err := aggregateUsage(dir, start, start)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(summaries))
	}
	s := summaries[0]
	if s.Provider != "minimax" || s.Model != "MiniMax-M3" {
		t.Errorf("Provider/Model = %s/%s, want minimax/MiniMax-M3", s.Provider, s.Model)
	}
	if s.Requests != 1 {
		t.Errorf("Requests = %d, want 1", s.Requests)
	}
	if s.Input != 100 {
		t.Errorf("Input = %d, want 100", s.Input)
	}
	if s.Output != 50 {
		t.Errorf("Output = %d, want 50", s.Output)
	}
	if s.Reasoning != 0 {
		t.Errorf("Reasoning = %d, want 0", s.Reasoning)
	}
	if s.Cached != 20 {
		t.Errorf("Cached = %d, want 20", s.Cached)
	}
	if s.Total != 150 {
		t.Errorf("Total = %d, want 150", s.Total)
	}
}

func TestAggregateUsage_MultipleSameProvider(t *testing.T) {
	dir := t.TempDir()
	writeJSONL(t, dir, "2026-06-09", []proxy.UsageRecord{
		makeRecord("minimax", "MiniMax-M3", 100, 50, 0, 20, 150),
		makeRecord("minimax", "MiniMax-M3", 200, 80, 0, 40, 280),
		makeRecord("minimax", "MiniMax-M3", 50, 20, 0, 10, 70),
	})

	start := time.Date(2026, 6, 9, 0, 0, 0, 0, time.Local)
	summaries, err := aggregateUsage(dir, start, start)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(summaries))
	}
	s := summaries[0]
	if s.Requests != 3 {
		t.Errorf("Requests = %d, want 3", s.Requests)
	}
	if s.Input != 350 {
		t.Errorf("Input = %d, want 350", s.Input)
	}
	if s.Output != 150 {
		t.Errorf("Output = %d, want 150", s.Output)
	}
	if s.Cached != 70 {
		t.Errorf("Cached = %d, want 70", s.Cached)
	}
	if s.Total != 500 {
		t.Errorf("Total = %d, want 500", s.Total)
	}
}

func TestAggregateUsage_MultipleProviders(t *testing.T) {
	dir := t.TempDir()
	writeJSONL(t, dir, "2026-06-09", []proxy.UsageRecord{
		makeRecord("minimax", "MiniMax-M3", 100, 50, 0, 20, 150),
		makeRecord("deepseek", "deepseek-v4-flash", 200, 80, 30, 40, 280),
		makeRecord("mimo-anthropic", "mimo-v2.5-pro", 50, 20, 0, 10, 70),
	})

	start := time.Date(2026, 6, 9, 0, 0, 0, 0, time.Local)
	summaries, err := aggregateUsage(dir, start, start)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summaries) != 3 {
		t.Fatalf("expected 3 summaries, got %d", len(summaries))
	}
	// Should be sorted by provider name
	if summaries[0].Provider != "deepseek" {
		t.Errorf("summary[0].Provider = %q, want deepseek", summaries[0].Provider)
	}
	if summaries[1].Provider != "mimo-anthropic" {
		t.Errorf("summary[1].Provider = %q, want mimo-anthropic", summaries[1].Provider)
	}
	if summaries[2].Provider != "minimax" {
		t.Errorf("summary[2].Provider = %q, want minimax", summaries[2].Provider)
	}
}

func TestAggregateUsage_MultiDayRange(t *testing.T) {
	dir := t.TempDir()
	writeJSONL(t, dir, "2026-06-07", []proxy.UsageRecord{
		makeRecord("minimax", "MiniMax-M3", 100, 50, 0, 20, 150),
	})
	writeJSONL(t, dir, "2026-06-08", []proxy.UsageRecord{
		makeRecord("minimax", "MiniMax-M3", 200, 80, 0, 40, 280),
	})
	writeJSONL(t, dir, "2026-06-09", []proxy.UsageRecord{
		makeRecord("minimax", "MiniMax-M3", 50, 20, 0, 10, 70),
	})

	start := time.Date(2026, 6, 7, 0, 0, 0, 0, time.Local)
	end := time.Date(2026, 6, 9, 0, 0, 0, 0, time.Local)
	summaries, err := aggregateUsage(dir, start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(summaries))
	}
	s := summaries[0]
	if s.Requests != 3 {
		t.Errorf("Requests = %d, want 3", s.Requests)
	}
	if s.Input != 350 {
		t.Errorf("Input = %d, want 350", s.Input)
	}
	if s.Total != 500 {
		t.Errorf("Total = %d, want 500", s.Total)
	}
}

func TestAggregateUsage_MissingDayFile(t *testing.T) {
	dir := t.TempDir()
	// Only create 06-07 and 06-09, skip 06-08
	writeJSONL(t, dir, "2026-06-07", []proxy.UsageRecord{
		makeRecord("p", "m", 100, 50, 0, 0, 150),
	})
	writeJSONL(t, dir, "2026-06-09", []proxy.UsageRecord{
		makeRecord("p", "m", 200, 80, 0, 0, 280),
	})

	start := time.Date(2026, 6, 7, 0, 0, 0, 0, time.Local)
	end := time.Date(2026, 6, 9, 0, 0, 0, 0, time.Local)
	summaries, err := aggregateUsage(dir, start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(summaries))
	}
	if summaries[0].Requests != 2 {
		t.Errorf("Requests = %d, want 2 (missing day should be skipped)", summaries[0].Requests)
	}
	if summaries[0].Input != 300 {
		t.Errorf("Input = %d, want 300", summaries[0].Input)
	}
}

func TestAggregateUsage_EmptyJSONL(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, "2026-06-09.jsonl")
	os.WriteFile(path, []byte(""), 0o644)

	start := time.Date(2026, 6, 9, 0, 0, 0, 0, time.Local)
	summaries, err := aggregateUsage(dir, start, start)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summaries) != 0 {
		t.Errorf("expected 0 summaries for empty file, got %d", len(summaries))
	}
}

func TestAggregateUsage_InvalidJSONLines(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, "2026-06-09.jsonl")
	content := "not json\n" +
		"{\"provider\":\"p\",\"model\":\"m\",\"input_tokens\":100,\"output_tokens\":50,\"total_tokens\":150}\n" +
		"also bad\n"
	os.WriteFile(path, []byte(content), 0o644)

	start := time.Date(2026, 6, 9, 0, 0, 0, 0, time.Local)
	summaries, err := aggregateUsage(dir, start, start)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("expected 1 summary (invalid lines skipped), got %d", len(summaries))
	}
	if summaries[0].Requests != 1 {
		t.Errorf("Requests = %d, want 1", summaries[0].Requests)
	}
}

func TestAggregateUsage_SortByProviderThenModel(t *testing.T) {
	dir := t.TempDir()
	writeJSONL(t, dir, "2026-06-09", []proxy.UsageRecord{
		makeRecord("zeta", "model-a", 10, 5, 0, 0, 15),
		makeRecord("alpha", "model-b", 10, 5, 0, 0, 15),
		makeRecord("alpha", "model-a", 10, 5, 0, 0, 15),
		makeRecord("zeta", "model-b", 10, 5, 0, 0, 15),
	})

	start := time.Date(2026, 6, 9, 0, 0, 0, 0, time.Local)
	summaries, err := aggregateUsage(dir, start, start)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summaries) != 4 {
		t.Fatalf("expected 4 summaries, got %d", len(summaries))
	}

	expected := [][2]string{
		{"alpha", "model-a"},
		{"alpha", "model-b"},
		{"zeta", "model-a"},
		{"zeta", "model-b"},
	}
	for i, want := range expected {
		if summaries[i].Provider != want[0] || summaries[i].Model != want[1] {
			t.Errorf("summaries[%d] = %s/%s, want %s/%s",
				i, summaries[i].Provider, summaries[i].Model, want[0], want[1])
		}
	}
}

func TestAggregateUsage_WithReasoningTokens(t *testing.T) {
	dir := t.TempDir()
	writeJSONL(t, dir, "2026-06-09", []proxy.UsageRecord{
		makeRecord("deepseek", "deepseek-v4-flash", 400, 200, 100, 50, 600),
	})

	start := time.Date(2026, 6, 9, 0, 0, 0, 0, time.Local)
	summaries, err := aggregateUsage(dir, start, start)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := summaries[0]
	if s.Reasoning != 100 {
		t.Errorf("Reasoning = %d, want 100", s.Reasoning)
	}
}

// --- Number formatting tests ---

func TestFormatNumber(t *testing.T) {
	tests := []struct {
		input int
		want  string
	}{
		{0, "0"},
		{1, "1"},
		{99, "99"},
		{100, "100"},
		{999, "999"},
		{1000, "1,000"},
		{1234, "1,234"},
		{12345, "12,345"},
		{123456, "123,456"},
		{1234567, "1,234,567"},
		{12345678, "12,345,678"},
		{285340, "285,340"},
		{1200000, "1,200,000"},
	}

	for _, tt := range tests {
		got := formatNumber(tt.input)
		if got != tt.want {
			t.Errorf("formatNumber(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// --- Table rendering tests ---

func TestRenderUsageTable_NoTTY(t *testing.T) {
	summaries := []UsageSummary{
		{Provider: "minimax", Model: "MiniMax-M3", Requests: 42, Input: 285340, Output: 8230, Reasoning: 0, Cached: 198420, Total: 293570},
		{Provider: "deepseek", Model: "deepseek-v4-flash", Requests: 8, Input: 42100, Output: 6500, Reasoning: 2100, Cached: 18000, Total: 48600},
	}

	start := time.Date(2026, 6, 9, 0, 0, 0, 0, time.Local)
	output := renderUsageTable(summaries, start, start, false)

	// Should NOT contain ANSI escape codes
	if strings.Contains(output, "\033[") {
		t.Error("non-TTY output should not contain ANSI escape codes")
	}

	// Should contain title
	if !strings.Contains(output, "agr usage · 2026-06-09") {
		t.Error("output should contain title with date")
	}

	// Should contain headers
	if !strings.Contains(output, "Provider") || !strings.Contains(output, "Model") {
		t.Error("output should contain column headers")
	}

	// Should contain data
	if !strings.Contains(output, "minimax") || !strings.Contains(output, "MiniMax-M3") {
		t.Error("output should contain provider and model names")
	}

	// Should contain formatted numbers
	if !strings.Contains(output, "285,340") {
		t.Error("output should contain formatted numbers with commas")
	}

	// Should contain total row
	if !strings.Contains(output, "Total") {
		t.Error("output should contain Total row")
	}

	// Should contain separator lines
	if !strings.Contains(output, "─") {
		t.Error("output should contain separator lines with ─")
	}
}

func TestRenderUsageTable_TTY(t *testing.T) {
	summaries := []UsageSummary{
		{Provider: "test", Model: "model", Requests: 1, Input: 100, Output: 50, Reasoning: 0, Cached: 10, Total: 150},
	}

	start := time.Date(2026, 6, 9, 0, 0, 0, 0, time.Local)
	output := renderUsageTable(summaries, start, start, true)

	// Should contain ANSI escape codes
	if !strings.Contains(output, "\033[36m") {
		t.Error("TTY output should contain Cyan color for title")
	}
	if !strings.Contains(output, "\033[1m") {
		t.Error("TTY output should contain Bold for headers and total")
	}
	if !strings.Contains(output, "\033[2m") {
		t.Error("TTY output should contain Dim for separator lines")
	}
	if !strings.Contains(output, "\033[0m") {
		t.Error("TTY output should contain reset codes")
	}
}

func TestRenderUsageTable_RangeTitle(t *testing.T) {
	summaries := []UsageSummary{
		{Provider: "p", Model: "m", Requests: 1, Input: 100, Output: 50, Reasoning: 0, Cached: 0, Total: 150},
	}

	start := time.Date(2026, 6, 5, 0, 0, 0, 0, time.Local)
	end := time.Date(2026, 6, 9, 0, 0, 0, 0, time.Local)
	output := renderUsageTable(summaries, start, end, false)

	if !strings.Contains(output, "2026-06-05 ~ 2026-06-09") {
		t.Errorf("range title should contain '2026-06-05 ~ 2026-06-09', got %q", output)
	}
}

func TestRenderUsageTable_TotalRow(t *testing.T) {
	summaries := []UsageSummary{
		{Provider: "a", Model: "m1", Requests: 10, Input: 1000, Output: 500, Reasoning: 100, Cached: 200, Total: 1500},
		{Provider: "b", Model: "m2", Requests: 20, Input: 2000, Output: 800, Reasoning: 50, Cached: 300, Total: 2800},
	}

	start := time.Date(2026, 6, 9, 0, 0, 0, 0, time.Local)
	output := renderUsageTable(summaries, start, start, false)

	// Total row should show summed values
	if !strings.Contains(output, "30") { // 10 + 20 requests
		t.Error("Total row should show total requests (30)")
	}
	if !strings.Contains(output, "3,000") { // 1000 + 2000 input
		t.Error("Total row should show total input (3,000)")
	}
	if !strings.Contains(output, "1,300") { // 500 + 800 output
		t.Error("Total row should show total output (1,300)")
	}
	if !strings.Contains(output, "150") { // 100 + 50 reasoning
		t.Error("Total row should show total reasoning (150)")
	}
	if !strings.Contains(output, "500") { // 200 + 300 cached
		t.Error("Total row should show total cached (500)")
	}
	if !strings.Contains(output, "4,300") { // 1500 + 2800 total
		t.Error("Total row should show grand total (4,300)")
	}
}

func TestRenderUsageTable_AllZeroTokens(t *testing.T) {
	summaries := []UsageSummary{
		{Provider: "p", Model: "m", Requests: 1, Input: 0, Output: 0, Reasoning: 0, Cached: 0, Total: 0},
	}

	start := time.Date(2026, 6, 9, 0, 0, 0, 0, time.Local)
	output := renderUsageTable(summaries, start, start, false)

	// Should render without errors, with zeros
	if !strings.Contains(output, "p") || !strings.Contains(output, "m") {
		t.Error("should contain provider and model even with zero tokens")
	}
}

// --- End-to-end: parse + aggregate + render ---

func TestUsageCommand_Integration(t *testing.T) {
	dir := t.TempDir()
	writeJSONL(t, dir, "2026-06-09", []proxy.UsageRecord{
		makeRecord("minimax", "MiniMax-M3", 285340, 8230, 0, 198420, 293570),
		makeRecord("minimax", "MiniMax-M3", 100, 50, 0, 20, 150),
		makeRecord("deepseek", "deepseek-v4-flash", 42100, 6500, 2100, 18000, 48600),
	})

	start := time.Date(2026, 6, 9, 0, 0, 0, 0, time.Local)
	summaries, err := aggregateUsage(dir, start, start)
	if err != nil {
		t.Fatalf("aggregateUsage error: %v", err)
	}

	if len(summaries) != 2 {
		t.Fatalf("expected 2 summaries, got %d", len(summaries))
	}

	// Verify aggregation
	if summaries[0].Provider != "deepseek" {
		t.Errorf("first provider = %q, want deepseek (sorted)", summaries[0].Provider)
	}
	if summaries[0].Requests != 1 {
		t.Errorf("deepseek Requests = %d, want 1", summaries[0].Requests)
	}
	if summaries[1].Provider != "minimax" {
		t.Errorf("second provider = %q, want minimax", summaries[1].Provider)
	}
	if summaries[1].Requests != 2 {
		t.Errorf("minimax Requests = %d, want 2", summaries[1].Requests)
	}
	if summaries[1].Input != 285440 {
		t.Errorf("minimax Input = %d, want 285440 (285340+100)", summaries[1].Input)
	}

	// Render non-TTY
	output := renderUsageTable(summaries, start, start, false)
	if strings.Contains(output, "\033[") {
		t.Error("non-TTY should not have ANSI codes")
	}
	if !strings.Contains(output, "agr usage · 2026-06-09") {
		t.Error("should have title")
	}
	if !strings.Contains(output, "minimax") || !strings.Contains(output, "deepseek") {
		t.Error("should have both providers")
	}
}

func TestUsageCommand_EmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, 6, 9, 0, 0, 0, 0, time.Local)
	summaries, err := aggregateUsage(dir, start, start)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summaries) != 0 {
		t.Errorf("expected 0 summaries for empty dir, got %d", len(summaries))
	}
}
