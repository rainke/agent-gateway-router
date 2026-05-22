package cmd

import (
	"fmt"
	"time"

	"agr/config"
	"agr/process"

	"github.com/spf13/cobra"
)

func init() {
	restartCmd.Flags().StringVarP(&configFile, "config", "c", "", "指定 TOML 配置文件路径")
	restartCmd.Flags().IntVarP(&port, "port", "p", 0, "覆盖配置文件中的监听端口")
	restartCmd.Flags().BoolVarP(&daemon, "daemon", "d", false, "以后台进程方式运行")
	rootCmd.AddCommand(restartCmd)
}

var restartCmd = &cobra.Command{
	Use:   "restart",
	Short: "重启 agr 网关服务",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(configFile)
		if err != nil {
			return err
		}

		// 尝试停止现有进程
		pid, err := process.ReadPID(cfg.Server.PIDFile)
		if err == nil && process.IsRunning(pid) {
			fmt.Printf("正在停止 agr (PID: %d)...\n", pid)
			if err := process.StopProcess(pid); err != nil {
				return fmt.Errorf("停止现有进程失败: %w", err)
			}

			// 等待进程退出
			for i := 0; i < 30; i++ {
				if !process.IsRunning(pid) {
					break
				}
				time.Sleep(time.Second)
			}

			if process.IsRunning(pid) {
				return fmt.Errorf("等待进程退出超时，PID: %d", pid)
			}
			process.RemovePID(cfg.Server.PIDFile)
			fmt.Println("agr 已停止")
		}

		// 启动新进程
		fmt.Println("正在启动 agr...")
		if daemon {
			return startDaemon()
		}
		return runServer()
	},
}
