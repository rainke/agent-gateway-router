package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestDailyFileWriter_SameDay verifies that multiple writes on the same
// calendar day all land in a single dated log file.
func TestDailyFileWriter_SameDay(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 6, 24, 10, 0, 0, 0, time.Local)
	w := newDailyFileWriter(dir, func() time.Time { return now })

	if _, err := w.Write([]byte("line1\n")); err != nil {
		t.Fatalf("第一次写入失败: %v", err)
	}
	if _, err := w.Write([]byte("line2\n")); err != nil {
		t.Fatalf("第二次写入失败: %v", err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("关闭失败: %v", err)
	}

	path := filepath.Join(dir, "2026-06-24.log")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取日志文件失败: %v", err)
	}
	want := "line1\nline2\n"
	if string(data) != want {
		t.Errorf("同一天写入内容不匹配: got %q, want %q", string(data), want)
	}

	// 不应产生其他日期的文件
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("期望只产生 1 个日志文件，实际 %d", len(entries))
	}
}

// TestDailyFileWriter_CrossesMidnight verifies that when the calendar day
// changes between writes, the writer closes the old file and opens a new
// dated file for the new day.
func TestDailyFileWriter_CrossesMidnight(t *testing.T) {
	dir := t.TempDir()
	current := time.Date(2026, 6, 24, 23, 59, 0, 0, time.Local)
	w := newDailyFileWriter(dir, func() time.Time { return current })

	if _, err := w.Write([]byte("day1\n")); err != nil {
		t.Fatalf("第一天写入失败: %v", err)
	}

	// 跨过午夜
	current = time.Date(2026, 6, 25, 0, 5, 0, 0, time.Local)

	if _, err := w.Write([]byte("day2-a\n")); err != nil {
		t.Fatalf("第二天第一次写入失败: %v", err)
	}
	if _, err := w.Write([]byte("day2-b\n")); err != nil {
		t.Fatalf("第二天第二次写入失败: %v", err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("关闭失败: %v", err)
	}

	// 第一天的文件
	d1, err := os.ReadFile(filepath.Join(dir, "2026-06-24.log"))
	if err != nil {
		t.Fatalf("读取第一天日志失败: %v", err)
	}
	if string(d1) != "day1\n" {
		t.Errorf("第一天日志内容不匹配: got %q, want %q", string(d1), "day1\n")
	}

	// 第二天的文件
	d2, err := os.ReadFile(filepath.Join(dir, "2026-06-25.log"))
	if err != nil {
		t.Fatalf("读取第二天日志失败: %v", err)
	}
	if string(d2) != "day2-a\nday2-b\n" {
		t.Errorf("第二天日志内容不匹配: got %q, want %q", string(d2), "day2-a\nday2-b\n")
	}
}

// TestDailyFileWriter_AppendsToExisting verifies that the writer appends
// to an already-existing dated log file rather than truncating it.
func TestDailyFileWriter_AppendsToExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "2026-06-24.log")
	if err := os.WriteFile(path, []byte("preexisting\n"), 0o644); err != nil {
		t.Fatalf("写入预置内容失败: %v", err)
	}

	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.Local)
	w := newDailyFileWriter(dir, func() time.Time { return now })

	if _, err := w.Write([]byte("appended\n")); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("关闭失败: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取日志失败: %v", err)
	}
	want := "preexisting\nappended\n"
	if string(data) != want {
		t.Errorf("追加写入内容不匹配: got %q, want %q", string(data), want)
	}
}

// TestDailyFileWriter_RealTimeRotation uses real wall-clock time to verify
// that the writer picks up the correct date and writes to the expected file.
func TestDailyFileWriter_RealTimeRotation(t *testing.T) {
	dir := t.TempDir()
	w := newDailyFileWriter(dir, time.Now)

	if _, err := w.Write([]byte("realtime\n")); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("关闭失败: %v", err)
	}

	expected := time.Now().Format("2006-01-02") + ".log"
	path := filepath.Join(dir, expected)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", expected, err)
	}
	if !strings.Contains(string(data), "realtime") {
		t.Errorf("实时写入内容缺失: got %q", string(data))
	}
}
