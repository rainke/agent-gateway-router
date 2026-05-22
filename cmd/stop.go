package cmd

import (
	"fmt"
	"time"

	"agr/config"
	"agr/process"

	"github.com/spf13/cobra"
)

func init() {
	stopCmd.Flags().StringVarP(&configFile, "config", "c", "", "指定 TOML 配置文件路径")
	rootCmd.AddCommand(stopCmd)
}

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "停止 agr 网关服务",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(configFile)
		if err != nil {
			return err
		}

		pid, err := process.ReadPID(cfg.Server.PIDFile)
		if err != nil {
			return err
		}

		if !process.IsRunning(pid) {
			// 进程已不存在，清理 PID 文件
			process.RemovePID(cfg.Server.PIDFile)
			fmt.Println("agr 未在运行")
			return nil
		}

		fmt.Printf("正在停止 agr (PID: %d)...\n", pid)
		if err := process.StopProcess(pid); err != nil {
			return err
		}

		// 等待进程退出
		for i := 0; i < 30; i++ {
			if !process.IsRunning(pid) {
				process.RemovePID(cfg.Server.PIDFile)
				fmt.Println("agr 已停止")
				return nil
			}
			time.Sleep(time.Second)
		}

		return fmt.Errorf("等待进程退出超时，PID: %d", pid)
	},
}
