package process

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// WritePID 写入 PID 文件
func WritePID(pidFile string) error {
	dir := filepath.Dir(pidFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建 PID 文件目录失败: %w", err)
	}

	pid := os.Getpid()
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(pid)), 0644); err != nil {
		return fmt.Errorf("写入 PID 文件失败: %w", err)
	}
	return nil
}

// ReadPID 读取 PID 文件中的进程 ID
func ReadPID(pidFile string) (int, error) {
	data, err := os.ReadFile(pidFile)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, fmt.Errorf("PID 文件不存在: %s，agr 可能未在运行", pidFile)
		}
		return 0, fmt.Errorf("读取 PID 文件失败: %w", err)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("PID 文件内容无效: %w", err)
	}
	return pid, nil
}

// RemovePID 删除 PID 文件
func RemovePID(pidFile string) error {
	if err := os.Remove(pidFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除 PID 文件失败: %w", err)
	}
	return nil
}

// IsRunning 检查指定 PID 的进程是否在运行
func IsRunning(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// 发送信号 0 检查进程是否存在
	err = process.Signal(syscall.Signal(0))
	return err == nil
}

// StopProcess 向指定 PID 发送终止信号
func StopProcess(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("查找进程失败: %w", err)
	}

	// 发送 SIGTERM 信号，让进程优雅退出
	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("发送停止信号失败: %w", err)
	}
	return nil
}

// CheckStale 检查 PID 文件是否过期（进程已不存在）
func CheckStale(pidFile string) (bool, int) {
	pid, err := ReadPID(pidFile)
	if err != nil {
		return false, 0
	}
	if !IsRunning(pid) {
		return true, pid
	}
	return false, pid
}
