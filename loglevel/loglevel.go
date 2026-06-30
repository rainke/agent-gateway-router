// Package loglevel 定义 agr 共享的日志级别，并提供与 log/slog 之间的转换辅助。
//
// 级别优先级顺序（从最详细到最简略）为：
//
//	debug > trace > info > warn > error
//
// 其中 trace 为自定义级别，数值取 slog.Level(-2)，介于 debug(-4) 与 info(0) 之间。
// 数值越小越详细，因此在 info/warn/error 下不会输出 trace 级别的日志，
// 而在 debug 或 trace 下会输出。
package loglevel

import (
	"context"
	"log/slog"
	"strings"
)

// LevelTrace 是介于 debug 与 info 之间的自定义日志级别，
// 用于输出最细粒度的诊断信息（如转换前后的请求体）。
const LevelTrace = slog.Level(-2)

// ParseLevel 将字符串日志级别转换为 slog.Level。
// 空字符串或未识别的级别返回 slog.LevelInfo（与默认行为一致）。
func ParseLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "trace":
		return LevelTrace
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// LevelName 返回日志级别对应的大写名称。
// 自定义的 trace 级别返回 "TRACE"，避免 slog 默认的 "DEBUG+2" 形式。
func LevelName(l slog.Level) string {
	if l == LevelTrace {
		return "TRACE"
	}
	return strings.ToUpper(l.String())
}

// Trace 以 trace 级别输出日志。
func Trace(msg string, args ...any) {
	slog.Log(context.Background(), LevelTrace, msg, args...)
}
