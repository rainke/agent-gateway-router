package cmd

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"agr/config"
	"agr/process"
	"agr/server"

	"github.com/spf13/cobra"
)

var (
	configFile string
	port       int
	daemon     bool
)

func init() {
	startCmd.Flags().StringVarP(&configFile, "config", "c", "", "指定 TOML 配置文件路径")
	startCmd.Flags().IntVarP(&port, "port", "p", 0, "覆盖配置文件中的监听端口")
	startCmd.Flags().BoolVarP(&daemon, "daemon", "d", false, "以后台进程方式运行")
	rootCmd.AddCommand(startCmd)
}

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "启动 agr 网关服务",
	RunE: func(cmd *cobra.Command, args []string) error {
		// 检查是否是后台子进程（通过环境变量标记）
		if os.Getenv("AGR_DAEMON_CHILD") == "1" {
			return runServer()
		}

		// 加载配置（用于检查 PID 文件和端口）
		cfg, err := config.Load(configFile)
		if err != nil {
			return err
		}

		if port > 0 {
			cfg.Server.Port = port
		}

		// 检查是否已有实例在运行
		stale, pid := process.CheckStale(cfg.Server.PIDFile)
		if !stale && pid > 0 {
			return fmt.Errorf("agr 已在运行中 (PID: %d)，请先执行 agr stop", pid)
		}
		// 清理过期 PID 文件
		if stale {
			process.RemovePID(cfg.Server.PIDFile)
		}

		if daemon {
			return startDaemon()
		}
		return runServer()
	},
}

// startDaemon 以后台进程方式启动
func startDaemon() error {
	// 获取当前可执行文件路径
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("获取可执行文件路径失败: %w", err)
	}

	// 构建子进程参数
	args := []string{"start"}
	if configFile != "" {
		args = append(args, "-c", configFile)
	}
	if port > 0 {
		args = append(args, "-p", fmt.Sprintf("%d", port))
	}

	cmd := exec.Command(executable, args...)
	cmd.Env = append(os.Environ(), "AGR_DAEMON_CHILD=1")
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil

	// 使子进程脱离当前进程组
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动后台进程失败: %w", err)
	}

	fmt.Printf("agr 已在后台启动 (PID: %d)\n", cmd.Process.Pid)
	return nil
}

// runServer 前台运行服务
func runServer() error {
	// 加载配置
	cfg, err := config.Load(configFile)
	if err != nil {
		return err
	}

	// 命令行端口覆盖
	if port > 0 {
		cfg.Server.Port = port
	}

	// 设置日志级别
	setupLogger(cfg.Server.LogLevel)

	// 写入 PID 文件
	if err := process.WritePID(cfg.Server.PIDFile); err != nil {
		return err
	}
	defer process.RemovePID(cfg.Server.PIDFile)

	// 创建服务器
	srv := server.New(cfg)

	// 监听系统信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// 启动服务
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start()
	}()

	// 等待信号或错误
	select {
	case sig := <-sigCh:
		slog.Info("收到信号，开始停机", "signal", sig)
		return srv.Shutdown()
	case err := <-errCh:
		return err
	}
}

// setupLogger 设置日志级别，同时输出到 stdout 和日志文件
func setupLogger(level string) {
	var logLevel slog.Level
	switch level {
	case "debug":
		logLevel = slog.LevelDebug
	case "info":
		logLevel = slog.LevelInfo
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}

	// 创建日志目录
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	logDir := filepath.Join(home, ".agr", "logs")
	os.MkdirAll(logDir, 0755)

	// 按日期命名日志文件
	logFile := filepath.Join(logDir, time.Now().Format("2006-01-02")+".log")
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		// 打开文件失败，仅输出到 stdout
		handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})
		slog.SetDefault(slog.New(handler))
		return
	}

	// 同时写入 stdout 和日志文件
	w := io.MultiWriter(os.Stdout, f)
	handler := slog.NewTextHandler(w, &slog.HandlerOptions{Level: logLevel})
	slog.SetDefault(slog.New(handler))
}
