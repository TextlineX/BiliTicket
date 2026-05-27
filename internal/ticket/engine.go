package ticket

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/yourname/gobiliticket/internal/api"
	"github.com/yourname/gobiliticket/internal/push"
)

// ============ 抢票配置 ============

type Config struct {
	ProjectID   string // 项目ID
	ScreenID    string // 场次ID
	SkuID      string // 票档ID
	UserID      int64  // 用户ID
	BuyerID     int64  // 购票人ID
	BuyerIDs    []int64 // 多人购票：购票人ID列表（可选）
	ContactName string // 联系人姓名（可选，不填则尝试从购票人获取）
	ContactTel  string // 联系人手机号（可选，不填则尝试从购票人获取）
	Count       int    // 购买数量
	IntervalMs  int    // 轮询间隔(ms)
	MaxAttempts int    // 最大尝试次数(0=无限)
	IsHot       bool   // 是否热门项目
	IsMobile    bool   // 是否手机端
	FastMode    bool   // 快模式
}

// ============ 抢票引擎 ============

type Engine struct {
	api    *api.Client
	config *Config
	push   *push.Manager
	mu     sync.RWMutex
}

func isCongestionErr(errno int, msg string) bool {
	if errno == 100001 || errno == 900001 {
		return true
	}
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return false
	}
	return strings.Contains(msg, "前方拥堵") || strings.Contains(msg, "拥堵")
}

func congestionBackoff(base time.Duration, streak int) time.Duration {
	if streak <= 0 {
		return base
	}
	if streak > 5 {
		streak = 5
	}
	d := base * time.Duration(1<<streak)
	if d > 8*time.Second {
		d = 8 * time.Second
	}
	// 抖动：±20%
	jitter := time.Duration(int64(d) / 5)
	if jitter <= 0 {
		return d
	}
	off := time.Duration(time.Now().UnixNano()%int64(jitter*2+1)) - jitter
	return d + off
}

func normalizeCNPhone(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "*") {
		return ""
	}
	var digits strings.Builder
	digits.Grow(len(raw))
	for _, r := range raw {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	s := digits.String()
	if len(s) == 11 {
		return s
	}
	if len(s) > 11 {
		return s[len(s)-11:]
	}
	return ""
}

func isContactRequiredMsg(msg string) bool {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return false
	}
	return strings.Contains(msg, "联系人") && (strings.Contains(msg, "手机号") || strings.Contains(msg, "姓名") || strings.Contains(msg, "请填写正确的联系人手机号"))
}

// EventHandler 事件处理接口
type EventHandler interface {
	OnProgress(attempt int, status string)
	OnSuccess(orderID int64, msg string)
	OnFailure(err error)
	OnCaptcha(captchaURL string) // 需要验证码
}

// NewEngine 创建抢票引擎
func NewEngine(client *api.Client, cfg *Config) *Engine {
	return &Engine{
		api:    client,
		config: cfg,
	}
}

// SetPush 设置推送管理器
func (e *Engine) SetPush(p *push.Manager) {
	e.push = p
}

// Start 开始抢票
func (e *Engine) Start(ctx context.Context) error {
	cfg := e.config

	log.Printf("[INFO] ===== B站抢票任务启动 =====")
	log.Printf("[INFO] 项目ID: %s", cfg.ProjectID)
	log.Printf("[INFO] 场次ID: %s", cfg.ScreenID)
	log.Printf("[INFO] 票档ID: %s", cfg.SkuID)
	log.Printf("[INFO] 购买数量: %d", cfg.Count)
	log.Printf("[INFO] 轮询间隔: %dms", cfg.IntervalMs)
	log.Printf("[INFO] 热门项目: %v", cfg.IsHot)

	// 1. 获取项目详情
	log.Printf("[INFO] 获取项目详情...")
	project, err := e.api.GetProject(ctx, cfg.ProjectID)
	if err != nil {
		return fmt.Errorf("获取项目详情失败: %w", err)
	}
	log.Printf("[INFO] 项目名称: %s", project.Data.Name)
	requireBuyerCount := project.Data.IDBind != 0

	// 2. 检查是否已开售
	saleBegin := project.Data.SaleBegin
	if saleBegin > 0 {
		// 计算倒计时
		now := time.Now().Unix()
		if saleBegin > now {
			countdown := saleBegin - now
			log.Printf("[INFO] 距离开售时间: %d 秒", countdown)
		}
	}

	// 3. 获取购票人信息
	log.Printf("[INFO] 获取购票人信息...")
	buyerInfo, err := e.api.GetBuyerInfo(ctx)
	if err != nil {
		log.Printf("[WARN] 获取购票人信息失败: %v", err)
	}

	var buyerJSON string
	contactName := strings.TrimSpace(cfg.ContactName)
	contactTel := normalizeCNPhone(cfg.ContactTel)
	if buyerInfo != nil {
		list := buyerInfo.GetBuyerList()
		if chosen, selErr := api.SelectBuyersForOrder(list, cfg.BuyerIDs, cfg.Count, requireBuyerCount); selErr != nil {
			return selErr
		} else if len(chosen) > 0 {
			cfg.BuyerID = chosen[0].ID
			if contactName == "" {
				contactName = strings.TrimSpace(chosen[0].Name)
			}
			if contactTel == "" {
				contactTel = normalizeCNPhone(chosen[0].Tel)
			}
			if j, err := api.BuildBuyerInfoJSON(chosen); err == nil {
				buyerJSON = j
			}
		}
	}
	// 兜底：购票人列表手机号可能为空/脱敏，尝试从联系人地址列表获取
	if contactName == "" || contactTel == "" {
		if addrResp, err := e.api.GetBuyerAddressList(ctx); err == nil && addrResp != nil {
			addrs := addrResp.GetAddressList()
			if len(addrs) > 0 {
				if contactName == "" {
					contactName = strings.TrimSpace(addrs[0].Name)
				}
				if contactTel == "" {
					contactTel = normalizeCNPhone(addrs[0].Tel)
				}
			}
		}
	}

	// 4. 开始抢票循环
	attempts := 0
	interval := time.Duration(cfg.IntervalMs) * time.Millisecond
	congestionStreak := 0

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		attempts++
		if cfg.MaxAttempts > 0 && attempts > cfg.MaxAttempts {
			return fmt.Errorf("达到最大尝试次数 %d", cfg.MaxAttempts)
		}

		log.Printf("[INFO] 第 %d 次尝试...", attempts)

		// 4.1 获取 Token (prepare阶段)
		tokenResp, err := e.api.GetTicketToken(ctx, api.TokenParams{
			ProjectID: cfg.ProjectID,
			ScreenID:  cfg.ScreenID,
			SkuID:     cfg.SkuID,
			Count:     cfg.Count,
			IsHot:     cfg.IsHot,
		})

		if err != nil {
			log.Printf("[WARN] 获取Token失败: %v", err)
			congestionStreak = 0
			time.Sleep(interval)
			continue
		}
		if tokenResp == nil {
			log.Printf("[WARN] 获取Token失败: 空响应")
			congestionStreak = 0
			time.Sleep(interval)
			continue
		}

		// 检查是否需要验证码
		if tokenResp.Code != 0 && tokenResp.Code == 401 {
			log.Printf("[WARN] 需要人机验证!")
			congestionStreak = 0
			time.Sleep(interval)
			continue
		}
		if tokenResp.Code != 0 || tokenResp.Errno != 0 {
			msg := tokenResp.Msg
			if msg == "" {
				msg = tokenResp.Message
			}
			log.Printf("[WARN] 获取Token失败: code=%d errno=%d msg=%s", tokenResp.Code, tokenResp.Errno, msg)
			if isCongestionErr(tokenResp.Errno, msg) {
				congestionStreak++
				time.Sleep(congestionBackoff(interval, congestionStreak))
			} else {
				congestionStreak = 0
				time.Sleep(interval)
			}
			continue
		}

		token, ptoken := tokenResp.GetToken()
		if token == "" {
			log.Printf("[WARN] 获取Token失败: token为空（可能需要人机验证/风控）")
			congestionStreak = 0
			time.Sleep(interval)
			continue
		}

		// 4.2 确认订单
		confirmResp, err := e.api.ConfirmOrder(ctx, cfg.ProjectID, token)
		if err != nil {
			log.Printf("[WARN] 确认订单失败: %v", err)
			congestionStreak = 0
			time.Sleep(interval)
			continue
		}

		if confirmResp.Errno != 0 || confirmResp.Code != 0 {
			log.Printf("[WARN] 确认订单失败: code=%d, msg=%s", confirmResp.Errno, confirmResp.Msg)
			if isCongestionErr(confirmResp.Errno, confirmResp.Msg) {
				congestionStreak++
				time.Sleep(congestionBackoff(interval, congestionStreak))
			} else {
				congestionStreak = 0
				time.Sleep(interval)
			}
			continue
		}

		count, payMoney, hasData := confirmResp.GetConfirmData()
		if !hasData {
			log.Printf("[WARN] 确认订单无有效数据")
			congestionStreak = 0
			time.Sleep(interval)
			continue
		}

		// 4.3 创建订单 (createV2阶段)
		createResp, err := e.api.CreateOrder(ctx, api.CreateOrderParams{
			ProjectID: cfg.ProjectID,
			ScreenID:  cfg.ScreenID,
			SkuID:     cfg.SkuID,
			Token:     token,
			Ptoken:    ptoken,
			BuyerInfo: buyerJSON,
			ContactName: contactName,
			ContactTel:  contactTel,
			PayMoney:  payMoney,
			Count:     count,
			IsHot:     cfg.IsHot,
			IsMobile:  cfg.IsMobile,
			FastMode:  cfg.FastMode,
		})

		if err != nil {
			log.Printf("[WARN] 创建订单失败: %v", err)
			congestionStreak = 0
			time.Sleep(interval)
			continue
		}

		// 检查是否成功
		if createResp.IsSuccess() {
			orderID, _ := createResp.GetOrderID()
			log.Printf("[SUCCESS] 抢票成功!!!")
			log.Printf("[SUCCESS] 订单号: %d", orderID)

			// 发送推送通知
			if e.push != nil {
				e.push.Send(
					"🎫 B站抢票成功!",
					fmt.Sprintf("订单号: %d\n项目: %s\n金额: %d元",
						orderID, project.Data.Name, payMoney/100),
				)
			}

			return nil
		}

		log.Printf("[DEBUG] 创建订单响应: errno=%d, code=%d, msg=%s",
			createResp.Errno, createResp.Code, createResp.Msg)
		if isContactRequiredMsg(createResp.Msg) {
			return fmt.Errorf("该项目要求联系人信息（姓名+11位手机号），请补充后重试: %s", createResp.Msg)
		}
		if isCongestionErr(createResp.Errno, createResp.Msg) {
			congestionStreak++
			time.Sleep(congestionBackoff(interval, congestionStreak))
		} else {
			congestionStreak = 0
			time.Sleep(interval)
		}
	}
}

// ============ 状态监控 ============

type Status struct {
	Mu         sync.RWMutex
	State      string
	Attempts   int
	StartTime  time.Time
	OrderID    int64
	Error      error
}

func (s *Status) Update(state string, attempt int) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.State = state
	s.Attempts = attempt
}

func (s *Status) Get() (string, int) {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	return s.State, s.Attempts
}

func (s *Status) SetOrder(id int64) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.OrderID = id
}

func (s *Status) SetError(err error) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.Error = err
}

// ============ 抢票日志 ============

type TicketLog struct {
	Events []LogEvent
	mu     sync.Mutex
}

type LogEvent struct {
	Time    time.Time `json:"time"`
	Type    string    `json:"type"` // info, success, error, debug
	Message string    `json:"message"`
	Data    string    `json:"data,omitempty"`
}

func (l *TicketLog) Add(eventType, msg string, data interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()

	var dataStr string
	if data != nil {
		if j, err := json.Marshal(data); err == nil {
			dataStr = string(j)
		}
	}

	l.Events = append(l.Events, LogEvent{
		Time:    time.Now(),
		Type:    eventType,
		Message: msg,
		Data:    dataStr,
	})
}

func (l *TicketLog) GetEvents() []LogEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.Events
}
