package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/yourname/gobiliticket/internal/api"
	"github.com/yourname/gobiliticket/internal/config"
	"github.com/yourname/gobiliticket/internal/push"
	"github.com/yourname/gobiliticket/internal/ticket"
)

var (
	areaID      int
	scheduleID int
	itemID     int
	userID     int64
	quantity   int
	intervalMs int
	maxAttempts int
	isHot     bool
	isMobile  bool
	fastMode  bool
	buyerIDs  string
	contactName string
	contactTel  string
)

var buyCmd = &cobra.Command{
	Use:   "buy",
	Short: "开始抢票",
	Long:  `启动抢票任务，持续尝试直到成功或达到最大次数`,
	RunE:  runBuy,
}

func init() {
	buyCmd.Flags().IntVarP(&areaID, "area-id", "", 0, "项目ID (project_id)")
	buyCmd.Flags().IntVarP(&scheduleID, "schedule-id", "", 0, "场次ID (screen_id)")
	buyCmd.Flags().IntVarP(&itemID, "item-id", "", 0, "票档ID (sku_id)")
	buyCmd.Flags().Int64VarP(&userID, "user-id", "", 0, "用户ID")
	buyCmd.Flags().IntVarP(&quantity, "quantity", "q", 1, "购买数量")
	buyCmd.Flags().IntVarP(&intervalMs, "interval", "i", 500, "轮询间隔(毫秒)")
	buyCmd.Flags().IntVarP(&maxAttempts, "max-attempts", "m", 0, "最大尝试次数(0=无限)")
	buyCmd.Flags().BoolVar(&isHot, "hot", false, "热门项目模式")
	buyCmd.Flags().BoolVar(&isMobile, "mobile", false, "手机端模式")
	buyCmd.Flags().BoolVar(&fastMode, "fast", false, "快模式")
	buyCmd.Flags().StringVar(&buyerIDs, "buyer-ids", "", "购票人ID列表(逗号分隔)，用于多人实名购票")
	buyCmd.Flags().StringVar(&contactName, "contact-name", "", "联系人姓名（项目需要时必填）")
	buyCmd.Flags().StringVar(&contactTel, "contact-tel", "", "联系人手机号（项目需要时必填）")
}

func runBuy(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 监听 Ctrl+C
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("[INFO] 收到退出信号，正在取消任务...")
		cancel()
	}()

	// 验证参数
	if areaID == 0 || scheduleID == 0 || itemID == 0 {
		return fmt.Errorf("缺少必要参数: --area-id, --schedule-id, --item-id")
	}

	// 加载配置
	cfg := &config.Config{}
	cfg.LoadFromEnv()

	// 创建 API 客户端
	apiClient := api.NewClient(&api.Config{
		SESSDATA:   cfg.BiliBili.SESSDATA,
		BILI_JCT:   cfg.BiliBili.BILI_JCT,
		BUVID3:     cfg.BiliBili.BUVID3,
		DedeUserID: cfg.BiliBili.DedeUserID,
	})

	var buyerIDList []int64
	if strings.TrimSpace(buyerIDs) != "" {
		parts := strings.Split(buyerIDs, ",")
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			id, err := strconv.ParseInt(p, 10, 64)
			if err != nil || id <= 0 {
				return fmt.Errorf("buyer-ids 参数错误: %q", p)
			}
			buyerIDList = append(buyerIDList, id)
		}
	}

	// 创建抢票引擎
	engine := ticket.NewEngine(apiClient, &ticket.Config{
		ProjectID:   fmt.Sprintf("%d", areaID),
		ScreenID:    fmt.Sprintf("%d", scheduleID),
		SkuID:      fmt.Sprintf("%d", itemID),
		UserID:     userID,
		Count:      quantity,
		IntervalMs: intervalMs,
		MaxAttempts: maxAttempts,
		IsHot:     isHot,
		IsMobile:  isMobile,
		FastMode:  fastMode,
		BuyerIDs:  buyerIDList,
		ContactName: contactName,
		ContactTel:  contactTel,
	})

	// 设置推送
	if cfg.Push.Enabled {
		pushManager := push.NewManager(&push.Config{
			ServerChan: push.ServerChanConfig{
				APIURL: cfg.Push.ServerChan.APIURL,
				Key:    cfg.Push.ServerChan.Key,
			},
			Bark: push.BarkConfig{
				Token: cfg.Push.Bark.Token,
			},
			PushPlus: push.PushPlusConfig{
				Token: cfg.Push.PushPlus.Token,
			},
			Ntfy: push.NtfyConfig{
				URL:      cfg.Push.Ntfy.URL,
				Username: cfg.Push.Ntfy.Username,
				Password: cfg.Push.Ntfy.Password,
			},
		})
		engine.SetPush(pushManager)
	}

	// 开始抢票
	err := engine.Start(ctx)
	if err != nil {
		if err == context.Canceled {
			log.Println("[INFO] 任务被用户取消")
			return nil
		}
		return fmt.Errorf("抢票失败: %w", err)
	}

	return nil
}
