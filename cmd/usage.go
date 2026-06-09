package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"agr/proxy"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func init() {
	rootCmd.AddCommand(usageCmd)
}

var usageCmd = &cobra.Command{
	Use:   "usage [date|start:end]",
	Short: "查看 token 使用量统计",
	Long:  "查看指定日期或日期范围的 token 使用量。格式：YYYY-MM-DD 或 YYYY-MM-DD:YYYY-MM-DD",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var dateArg string
		if len(args) > 0 {
			dateArg = args[0]
		}

		startDate, endDate, err := parseUsageDateArg(dateArg)
		if err != nil {
			return err
		}

		usageDir := proxy.UsageDir()
		summaries, err := aggregateUsage(usageDir, startDate, endDate)
		if err != nil {
			return err
		}

		if len(summaries) == 0 {
			if dateArg == "" {
				fmt.Fprintln(os.Stdout, "No usage data found.")
			} else {
				fmt.Fprintf(os.Stdout, "No usage data found for %s.\n", dateArg)
			}
			return nil
		}

		isTTY := term.IsTerminal(int(os.Stdout.Fd()))
		output := renderUsageTable(summaries, startDate, endDate, isTTY)
		fmt.Fprint(os.Stdout, output)
		return nil
	},
}

// parseUsageDateArg 解析日期参数。
// 空参数返回今天；"YYYY-MM-DD" 返回单日；"YYYY-MM-DD:YYYY-MM-DD" 返回范围。
func parseUsageDateArg(arg string) (start, end time.Time, err error) {
	today := time.Now().In(time.Local)
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.Local)

	if arg == "" {
		return today, today, nil
	}

	if strings.Contains(arg, ":") {
		parts := strings.SplitN(arg, ":", 2)
		start, err = time.ParseInLocation("2006-01-02", parts[0], time.Local)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("无效的起始日期格式: %s（需要 YYYY-MM-DD）", parts[0])
		}
		end, err = time.ParseInLocation("2006-01-02", parts[1], time.Local)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("无效的结束日期格式: %s（需要 YYYY-MM-DD）", parts[1])
		}
		if start.After(end) {
			return time.Time{}, time.Time{}, fmt.Errorf("起始日期 %s 不能晚于结束日期 %s", parts[0], parts[1])
		}
		return start, end, nil
	}

	start, err = time.ParseInLocation("2006-01-02", arg, time.Local)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("无效的日期格式: %s（需要 YYYY-MM-DD）", arg)
	}
	return start, start, nil
}

// UsageSummary 按 provider+model 聚合的使用量。
type UsageSummary struct {
	Provider  string
	Model     string
	Requests  int
	Input     int
	Output    int
	Reasoning int
	Cached    int
	Total     int
}

// aggregateUsage 读取日期范围内所有 JSONL 文件并按 provider+model 聚合。
func aggregateUsage(usageDir string, start, end time.Time) ([]UsageSummary, error) {
	if _, err := os.Stat(usageDir); os.IsNotExist(err) {
		return nil, nil
	}

	agg := make(map[string]*UsageSummary)

	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		filename := d.Format("2006-01-02") + ".jsonl"
		path := filepath.Join(usageDir, filename)

		if err := readJSONLFile(path, agg); err != nil {
			return nil, err
		}
	}

	// 转换为排序后的切片
	summaries := make([]UsageSummary, 0, len(agg))
	for _, s := range agg {
		summaries = append(summaries, *s)
	}

	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].Provider != summaries[j].Provider {
			return summaries[i].Provider < summaries[j].Provider
		}
		return summaries[i].Model < summaries[j].Model
	})

	return summaries, nil
}

// readJSONLFile 读取单个 JSONL 文件并将记录聚合到 map 中。
func readJSONLFile(path string, agg map[string]*UsageSummary) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // 跳过缺失的日期文件
		}
		return fmt.Errorf("打开文件 %s 失败: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var record proxy.UsageRecord
		if err := json.Unmarshal(line, &record); err != nil {
			slog.Debug("跳过无效 JSONL 行", "path", path, "error", err)
			continue
		}

		key := record.Provider + "\t" + record.Model
		s, ok := agg[key]
		if !ok {
			s = &UsageSummary{
				Provider: record.Provider,
				Model:    record.Model,
			}
			agg[key] = s
		}

		s.Requests++
		s.Input += record.InputTokens
		s.Output += record.OutputTokens
		s.Reasoning += record.OutputReasoningTokens
		s.Cached += record.CachedTokens
		s.Total += record.TotalTokens
	}

	return scanner.Err()
}

// renderUsageTable 渲染 ANSI 表格输出。
// isTTY 为 true 时启用颜色。
func renderUsageTable(summaries []UsageSummary, start, end time.Time, isTTY bool) string {
	var b strings.Builder

	// 标题行
	title := "agr usage"
	if start.Equal(end) {
		title += " · " + start.Format("2006-01-02")
	} else {
		title += " · " + start.Format("2006-01-02") + " ~ " + end.Format("2006-01-02")
	}
	if isTTY {
		b.WriteString("\033[36m" + title + "\033[0m")
	} else {
		b.WriteString(title)
	}
	b.WriteString("\n\n")

	headers := []string{"Provider", "Model", "Requests", "Input", "Output", "Reasoning", "Cached", "Total"}

	// 预格式化所有数据单元格以计算列宽
	type row [8]string
	rows := make([]row, len(summaries))
	var totals [8]int

	for i, s := range summaries {
		totals[0] += s.Requests
		totals[1] += s.Input
		totals[2] += s.Output
		totals[3] += s.Reasoning
		totals[4] += s.Cached
		totals[5] += s.Total

		rows[i] = row{
			s.Provider,
			s.Model,
			formatNumber(s.Requests),
			formatNumber(s.Input),
			formatNumber(s.Output),
			formatNumber(s.Reasoning),
			formatNumber(s.Cached),
			formatNumber(s.Total),
		}
	}

	totalRow := row{
		"Total", "",
		formatNumber(totals[0]),
		formatNumber(totals[1]),
		formatNumber(totals[2]),
		formatNumber(totals[3]),
		formatNumber(totals[4]),
		formatNumber(totals[5]),
	}

	// 计算列宽
	widths := make([]int, 8)
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, r := range rows {
		for i, c := range r {
			if len(c) > widths[i] {
				widths[i] = len(c)
			}
		}
	}
	for i, c := range totalRow {
		if i >= 2 && len(c) > widths[i] {
			widths[i] = len(c)
		}
	}
	// 填充间距
	for i := range widths {
		widths[i] += 2
	}

	// 表头
	if isTTY {
		b.WriteString("\033[1m")
	}
	for i, h := range headers {
		if i < 2 {
			b.WriteString(fmt.Sprintf("%-*s", widths[i], h))
		} else {
			b.WriteString(fmt.Sprintf("%*s", widths[i], h))
		}
	}
	if isTTY {
		b.WriteString("\033[0m")
	}
	b.WriteString("\n")

	// 分隔线
	totalWidth := 0
	for _, w := range widths {
		totalWidth += w
	}
	sep := strings.Repeat("─", totalWidth)
	if isTTY {
		b.WriteString("\033[2m" + sep + "\033[0m")
	} else {
		b.WriteString(sep)
	}
	b.WriteString("\n")

	// 数据行
	for _, r := range rows {
		b.WriteString(fmt.Sprintf("%-*s", widths[0], r[0]))
		b.WriteString(fmt.Sprintf("%-*s", widths[1], r[1]))
		for i := 2; i < 8; i++ {
			b.WriteString(fmt.Sprintf("%*s", widths[i], r[i]))
		}
		b.WriteString("\n")
	}

	// 分隔线
	if isTTY {
		b.WriteString("\033[2m" + sep + "\033[0m")
	} else {
		b.WriteString(sep)
	}
	b.WriteString("\n")

	// Total 汇总行
	if isTTY {
		b.WriteString("\033[1m")
	}
	b.WriteString(fmt.Sprintf("%-*s", widths[0]+widths[1], totalRow[0]))
	for i := 2; i < 8; i++ {
		b.WriteString(fmt.Sprintf("%*s", widths[i], totalRow[i]))
	}
	if isTTY {
		b.WriteString("\033[0m")
	}
	b.WriteString("\n")

	return b.String()
}

// formatNumber 将整数格式化为带千位分隔符的字符串。
func formatNumber(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}

	// 从右向左每 3 位插入逗号
	var result []byte
	for i := len(s) - 1; i >= 0; i-- {
		if len(result) > 0 && (len(s)-1-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, s[i])
	}
	// 反转
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return string(result)
}
