package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

func TestRestartCommandDefaultsToDaemon(t *testing.T) {
	preserveRestartFlagValues(t)
	configPath, _ := writeRestartTestConfig(t)

	daemonStarted := false
	cmd := newRestartCommand(func() error {
		daemonStarted = true
		return nil
	})
	if flag := cmd.Flags().Lookup("daemon"); flag != nil {
		t.Fatal("restart 不应提供 --daemon/-d 参数")
	}
	if err := cmd.Flags().Set("config", configPath); err != nil {
		t.Fatalf("设置配置参数失败: %v", err)
	}

	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("restart 执行失败: %v", err)
	}
	if !daemonStarted {
		t.Fatal("restart 默认应启动后台进程")
	}
}

func TestRestartCommandStopsRunningProcessBeforeStartingDaemon(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 不支持测试辅助进程的 SIGTERM 退出方式")
	}
	preserveRestartFlagValues(t)
	configPath, pidFile := writeRestartTestConfig(t)

	helper := exec.Command(os.Args[0], "-test.run=^TestRestartHelperProcess$")
	helper.Env = append(os.Environ(), "AGR_RESTART_HELPER_PROCESS=1")
	if err := helper.Start(); err != nil {
		t.Fatalf("启动测试辅助进程失败: %v", err)
	}
	waitCh := make(chan error, 1)
	go func() {
		waitCh <- helper.Wait()
	}()
	helperWaited := false
	t.Cleanup(func() {
		if helperWaited {
			return
		}
		_ = helper.Process.Kill()
		<-waitCh
	})

	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(helper.Process.Pid)), 0o600); err != nil {
		t.Fatalf("写入测试 PID 文件失败: %v", err)
	}

	daemonStarted := false
	cmd := newRestartCommand(func() error {
		daemonStarted = true
		return nil
	})
	if err := cmd.Flags().Set("config", configPath); err != nil {
		t.Fatalf("设置配置参数失败: %v", err)
	}

	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("restart 执行失败: %v", err)
	}
	if !daemonStarted {
		t.Fatal("停止现有进程后应启动后台进程")
	}
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Fatalf("停止现有进程后应删除 PID 文件，stat 错误: %v", err)
	}

	select {
	case <-waitCh:
		helperWaited = true
	case <-time.After(5 * time.Second):
		t.Fatal("测试辅助进程未退出")
	}
}

func TestRestartCommandPropagatesDaemonStartError(t *testing.T) {
	preserveRestartFlagValues(t)
	configPath, _ := writeRestartTestConfig(t)
	wantErr := errors.New("daemon start failed")
	cmd := newRestartCommand(func() error {
		return wantErr
	})
	if err := cmd.Flags().Set("config", configPath); err != nil {
		t.Fatalf("设置配置参数失败: %v", err)
	}

	if err := cmd.RunE(cmd, nil); !errors.Is(err, wantErr) {
		t.Fatalf("restart 错误 = %v，期望 %v", err, wantErr)
	}
}

func TestRestartCommandDoesNotStartDaemonWhenConfigIsInvalid(t *testing.T) {
	preserveRestartFlagValues(t)
	daemonStarted := false
	cmd := newRestartCommand(func() error {
		daemonStarted = true
		return nil
	})
	if err := cmd.Flags().Set("config", filepath.Join(t.TempDir(), "missing.toml")); err != nil {
		t.Fatalf("设置配置参数失败: %v", err)
	}

	if err := cmd.RunE(cmd, nil); err == nil {
		t.Fatal("配置文件不存在时 restart 应返回错误")
	}
	if daemonStarted {
		t.Fatal("配置文件无效时不应启动后台进程")
	}
}

func TestRestartHelperProcess(t *testing.T) {
	if os.Getenv("AGR_RESTART_HELPER_PROCESS") != "1" {
		return
	}
	for {
		time.Sleep(time.Hour)
	}
}

func preserveRestartFlagValues(t *testing.T) {
	t.Helper()
	originalConfigFile := configFile
	originalPort := port
	t.Cleanup(func() {
		configFile = originalConfigFile
		port = originalPort
	})
}

func writeRestartTestConfig(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "agr.pid")
	configPath := filepath.Join(dir, "config.toml")
	configContents := fmt.Sprintf("[server]\nport = 19898\npid_file = %q\n", pidFile)
	if err := os.WriteFile(configPath, []byte(configContents), 0o600); err != nil {
		t.Fatalf("写入测试配置失败: %v", err)
	}
	return configPath, pidFile
}
