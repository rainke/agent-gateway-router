package cmd

import (
	"agr/version"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:     "agr",
	Short:   "agr - Agent Gateway Router",
	Long:    "agr 是一个轻量级本地 Agent 网关路由代理，统一处理多种 AI 客户端与后端大模型供应商之间的协议适配、模型路由和流式响应转发。",
	Version: version.String(),
}

func init() {
	rootCmd.SetVersionTemplate("{{.Version}}\n")
}

// Execute 执行根命令
func Execute() error {
	return rootCmd.Execute()
}
