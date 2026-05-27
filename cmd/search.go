package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var (
	searchKeyword  string
	searchCategory string
	searchLimit   int
	searchUpcoming bool
	searchHot      bool
)

var searchCmd = &cobra.Command{
	Use:   "search",
	Short: "搜索/发现演出项目",
	Long: `搜索B站演出，发现即将开售和热卖中的演出

示例:
  # 查看热门演出
  ./gobiticket.exe search --hot

  # 查看即将开售的演出
  ./gobiticket.exe search --upcoming

  # 搜索关键词
  ./gobiticket.exe search "演唱会"

  # 限制显示数量
  ./gobiticket.exe search --hot --limit 20`,
	RunE: runSearch,
}

func init() {
	rootCmd.AddCommand(searchCmd)
	searchCmd.Flags().StringVarP(&searchKeyword, "keyword", "k", "", "搜索关键词")
	searchCmd.Flags().StringVarP(&searchCategory, "category", "t", "", "分类: 演唱会|音乐节|话剧|动漫|体育|全部")
	searchCmd.Flags().IntVarP(&searchLimit, "limit", "n", 15, "显示数量")
	searchCmd.Flags().BoolVar(&searchUpcoming, "upcoming", false, "即将开售")
	searchCmd.Flags().BoolVar(&searchHot, "hot", false, "热门演出")
}

func runSearch(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 从参数获取关键词
	if len(args) > 0 && searchKeyword == "" {
		searchKeyword = args[0]
	}

	// 解析分类
	catID := parseCategory(searchCategory)

	// 显示标题
	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════════════════════╗")
	fmt.Println("║           B站演出发现 - 抢票前的第一步                    ║")
	fmt.Println("╚══════════════════════════════════════════════════════════╝")
	fmt.Println()

	// 确定查询模式
	mode := "search"
	title := "🔍 搜索结果"
	if searchHot {
		mode = "hot"
		title = "🔥 热门演出"
	} else if searchUpcoming {
		mode = "upcoming"
		title = "⏰ 即将开售"
	}

	fmt.Printf("  %s\n", title)
	if searchKeyword != "" {
		fmt.Printf("  📝 关键词: %s\n", searchKeyword)
	}
	if catID > 0 {
		fmt.Printf("  🏷️  分类: %s\n", searchCategory)
	}
	fmt.Printf("  📊 显示数量: %d\n", searchLimit)
	fmt.Println()

	var projects []projectItem
	var err error

	switch mode {
	case "hot":
		projects, err = fetchHotProjects(ctx, searchLimit)
	case "upcoming":
		projects, err = fetchUpcomingProjects(ctx, searchLimit)
	default:
		if searchKeyword != "" {
			projects, err = searchProjects(ctx, searchKeyword, catID, searchLimit)
		} else {
			// 默认显示热门
			projects, err = fetchHotProjects(ctx, searchLimit)
		}
	}

	if err != nil {
		return fmt.Errorf("获取失败: %w", err)
	}

	if len(projects) == 0 {
		fmt.Println("  😔 暂无相关演出")
		return nil
	}

	// 添加到历史记录
	for _, p := range projects {
		addToHistory(HistoryItem{
			ProjectID: p.ID,
			Name:     p.Name,
			Venue:    p.Venue,
			PriceLow: p.PriceLow,
			SaleTime: p.SaleTime,
			SaleFlag: p.SaleFlag,
			Cover:    p.Img,
		})
	}

	// 显示列表
	displayProjects(projects)
	return nil
}

// ============ 数据结构 ============

type projectItem struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Type      int    `json:"type"`
	PriceLow  int    `json:"price_low"`
	PriceHigh int    `json:"price_high"`
	Status    int    `json:"status"`
	StartTime int64  `json:"start_time"`
	SaleTime  int64  `json:"sale_time"`
	SaleFlag  string `json:"sale_flag"`
	Venue     string `json:"venue"`
	Img       string `json:"img"`
	Cover     string `json:"cover"`
}

// ============ API 调用 ============

func fetchHotProjects(ctx context.Context, limit int) ([]projectItem, error) {
	apiURL := fmt.Sprintf(
		"https://show.bilibili.com/api/ticket/project/listV2?filterHt=false&page=1&pagesize=%d&platform=h5&type=1&area=0",
		limit,
	)
	return fetchProjectList(ctx, apiURL)
}

func fetchUpcomingProjects(ctx context.Context, limit int) ([]projectItem, error) {
	// 按开售时间排序，取即将开售的
	apiURL := fmt.Sprintf(
		"https://show.bilibili.com/api/ticket/project/listV2?filterHt=false&page=1&pagesize=%d&platform=h5&type=2&area=0",
		limit,
	)
	return fetchProjectList(ctx, apiURL)
}

func searchProjects(ctx context.Context, keyword string, catID, limit int) ([]projectItem, error) {
	// 使用B站搜索API
	apiURL := fmt.Sprintf(
		"https://api.bilibili.com/x/web-interface/search/type?search_type=ticket&keyword=%s&page=1&pagesize=%d",
		url.QueryEscape(keyword),
		limit,
	)

	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Referer", "https://www.bilibili.com/")

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
		Code    int `json:"code"`
		Data    struct {
			Results []struct {
				ID       int    `json:"id"`
				Title    string `json:"title"`
				Desc     string `json:"desc"`
				Cover    string `json:"cover"`
				Price    int    `json:"price"`
				Venue    string `json:"venue"`
				ShowTime int64  `json:"show_time"`
			} `json:"result"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	items := make([]projectItem, 0, len(result.Data.Results))
	for _, r := range result.Data.Results {
		items = append(items, projectItem{
			ID:       r.ID,
			Name:     cleanHTML(r.Title),
			PriceLow: r.Price,
			Venue:    r.Venue,
			Img:      r.Cover,
		})
	}

	return items, nil
}

func fetchProjectList(ctx context.Context, apiURL string) ([]projectItem, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
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

	// 尝试多种响应格式
	var result struct {
		Code    int `json:"code"`
		Success bool `json:"success"`
		Data    any `json:"data"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	// 从原始JSON提取项目
	items, err := extractProjectsFromRaw(body)
	return items, err
}

func extractProjectsFromRaw(body []byte) ([]projectItem, error) {
	// 解析为通用map
	var raw struct {
		Code    int `json:"code"`
		Success bool `json:"success"`
		Data    any `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}

	if raw.Code != 0 && !raw.Success {
		return nil, fmt.Errorf("API错误: code=%d", raw.Code)
	}

	if raw.Data == nil {
		return nil, fmt.Errorf("无数据")
	}

	// data 可能是数组或对象
	var dataMap map[string]any
	switch v := raw.Data.(type) {
	case []any:
		// data 是数组
		dataMap = map[string]any{"list": v}
	case map[string]any:
		dataMap = v
	default:
		return nil, fmt.Errorf("未知数据结构")
	}

	// 提取列表 (支持多种格式)
	var listRaw []any
	if resultInterface, ok := dataMap["result"].([]any); ok {
		listRaw = resultInterface
	} else if listInterface, ok := dataMap["list"].([]any); ok {
		listRaw = listInterface
	} else if dataInterface, ok := dataMap["data"].([]any); ok {
		listRaw = dataInterface
	}

	if len(listRaw) == 0 {
		return nil, nil
	}

	items := make([]projectItem, 0, len(listRaw))
	for i, itemRaw := range listRaw {
		if i >= 15 {
			break
		}

		item, ok := itemRaw.(map[string]any)
		if !ok {
			continue
		}

		proj := projectItem{}

		// 支持多种字段名
		if v, ok := item["project_id"].(float64); ok {
			proj.ID = int(v)
		} else if v, ok := item["id"].(float64); ok {
			proj.ID = int(v)
		}

		if v, ok := item["project_name"].(string); ok {
			proj.Name = v
		} else if v, ok := item["name"].(string); ok {
			proj.Name = v
		} else if v, ok := item["title"].(string); ok {
			proj.Name = v
		}

		if v, ok := item["price_low"].(float64); ok {
			proj.PriceLow = int(v)
		}
		if v, ok := item["price_high"].(float64); ok {
			proj.PriceHigh = int(v)
		}
		if v, ok := item["status"].(float64); ok {
			proj.Status = int(v)
		}
		if v, ok := item["start_unix"].(float64); ok {
			proj.StartTime = int64(v)
		}
		if v, ok := item["sale_start_time"].(float64); ok {
			proj.SaleTime = int64(v)
		}
		if v, ok := item["start_time"].(string); ok && proj.StartTime == 0 {
			proj.StartTime = parseTimeStr(v)
		}
		if v, ok := item["end_time"].(string); ok && proj.StartTime == 0 {
			proj.StartTime = parseTimeStr(v)
		}

		// 场馆
		if v, ok := item["venue_name"].(string); ok {
			proj.Venue = v
		}
		if v, ok := item["city"].(string); ok && proj.Venue != "" {
			proj.Venue = v + " · " + proj.Venue
		} else if v, ok := item["city"].(string); ok {
			proj.Venue = v
		}

		// 图片
		if v, ok := item["cover"].(string); ok {
			proj.Img = v
		}
		// 售卖状态
		if v, ok := item["sale_flag"].(string); ok {
			proj.SaleFlag = v
		}

		if proj.ID > 0 && proj.Name != "" {
			items = append(items, proj)
		}
	}

	return items, nil
}

// ============ 显示 ============

func displayProjects(projects []projectItem) {
	typeMap := map[int]string{
		10: "🌸 漫展",
		11: "🎵 演唱会",
		12: "🎭 话剧",
		13: "⚽ 体育",
		14: "🎬 电影",
		15: "🎪 其他",
		0:  "📍 综合",
	}

	statusMap := map[int]string{
		0: "待定",
		1: "热卖中",
		2: "已结束",
		3: "已取消",
	}

	for i, p := range projects {
		sep := "├─"
		if i == len(projects)-1 {
			sep = "└─"
		}

		// 名称
		name := p.Name
		if name == "" {
			name = "(无标题)"
		}

		// 价格
		priceStr := ""
		if p.PriceLow > 0 {
			if p.PriceHigh > p.PriceLow {
				priceStr = fmt.Sprintf("¥%.0f-%.0f", float64(p.PriceLow)/100, float64(p.PriceHigh)/100)
			} else {
				priceStr = fmt.Sprintf("¥%.0f", float64(p.PriceLow)/100)
			}
		} else {
			priceStr = "待定"
		}

		// 售卖状态
		saleStatus := statusMap[p.Status]
		if p.SaleFlag != "" {
			saleStatus = p.SaleFlag
		}

		// 时间
		timeStr := ""
		if p.StartTime > 0 {
			timeStr = time.Unix(p.StartTime, 0).Format("2006-01-02 15:04")
		} else if p.SaleTime > 0 {
			timeStr = time.Unix(p.SaleTime, 0).Format("待开售 2006-01-02")
		}

		// 场馆
		venue := p.Venue
		if venue == "" {
			venue = "待定"
		}

		// 类型
		typeName := typeMap[p.Type]
		if typeName == "" {
			typeName = "📍"
		}

		fmt.Printf("  %s [%d] %s\n", sep, i+1, name)
		fmt.Printf("     │ ID: %d | %s | %s | %s\n", p.ID, typeName, priceStr, saleStatus)
		if timeStr != "" {
			fmt.Printf("     │ %s | %s\n", timeStr, venue)
		}
		if p.Img != "" {
			fmt.Printf("     │ 🖼️  %s\n", p.Img)
		}
		fmt.Println()
	}

	// 提示
	if len(projects) > 0 {
		fmt.Println("  ─────────────────────────────────────────────────────")
		fmt.Println("  📝 查询项目详情:")
		fmt.Printf("    ./gobiticket.exe test -p <项目ID>\n\n")
		fmt.Println("  🎫 开始抢票:")
		p := projects[0]
		fmt.Printf("    ./gobiticket.exe buy --area-id %d --schedule-id <场次> --item-id <票档>\n", p.ID)
	}
}

// ============ 辅助函数 ============

func parseCategory(cat string) int {
	catMap := map[string]int{
		"演唱会": 11,
		"音乐节": 11,
		"话剧":   12,
		"动漫":   10,
		"漫展":   10,
		"体育":   13,
		"全部":   0,
	}
	return catMap[cat]
}

func cleanHTML(s string) string {
	s = strings.ReplaceAll(s, "<em>", "")
	s = strings.ReplaceAll(s, "</em>", "")
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&quot;", "\"")
	return s
}

func parseTimeStr(s string) int64 {
	formats := []string{
		"2006-01-02 15:04",
		"2006-01-02",
		"2006/01/02 15:04",
	}
	for _, f := range formats {
		if t, err := time.ParseInLocation(f, s, time.Local); err == nil {
			return t.Unix()
		}
	}
	return 0
}

// ============ 获取项目详情（带场次）============

// SearchResult 搜索结果（含场次信息）
type SearchResult struct {
	ID       int
	Name     string
	PriceLow int
	Venue    string
	Type     int
	Status   int
	Screens  []Screen
}

type Screen struct {
	ID    int
	Name  string
	Tickets []Ticket
}

type Ticket struct {
	ID    int
	Price int
	Desc  string
	Status string
}

func fetchProjectDetails(ctx context.Context, projectID int) (*SearchResult, error) {
	apiURL := fmt.Sprintf("https://show.bilibili.com/api/ticket/project/getV2?id=%d", projectID)

	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
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
			ID        int `json:"id"`
			Name      string `json:"name"`
			PriceLow  int `json:"price_low"`
			Status    int `json:"status"`
			Type      int `json:"type"`
			VenueInfo struct {
				Name string `json:"name"`
			} `json:"venue_info"`
			ScreenList []struct {
				ID   int `json:"id"`
				Name string `json:"name"`
				TicketList []struct {
					ID    int    `json:"id"`
					Price int    `json:"price"`
					Desc  string `json:"desc"`
					IsSale int   `json:"is_sale"`
					SaleFlag struct {
						DisplayName string `json:"display_name"`
					} `json:"sale_flag"`
				} `json:"ticket_list"`
			} `json:"screen_list"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	if result.Code != 0 {
		return nil, fmt.Errorf("获取项目详情失败: [%d] %s", result.Code, result.Message)
	}

	screens := make([]Screen, 0, len(result.Data.ScreenList))
	for _, s := range result.Data.ScreenList {
		tickets := make([]Ticket, 0, len(s.TicketList))
		for _, t := range s.TicketList {
			status := "待开售"
			if t.IsSale == 2 {
				status = "售卖中"
			} else if t.IsSale == 3 {
				status = "已停售"
			} else if t.IsSale == 4 {
				status = "已售罄"
			}
			tickets = append(tickets, Ticket{
				ID:     t.ID,
				Price:  t.Price,
				Desc:   t.Desc,
				Status: status,
			})
		}
		screens = append(screens, Screen{
			ID:       s.ID,
			Name:     s.Name,
			Tickets: tickets,
		})
	}

	return &SearchResult{
		ID:       result.Data.ID,
		Name:     result.Data.Name,
		PriceLow: result.Data.PriceLow,
		Venue:    result.Data.VenueInfo.Name,
		Type:     result.Data.Type,
		Status:   result.Data.Status,
		Screens:  screens,
	}, nil
}

// displayProjectDetail 显示项目详情
func displayProjectDetail(r *SearchResult) {
	fmt.Println()
	fmt.Println("┏━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┓")
	fmt.Printf("┃ %s\n", r.Name)
	fmt.Println("┗━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┛")
	fmt.Println()

	fmt.Printf("  📋 项目ID: %d\n", r.ID)
	fmt.Printf("  🏢 场馆: %s\n", r.Venue)
	if r.PriceLow > 0 {
		fmt.Printf("  💰 价格: ¥%.0f\n", float64(r.PriceLow)/100)
	}
	fmt.Println()

	if len(r.Screens) > 0 {
		fmt.Println("  🎭 场次与票档:")
		for i, s := range r.Screens {
			sep := "├─"
			if i == len(r.Screens)-1 {
				sep = "└─"
			}
			fmt.Printf("    %s [%d] ScreenID=%d | %s\n", sep, i+1, s.ID, s.Name)
			for j, t := range s.Tickets {
				lastSep := "├─"
				if j == len(s.Tickets)-1 {
					lastSep = "└─"
				}
				fmt.Printf("       %s [票档] SkuID=%d | ¥%.0f | %s | %s\n",
					lastSep, t.ID, float64(t.Price)/100, t.Desc, t.Status)
			}
		}
	}

	fmt.Println()
	fmt.Println("  ─────────────────────────────────────────────────────")
	if len(r.Screens) > 0 {
		s := r.Screens[0]
		fmt.Printf("  🎫 快速抢票命令:\n")
		fmt.Printf("    ./gobiticket.exe buy \\\n")
		fmt.Printf("      --area-id %d \\\n", r.ID)
		fmt.Printf("      --schedule-id %d \\\n", s.ID)
		if len(s.Tickets) > 0 {
			fmt.Printf("      --item-id %d \\\n", s.Tickets[0].ID)
		}
		fmt.Printf("      -q 1 -i 500\n")
	}
}
