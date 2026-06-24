package cmd

import (
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// dailyFileWriter 是一个 io.WriteCloser，它将日志写入按日期命名的文件
// （格式为 2006-01-02.log）。当跨过午夜时，它会自动关闭旧文件并打开
// 新日期对应的文件，从而实现按天滚动日志，无需重启进程。
//
// 所有方法都是并发安全的。
type dailyFileWriter struct {
	dir string
	now func() time.Time

	mu   sync.Mutex
	file *os.File
	date string // 当前已打开文件对应的日期，格式 2006-01-02
}

// newDailyFileWriter 创建一个按天滚动日志的 writer。
// now 参数用于注入时钟，便于测试；生产代码传 time.Now 即可。
func newDailyFileWriter(dir string, now func() time.Time) *dailyFileWriter {
	if now == nil {
		now = time.Now
	}
	return &dailyFileWriter{dir: dir, now: now}
}

// Write 实现 io.Writer。每次写入前检查当前日期是否与已打开文件
// 的日期一致；不一致则关闭旧文件并打开新日期的文件。
func (w *dailyFileWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	today := w.now().Format("2006-01-02")
	if w.file == nil || w.date != today {
		if w.file != nil {
			w.file.Close()
		}
		path := filepath.Join(w.dir, today+".log")
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			// 打开失败时重置状态，下次写入会重试
			w.file = nil
			w.date = ""
			return 0, err
		}
		w.file = f
		w.date = today
	}
	return w.file.Write(p)
}

// Close 关闭当前打开的日志文件。
func (w *dailyFileWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	w.date = ""
	return err
}

// 确保 dailyFileWriter 实现了 io.WriteCloser
var _ io.WriteCloser = (*dailyFileWriter)(nil)
