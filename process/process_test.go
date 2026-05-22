package process

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestWritePID_And_ReadPID(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "test.pid")

	err := WritePID(pidFile)
	if err != nil {
		t.Fatalf("WritePID 失败: %v", err)
	}

	// 验证文件存在
	if _, err := os.Stat(pidFile); os.IsNotExist(err) {
		t.Fatal("PID 文件未创建")
	}

	// 读取 PID
	pid, err := ReadPID(pidFile)
	if err != nil {
		t.Fatalf("ReadPID 失败: %v", err)
	}

	if pid != os.Getpid() {
		t.Errorf("PID 期望 %d，实际 %d", os.Getpid(), pid)
	}
}

func TestWritePID_CreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "subdir", "nested", "test.pid")

	err := WritePID(pidFile)
	if err != nil {
		t.Fatalf("WritePID 创建嵌套目录失败: %v", err)
	}

	if _, err := os.Stat(pidFile); os.IsNotExist(err) {
		t.Fatal("PID 文件未创建")
	}
}

func TestReadPID_FileNotExist(t *testing.T) {
	_, err := ReadPID("/nonexistent/path/test.pid")
	if err == nil {
		t.Fatal("期望读取不存在的 PID 文件时返回错误")
	}
}

func TestReadPID_InvalidContent(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "test.pid")
	os.WriteFile(pidFile, []byte("not-a-number"), 0644)

	_, err := ReadPID(pidFile)
	if err == nil {
		t.Fatal("期望 PID 文件内容无效时返回错误")
	}
}

func TestRemovePID(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "test.pid")
	os.WriteFile(pidFile, []byte("12345"), 0644)

	err := RemovePID(pidFile)
	if err != nil {
		t.Fatalf("RemovePID 失败: %v", err)
	}

	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Fatal("PID 文件未被删除")
	}
}

func TestRemovePID_FileNotExist(t *testing.T) {
	err := RemovePID("/nonexistent/path/test.pid")
	if err != nil {
		t.Fatalf("删除不存在的 PID 文件不应返回错误: %v", err)
	}
}

func TestIsRunning_CurrentProcess(t *testing.T) {
	// 当前进程应该在运行
	if !IsRunning(os.Getpid()) {
		t.Error("当前进程应该被检测为运行中")
	}
}

func TestIsRunning_NonexistentProcess(t *testing.T) {
	// 使用一个极大的 PID，几乎不可能存在
	if IsRunning(999999999) {
		t.Error("不存在的进程不应被检测为运行中")
	}
}

func TestCheckStale_FileNotExist(t *testing.T) {
	stale, pid := CheckStale("/nonexistent/path/test.pid")
	if stale {
		t.Error("文件不存在时不应返回 stale")
	}
	if pid != 0 {
		t.Errorf("文件不存在时 PID 应为 0，实际 %d", pid)
	}
}

func TestCheckStale_ProcessRunning(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "test.pid")
	// 写入当前进程的 PID
	os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0644)

	stale, pid := CheckStale(pidFile)
	if stale {
		t.Error("当前进程运行中，不应返回 stale")
	}
	if pid != os.Getpid() {
		t.Errorf("PID 期望 %d，实际 %d", os.Getpid(), pid)
	}
}

func TestCheckStale_ProcessNotRunning(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "test.pid")
	// 写入一个不存在的 PID
	os.WriteFile(pidFile, []byte("999999999"), 0644)

	stale, pid := CheckStale(pidFile)
	if !stale {
		t.Error("进程不存在时应返回 stale")
	}
	if pid != 999999999 {
		t.Errorf("PID 期望 999999999，实际 %d", pid)
	}
}

func TestStopProcess_NonexistentProcess(t *testing.T) {
	err := StopProcess(999999999)
	if err == nil {
		t.Error("期望向不存在的进程发送信号时返回错误")
	}
}
