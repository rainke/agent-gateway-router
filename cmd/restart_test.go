package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRestartCommandDefaultsToDaemon(t *testing.T) {
	originalConfigFile := configFile
	originalPort := port
	t.Cleanup(func() {
		configFile = originalConfigFile
		port = originalPort
	})

	pidFile := filepath.Join(t.TempDir(), "agr.pid")
	configPath := filepath.Join(t.TempDir(), "config.toml")
	configContents := "[server]\nport = 19898\npid_file = " + `"` + pidFile + `"` + "\n"
	if err := os.WriteFile(configPath, []byte(configContents), 0o600); err != nil {
		t.Fatalf("写入测试配置失败: %v", err)
	}

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
