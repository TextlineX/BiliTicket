package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var testProjectID string

var testCmd = &cobra.Command{
	Use:   "test",
	Short: "查询项目详情 (支持链接/ID)",
	Long: `通过项目ID或B站链接查询演出详情，展示所有场次和票档信息
自动添加到历史记录，方便后续快速抢票

支持格式:
  ./gobiticket.exe test -p 1000070
  ./gobiticket.exe test -p https://mall.bilibili.com/neul-next/ticket/detail.html?id=1000070
  ./gobiticket.exe test -p "https://show.bilibili.com/api/ticket/project/getV2?id=1000070"`,
	RunE: runTest,
}

func init() {
	rootCmd.AddCommand(testCmd)
	testCmd.Flags().StringVarP(&testProjectID, "project-id", "p", "", "项目ID或B站链接")
	testCmd.MarkFlagRequired("project-id")
}

func runTest(cmd *cobra.Command, args []string) error {
	if testProjectID == "" {
		return fmt.Errorf("缺少参数: --project-id")
	}

	// 解析 URL 或 ID
	projectID, err := ParseURL(testProjectID)
	if err != nil {
		return fmt.Errorf("解析失败: %w", err)
	}

	fmt.Printf("  🔍 项目ID: %d\n\n", projectID)

	// 使用 manage 的 CLI 版本（会自动添加到历史）
	project, err := fetchProjectDetailCLI(projectID)
	if err != nil {
		return fmt.Errorf("查询失败: %w", err)
	}

	displayProjectDetailCLI(project)

	// 显示购买命令
	fmt.Println()
	fmt.Println("  ─────────────────────────────────────────────────────")
	fmt.Println("  🎫 快速抢票命令:")
	if len(project.Screens) > 0 {
		s := project.Screens[0]
		fmt.Printf("    ./gobiticket.exe buy \\\n")
		fmt.Printf("      --area-id %d \\\n", project.ID)
		fmt.Printf("      --schedule-id %d \\\n", s.ID)
		if len(s.Tickets) > 0 {
			fmt.Printf("      --item-id %d \\\n", s.Tickets[0].ID)
		}
		fmt.Printf("      -q 1 -i 500\n")
	}
	fmt.Println()
	fmt.Println("  💡 查看所有历史: ./gobiticket.exe manage")
	fmt.Println()

	return nil
}
