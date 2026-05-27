package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/yourname/gobiliticket/internal/appdir"
)

var (
	manageAction   string
	manageSelect  int
)

var manageCmd = &cobra.Command{
	Use:   "manage",
	Short: "抢票管理 - 查看/选择/删除历史记录",
	Long: `管理演出搜索和查询历史，方便快速选择目标进行抢票

示例:
  # 查看所有历史记录
  ./gobiticket.exe manage

  # 选择记录开始抢票
  ./gobiticket.exe manage --select 1

  # 删除单条记录
  ./gobiticket.exe manage --delete 2

  # 清空所有记录
  ./gobiticket.exe manage --clear`,
	RunE: runManage,
}

func init() {
	rootCmd.AddCommand(manageCmd)
	manageCmd.Flags().StringVar(&manageAction, "delete", "", "删除指定序号的历史记录")
	manageCmd.Flags().StringVar(&manageAction, "clear", "", "清空所有历史记录")
	manageCmd.Flags().IntVarP(&manageSelect, "select", "s", 0, "选择指定序号开始抢票")
}

func runManage(cmd *cobra.Command, args []string) error {
	// 清空所有
	if manageAction == "clear" {
		confirm := ""
		fmt.Print("  ⚠️  确定要清空所有历史记录吗？(y/N): ")
		fmt.Scanln(&confirm)
		if confirm == "y" || confirm == "Y" {
			if err := clearHistory(); err != nil {
				return err
			}
			fmt.Println("  ✅ 历史记录已清空")
		} else {
			fmt.Println("  已取消")
		}
		return nil
	}

	// 删除单条
	if manageAction != "" {
		idx, err := strconv.Atoi(manageAction)
		if err != nil {
			return fmt.Errorf("无效的序号: %s", manageAction)
		}
		if err := deleteHistory(idx); err != nil {
			return err
		}
		fmt.Println("  ✅ 已删除历史记录")
		displayHistory()
		return nil
	}

	// 选择并抢票
	if manageSelect > 0 {
		item, err := selectHistory(manageSelect)
		if err != nil {
			return err
		}
		fmt.Println()
		fmt.Println("  🎫 已选择项目，开始查询详情...")
		fmt.Println()

		// 显示详情
		project, err := fetchProjectDetailCLI(item.ProjectID)
		if err != nil {
			return fmt.Errorf("查询项目详情失败: %w", err)
		}
		displayProjectDetailCLI(project)

		fmt.Println()
		fmt.Println("  ─────────────────────────────────────────────────────")
		fmt.Println("  📝 请使用以下命令开始抢票:")
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
		return nil
	}

	// 默认显示历史列表
	displayHistory()
	return nil
}

// ============ 数据结构 ============

type HistoryItem struct {
	ProjectID   int    `json:"project_id"`
	Name        string `json:"name"`
	Venue       string `json:"venue"`
	PriceLow    int    `json:"price_low"`
	SaleTime    int64  `json:"sale_time"`
	SaleFlag    string `json:"sale_flag"`
	AddedAt     int64  `json:"added_at"`
	Cover       string `json:"cover,omitempty"`
}

// ============ 历史记录管理 ============

func getHistoryPath() string {
	return appdir.FindHistoryPath()
}

func loadHistory() ([]HistoryItem, error) {
	path := getHistoryPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []HistoryItem{}, nil
		}
		return nil, err
	}

	var items []HistoryItem
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func saveHistory(items []HistoryItem) error {
	path := getHistoryPath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func addToHistory(item HistoryItem) error {
	items, err := loadHistory()
	if err != nil {
		return err
	}

	// 检查是否已存在
	for i, it := range items {
		if it.ProjectID == item.ProjectID {
			// 更新已存在的记录
			item.AddedAt = time.Now().Unix()
			items[i] = item
			return saveHistory(items)
		}
	}

	// 添加到开头
	item.AddedAt = time.Now().Unix()
	items = append([]HistoryItem{item}, items...)

	// 最多保留50条
	if len(items) > 50 {
		items = items[:50]
	}

	return saveHistory(items)
}

func deleteHistory(idx int) error {
	items, err := loadHistory()
	if err != nil {
		return err
	}
	if idx < 1 || idx > len(items) {
		return fmt.Errorf("序号 %d 超出范围", idx)
	}
	items = append(items[:idx-1], items[idx:]...)
	return saveHistory(items)
}

func selectHistory(idx int) (*HistoryItem, error) {
	items, err := loadHistory()
	if err != nil {
		return nil, err
	}
	if idx < 1 || idx > len(items) {
		return nil, fmt.Errorf("序号 %d 超出范围", idx)
	}
	return &items[idx-1], nil
}

func clearHistory() error {
	path := getHistoryPath()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func displayHistory() {
	items, err := loadHistory()
	if err != nil {
		fmt.Printf("  ⚠️  加载历史记录失败: %v\n", err)
		return
	}

	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════════════════════╗")
	fmt.Println("║              🎫 抢票历史记录                         ║")
	fmt.Println("╚══════════════════════════════════════════════════════════╝")
	fmt.Println()

	if len(items) == 0 {
		fmt.Println("  📭 暂无历史记录")
		fmt.Println()
		fmt.Println("  💡 使用以下命令添加:")
		fmt.Println("    ./gobiticket.exe search --hot           # 搜索演出")
		fmt.Println("    ./gobiticket.exe test -p <URL或ID>    # 查询详情")
		fmt.Println()
		return
	}

	// 按时间倒序显示
	sort.Slice(items, func(i, j int) bool {
		return items[i].AddedAt > items[j].AddedAt
	})

	for i, item := range items {
		sep := "├─"
		if i == len(items)-1 {
			sep = "└─"
		}

		priceStr := "待定"
		if item.PriceLow > 0 {
			priceStr = fmt.Sprintf("¥%.0f", float64(item.PriceLow)/100)
		}

		timeStr := ""
		if item.AddedAt > 0 {
			t := time.Unix(item.AddedAt, 0)
			timeStr = t.Format("2006-01-02 15:04")
		}

		saleFlag := item.SaleFlag
		if saleFlag == "" {
			saleFlag = "未知"
		}

		venue := item.Venue
		if venue == "" {
			venue = "未知场馆"
		}

		name := item.Name
		if name == "" {
			name = "(无标题)"
		}

		fmt.Printf("  %s [%d] %s\n", sep, i+1, name)
		fmt.Printf("     │ ID: %d | %s | %s\n", item.ProjectID, priceStr, saleFlag)
		fmt.Printf("     │ 📍 %s\n", venue)
		if timeStr != "" {
			fmt.Printf("     │ 添加于: %s\n", timeStr)
		}
		fmt.Println()
	}

	fmt.Println("  ─────────────────────────────────────────────────────")
	fmt.Println("  📝 操作命令:")
	fmt.Println("    ./gobiticket.exe manage -s <序号>   # 选择并查看详情")
	fmt.Println("    ./gobiticket.exe manage --delete <序号>  # 删除记录")
	fmt.Println("    ./gobiticket.exe manage --clear    # 清空所有记录")
	fmt.Println()
	fmt.Println("  🎫 开始抢票:")
	fmt.Println("    ./gobiticket.exe buy --area-id <ID> --schedule-id <场次> --item-id <票档>")
	fmt.Println()
}

// ============ 项目详情查询（CLI版本）============

type CLIProjectDetail struct {
	ID         int
	Name       string
	Type       int
	Venue      string
	PriceLow   int
	PriceHigh  int
	Status     int
	StartTime  int64
	SaleTime   int64
	SaleFlag   string
	Cover      string
	Screens    []CLIScreen
}

type CLIScreen struct {
	ID      int
	Name    string
	Tickets []CLITicket
}

type CLITicket struct {
	ID       int
	Price    int
	Desc     string
	IsSale   int
	SaleFlag string
}

func fetchProjectDetailCLI(projectID int) (*CLIProjectDetail, error) {
	apiURL := fmt.Sprintf("https://show.bilibili.com/api/ticket/project/getV2?id=%d", projectID)

	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Referer", "https://show.bilibili.com/")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			ID         int `json:"id"`
			Name       string `json:"name"`
			Type       int `json:"type"`
			PriceLow   int `json:"price_low"`
			PriceHigh  int `json:"price_high"`
			Status     int `json:"status"`
			StartTime  int64 `json:"start_time"`
			SaleBegin  int64 `json:"sale_begin"`
			SaleStart  int64 `json:"saleStart"`
			VenueInfo  struct {
				Name string `json:"name"`
			} `json:"venue_info"`
			ScreenList []struct {
				ID   int `json:"id"`
				Name string `json:"name"`
				TicketList []struct {
					ID       int    `json:"id"`
					Price    int    `json:"price"`
					Desc     string `json:"desc"`
					IsSale   int    `json:"is_sale"`
					SaleFlag struct {
						DisplayName string `json:"display_name"`
					} `json:"sale_flag"`
				} `json:"ticket_list"`
			} `json:"screen_list"`
			Cover string `json:"cover"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	if result.Code != 0 {
		return nil, fmt.Errorf("获取项目详情失败: [%d] %s", result.Code, result.Message)
	}

	proj := &CLIProjectDetail{
		ID:        result.Data.ID,
		Name:      result.Data.Name,
		Type:      result.Data.Type,
		PriceLow:  result.Data.PriceLow,
		PriceHigh: result.Data.PriceHigh,
		Status:    result.Data.Status,
		StartTime: result.Data.StartTime,
		Venue:     result.Data.VenueInfo.Name,
		Cover:     result.Data.Cover,
	}

	if result.Data.SaleBegin > 0 {
		proj.SaleTime = result.Data.SaleBegin
	} else if result.Data.SaleStart > 0 {
		proj.SaleTime = result.Data.SaleStart
	}

	for _, s := range result.Data.ScreenList {
		screen := CLIScreen{ID: s.ID, Name: s.Name}
		for _, t := range s.TicketList {
			screen.Tickets = append(screen.Tickets, CLITicket{
				ID:       t.ID,
				Price:    t.Price,
				Desc:     t.Desc,
				IsSale:   t.IsSale,
				SaleFlag: t.SaleFlag.DisplayName,
			})
		}
		proj.Screens = append(proj.Screens, screen)
	}

	// 添加到历史
	histItem := HistoryItem{
		ProjectID: proj.ID,
		Name:      proj.Name,
		Venue:     proj.Venue,
		PriceLow:  proj.PriceLow,
		SaleTime:  proj.SaleTime,
		SaleFlag:  proj.SaleFlag,
		Cover:     proj.Cover,
	}
	addToHistory(histItem)

	return proj, nil
}

func displayProjectDetailCLI(proj *CLIProjectDetail) {
	statusMap := map[int]string{
		0: "待定",
		1: "热卖中",
		2: "已结束",
		3: "已取消",
	}

	saleFlagMap := map[int]string{
		1: "待开售",
		2: "售卖中",
		3: "已停售",
		4: "已售罄",
	}

	fmt.Println("┏━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┓")
	name := proj.Name
	if len(name) > 44 {
		name = name[:44]
	}
	fmt.Printf("┃ %s\n", name)
	fmt.Println("┗━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┛")
	fmt.Println()

	fmt.Printf("  📋 项目ID:  %d\n", proj.ID)
	fmt.Printf("  🏢 场馆:   %s\n", proj.Venue)
	if proj.PriceLow > 0 && proj.PriceHigh > 0 {
		fmt.Printf("  💰 价格区间: ¥%.2f - ¥%.2f\n",
			float64(proj.PriceLow)/100, float64(proj.PriceHigh)/100)
	}
	fmt.Printf("  📊 状态:   %s\n", statusMap[proj.Status])

	if proj.StartTime > 0 {
		t := time.Unix(proj.StartTime, 0)
		fmt.Printf("  📅 活动开始: %s\n", t.Format("2006-01-02 15:04:05"))
	}
	if proj.SaleTime > 0 {
		t := time.Unix(proj.SaleTime, 0)
		fmt.Printf("  🛒 开售时间: %s\n", t.Format("2006-01-02 15:04:05"))
		now := time.Now().Unix()
		if proj.SaleTime > now {
			cd := proj.SaleTime - now
			fmt.Printf("  ⏰ 距离开售: %s\n", formatDurationCLI(time.Duration(cd)*time.Second))
		} else {
			fmt.Printf("  ⏰ 开售状态: 已开售\n")
		}
	}

	fmt.Println()
	if len(proj.Screens) > 0 {
		fmt.Println("  🎭 场次与票档:")
		for i, s := range proj.Screens {
			sep := "├─"
			if i == len(proj.Screens)-1 {
				sep = "└─"
			}
			fmt.Printf("    %s [%d] ScreenID=%d | %s\n", sep, i+1, s.ID, s.Name)
			for j, t := range s.Tickets {
				lastSep := "├─"
				if j == len(s.Tickets)-1 {
					lastSep = "└─"
				}
				saleStatus := saleFlagMap[t.IsSale]
				if t.SaleFlag != "" {
					saleStatus = t.SaleFlag
				}
				fmt.Printf("       %s [票档%d] SkuID=%d | ¥%.0f | %s | %s\n",
					lastSep, j+1, t.ID, float64(t.Price)/100, t.Desc, saleStatus)
			}
		}
	}
}

func formatDurationCLI(d time.Duration) string {
	if d < 0 {
		return "已过期"
	}
	days := int(d.Hours() / 24)
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	secs := int(d.Seconds()) % 60
	if days > 0 {
		return fmt.Sprintf("%d天 %d小时 %d分", days, hours, mins)
	}
	if hours > 0 {
		return fmt.Sprintf("%d小时 %d分 %d秒", hours, mins, secs)
	}
	if mins > 0 {
		return fmt.Sprintf("%d分 %d秒", mins, secs)
	}
	return fmt.Sprintf("%d秒", secs)
}

// ============ URL 解析 ============

// ParseURL 从各种B站链接中提取项目ID
func ParseURL(input string) (int, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return 0, fmt.Errorf("输入为空")
	}

	// 尝试直接解析为数字
	if id, err := strconv.Atoi(input); err == nil && id > 0 {
		return id, nil
	}

	// 解析URL
	u, err := url.Parse(input)
	if err != nil {
		return 0, fmt.Errorf("无效的链接: %s", input)
	}

	// mall.bilibili.com 格式
	// https://mall.bilibili.com/neul-next/ticket/detail.html?noTitleBar=1&id=1000070
	if strings.Contains(u.Host, "mall.bilibili.com") || strings.Contains(u.Host, "show.bilibili.com") {
		// 尝试多个可能的参数名
		for _, key := range []string{"id", "project_id", "projectId", "iid"} {
			if v := u.Query().Get(key); v != "" {
				if id, err := strconv.Atoi(v); err == nil && id > 0 {
					return id, nil
				}
			}
		}
	}

	// show.bilibili.com/api 格式
	// https://show.bilibili.com/api/ticket/project/getV2?id=1000070
	if strings.Contains(input, "show.bilibili.com") {
		if v := u.Query().Get("id"); v != "" {
			if id, err := strconv.Atoi(v); err == nil && id > 0 {
				return id, nil
			}
		}
	}

	// 从路径中提取数字
	pathParts := strings.Split(u.Path, "/")
	for i := len(pathParts) - 1; i >= 0; i-- {
		if part := pathParts[i]; part != "" {
			if id, err := strconv.Atoi(part); err == nil && id > 1000 {
				return id, nil
			}
		}
	}

	return 0, fmt.Errorf("无法从链接中提取项目ID: %s", input)
}
