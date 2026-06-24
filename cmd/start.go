package cmd

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

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

	// 使子进程脱离当前进程组（平台特定设置在 daemon_*.go 中）
	cmd.SysProcAttr = daemonProcAttr()

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
	notifyShutdownSignals(sigCh)

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

	// 使用按天滚动的 writer，跨过午夜时自动切换到新日期的日志文件，
	// 无需重启进程即可实现日志按天分割。
	fw := newDailyFileWriter(logDir, time.Now)
	// 预先尝试打开一次，失败则仅输出到 stdout
	if _, err := fw.Write(nil); err != nil {
		slog.SetDefault(slog.New(newUnescapeHandler(os.Stdout, logLevel)))
		return
	}

	// 同时写入 stdout 和日志文件
	w := io.MultiWriter(os.Stdout, fw)
	slog.SetDefault(slog.New(newUnescapeHandler(w, logLevel)))
}

// newUnescapeHandler 构造一个 slog.Handler，
// 行为接近 slog.TextHandler，但对 string 类型的值不再调用 strconv.AppendQuote，
// 因此传入的 JSON 字符串里如果包含双引号/反斜杠等字符不会被再次转义。
func newUnescapeHandler(w io.Writer, level slog.Level) slog.Handler {
	return &unescapeTextHandler{w: w, level: level}
}

// unescapeTextHandler 仿照 slog.TextHandler 的输出格式，
// 但在写 string 值时直接输出原始内容（只在包含换行等控制字符时才加引号并转义）。
type unescapeTextHandler struct {
	w     io.Writer
	level slog.Level
	attrs []slog.Attr
	group string
}

func (h *unescapeTextHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.level
}

func (h *unescapeTextHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	// time
	if !r.Time.IsZero() {
		b.WriteString("time=")
		b.WriteString(r.Time.Format("2006-01-02T15:04:05.000-07:00"))
		b.WriteByte(' ')
	}
	// level
	b.WriteString("level=")
	b.WriteString(strings.ToUpper(r.Level.String()))
	b.WriteByte(' ')
	// msg
	b.WriteString("msg=")
	unescapeWriteValue(&b, slog.StringValue(r.Message), false)
	// attrs (pre + record)
	for _, a := range h.attrs {
		unescapeAppendAttr(&b, h.group, a)
	}
	r.Attrs(func(a slog.Attr) bool {
		unescapeAppendAttr(&b, h.group, a)
		return true
	})
	b.WriteByte('\n')
	_, err := io.WriteString(h.w, b.String())
	return err
}

func (h *unescapeTextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	merged := make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	merged = append(merged, h.attrs...)
	merged = append(merged, attrs...)
	return &unescapeTextHandler{w: h.w, level: h.level, attrs: merged, group: h.group}
}

func (h *unescapeTextHandler) WithGroup(name string) slog.Handler {
	g := name
	if h.group != "" {
		g = h.group + "." + name
	}
	return &unescapeTextHandler{w: h.w, level: h.level, attrs: h.attrs, group: g}
}

func unescapeAppendAttr(b *strings.Builder, prefix string, a slog.Attr) {
	if a.Equal(slog.Attr{}) {
		return
	}
	key := a.Key
	if prefix != "" {
		key = prefix + "." + key
	}
	// slog.Any 自带的 "slog" group / source 等走默认值
	if a.Value.Kind() == slog.KindGroup {
		g := a.Value.Group()
		newPrefix := key
		for _, ga := range g {
			unescapeAppendAttr(b, newPrefix, ga)
		}
		return
	}
	b.WriteString(" ")
	b.WriteString(key)
	b.WriteString("=")
	unescapeWriteValue(b, a.Value, true)
}

// unescapeWriteValue 写出 attr value。
// 关键点：string 值不再强制用 strconv.Quote 包裹，直接写出。
// 只在包含换行/控制字符时使用 strconv.Quote 兜底。
func unescapeWriteValue(b *strings.Builder, v slog.Value, allowUnquoted bool) {
	switch v.Kind() {
	case slog.KindString:
		s := v.String()
		if allowUnquoted && !needsTextQuote(s) {
			b.WriteString(s)
		} else {
			b.WriteString(strconv.Quote(s))
		}
	case slog.KindInt64:
		b.WriteString(strconv.FormatInt(v.Int64(), 10))
	case slog.KindUint64:
		b.WriteString(strconv.FormatUint(v.Uint64(), 10))
	case slog.KindFloat64:
		b.WriteString(strconv.FormatFloat(v.Float64(), 'g', -1, 64))
	case slog.KindBool:
		b.WriteString(strconv.FormatBool(v.Bool()))
	case slog.KindDuration:
		b.WriteString(v.Duration().String())
	case slog.KindTime:
		b.WriteString(v.Time().Format("2006-01-02T15:04:05.000-07:00"))
	case slog.KindAny, slog.KindLogValuer:
		fallthrough
	default:
		// 任意类型用 fmt.Sprint 渲染
		fmt.Fprintf(b, "%+v", v.Any())
	}
}

// needsTextQuote 判定 string 是否需要加引号。
// 比 slog.TextHandler 宽松：双引号、反斜杠不再触发 quote，
// 只在包含空格、`=`、换行等控制字符时才走 quote 路径。
func needsTextQuote(s string) bool {
	if s == "" {
		return true
	}
	for i := 0; i < len(s); {
		b := s[i]
		if b < utf8.RuneSelf {
			if b == ' ' || b == '=' || b == '\n' || b == '\r' || b == '\t' {
				return true
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError || !unicode.IsPrint(r) {
			return true
		}
		i += size
	}
	return false
}
