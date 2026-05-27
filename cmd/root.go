package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	configFile string
	verbose    bool
)

var rootCmd = &cobra.Command{
	Use:   "gobiliticket",
	Short: "B站抢票工具 - Go版本",
	Long: `B站抢票工具 Go 语言版本
支持多平台、可扩展的抢票解决方案

子命令:
  buy      开始抢票
  login    扫码登录 B站
  web      启动 Web 管理界面
  worker   启动 Worker 模式
  server   启动 Server 模式
  ui       启动 Web UI 界面
  demo     启动性能展示 Demo
  search   搜索/发现演出项目
  test     查询项目详情
  manage   抢票历史管理`,
	Version: "1.0.0",
	RunE: func(cmd *cobra.Command, args []string) error {
		// build.bat 会编译一个 windowsgui 版本用于“直接双击启动”。
		// windowsgui 模式没有控制台输出，如果这里仅显示 help，用户会感觉“无反应”。
		// 因此默认行为：无参数时直接启动 Web 管理界面。
		return runWeb(webCmd, args)
	},
}

func main() {
	rootCmd.PersistentFlags().StringVarP(&configFile, "config", "c", "", "配置文件路径")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "详细输出")

	// 初始化日志（GUI 模式也需要记录到文件）
	initLogging()

	rootCmd.AddCommand(buyCmd)
	rootCmd.AddCommand(workerCmd)
	rootCmd.AddCommand(serverCmd)
	rootCmd.AddCommand(uiCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
