package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-resty/resty/v2"
)

// ============ B站 API 客户端 ============

var seedOnce sync.Once

type Client struct {
	http  *resty.Client
	conf  *Config
	token *CTokenGenerator
}

type Config struct {
	SESSDATA   string
	BILI_JCT   string
	BUVID3     string
	DedeUserID string
	UA         string
}

func NewClient(conf *Config) *Client {
	seedOnce.Do(func() {
		rand.Seed(time.Now().UnixNano())
	})

	if conf.UA == "" {
		conf.UA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"
	}

	client := resty.New().
		SetTimeout(30 * time.Second).
		SetHeader("User-Agent", conf.UA).
		SetHeader("Referer", "https://show.bilibili.com/")

	if conf.SESSDATA != "" {
		client.SetCookie(&http.Cookie{Name: "SESSDATA", Value: conf.SESSDATA})
	}
	if conf.BILI_JCT != "" {
		client.SetCookie(&http.Cookie{Name: "bili_jct", Value: conf.BILI_JCT})
	}
	if conf.BUVID3 != "" {
		client.SetCookie(&http.Cookie{Name: "BUVID3", Value: conf.BUVID3})
	}
	if conf.DedeUserID != "" {
		client.SetCookie(&http.Cookie{Name: "DedeUserID", Value: conf.DedeUserID})
	}

	return &Client{
		http:  client,
		conf:  conf,
		token: NewCTokenGenerator(),
	}
}

// ============ API 端点 ============

// GetProject 获取项目详情
func (c *Client) GetProject(ctx context.Context, projectID string) (*ProjectResponse, error) {
	var resp ProjectResponse
	url := fmt.Sprintf("https://show.bilibili.com/api/ticket/project/getV2?id=%s", projectID)

	_, err := c.http.R().
		SetContext(ctx).
		SetResult(&resp).
		Get(url)

	return &resp, err
}

// GetBuyerInfo 获取购票人信息
func (c *Client) GetBuyerInfo(ctx context.Context) (*BuyerInfoResponse, error) {
	var resp BuyerInfoResponse
	_, err := c.http.R().
		SetContext(ctx).
		SetResult(&resp).
		Get("https://show.bilibili.com/api/ticket/buyer/list")
	return &resp, err
}

// GetTicketToken 获取购票令牌 (prepare阶段)
func (c *Client) GetTicketToken(ctx context.Context, params TokenParams) (*TokenResponse, error) {
	url := fmt.Sprintf("https://show.bilibili.com/api/ticket/order/prepare?project_id=%s", params.ProjectID)

	tokenValue := ""
	if params.IsHot {
		tokenValue = c.token.Generate(false)
	}

	// 参考社区实现：prepare 阶段仅需基础字段 + newRisk
	payload := map[string]any{
		"project_id": params.ProjectID,
		"screen_id":  params.ScreenID,
		"sku_id":     params.SkuID,
		"count":      params.Count,
		"order_type": 1,
		"token":      tokenValue,
		"newRisk":    true,
	}

	var resp TokenResponse

	// 优先按 JSON 方式发送（接口在不同阶段可能更偏向 JSON）
	_, err := c.http.R().
		SetContext(ctx).
		SetHeader("Content-Type", "application/json").
		SetBody(payload).
		SetResult(&resp).
		Post(url)
	if err == nil && (resp.Code != 0 || resp.Errno != 0) {
		// 如果返回了明显的错误码，尝试用表单方式再发一次（兼容旧实现）
		formData := make(map[string]string, len(payload))
		for k, v := range payload {
			switch val := v.(type) {
			case string:
				formData[k] = val
			case int:
				formData[k] = strconv.Itoa(val)
			case int64:
				formData[k] = strconv.FormatInt(val, 10)
			case bool:
				formData[k] = strconv.FormatBool(val)
			}
		}

		var resp2 TokenResponse
		_, err2 := c.http.R().
			SetContext(ctx).
			SetFormData(formData).
			SetResult(&resp2).
			Post(url)
		if err2 == nil {
			resp = resp2
		}
	}

	return &resp, err
}

// ConfirmOrder 确认订单
func (c *Client) ConfirmOrder(ctx context.Context, projectID, token string) (*ConfirmResponse, error) {
	url := fmt.Sprintf("https://show.bilibili.com/api/ticket/order/confirmInfo?token=%s&voucher=&project_id=%s&requestSource=neul-next",
		token, projectID)

	var resp ConfirmResponse
	_, err := c.http.R().
		SetContext(ctx).
		SetResult(&resp).
		Get(url)
	return &resp, err
}

// CreateOrder 创建订单 (createV2阶段)
func (c *Client) CreateOrder(ctx context.Context, params CreateOrderParams) (*CreateOrderResponse, error) {
	url := fmt.Sprintf("https://show.bilibili.com/api/ticket/order/createV2?project_id=%s", params.ProjectID)

	// 生成点击位置
	clickPos := GenerateClickPosition(params.IsMobile, params.FastMode)

	// 生成时间戳
	timestamp := time.Now().UnixMilli()

	data := map[string]interface{}{
		"project_id":     params.ProjectID,
		"screen_id":      params.ScreenID,
		"sku_id":         params.SkuID,
		"token":          params.Token,
		"ctoken":         c.token.Generate(true), // createV2 阶段需要 ctoken
		"ptoken":         params.Ptoken,
		"buyer_info":     params.BuyerInfo,
		"clickPosition":  clickPos,
		"newRisk":        true,
		"requestSource":  "pc-new",
		"deviceId":       c.conf.BUVID3,
		"pay_money":      params.PayMoney,
		"count":          params.Count,
		"timestamp":      timestamp,
		"order_type":     1,
	}

	// 联系人信息：部分项目必填。为空时不要强行带上，避免触发“手机号不正确”。
	if params.ContactName != "" {
		data["buyer"] = params.ContactName
		data["buyer_name"] = params.ContactName
	}
	if params.ContactTel != "" {
		data["tel"] = params.ContactTel
		data["buyer_tel"] = params.ContactTel
	}

	// 添加 risk header
	headers := map[string]string{
		"X-Risk-Header": fmt.Sprintf("platform/h5 uid/%s deviceId/%s",
			c.conf.DedeUserID, c.conf.BUVID3),
	}

	var resp CreateOrderResponse
	_, err := c.http.R().
		SetContext(ctx).
		SetHeaders(headers).
		SetBody(data).
		SetResult(&resp).
		Post(url)

	return &resp, err
}

// ============ CToken 生成器 ============

type CTokenGenerator struct {
	touchEvent       int
	visibilityChange int
	pageUnload       int
	timer            int
	timeDifference   int
	scrollX          int
	scrollY          int
	innerWidth       int
	innerHeight      int
	outerWidth       int
	outerHeight      int
	screenX          int
	screenY          int
	screenWidth      int
	screenHeight     int
	screenAvailWidth int
	ticketCollectT   int64
	timeOffset       int64
	stayTime         int
}

func NewCTokenGenerator() *CTokenGenerator {
	return &CTokenGenerator{
		ticketCollectT: time.Now().Unix(),
		timeOffset:     0,
		stayTime:       5000,
	}
}

func (g *CTokenGenerator) Generate(isCreateV2 bool) string {
	r := rand.Intn

	if isCreateV2 {
		// createV2 阶段
		g.timeDifference = int(time.Now().Unix() + g.timeOffset - g.ticketCollectT)
		g.timer = g.timeDifference + g.stayTime
		g.pageUnload = 25
	} else {
		// prepare 阶段
		g.timeDifference = 0
		g.timer = g.stayTime
		g.touchEvent = r(3) + 3 + 4 // 3-10
	}

	// 固定值
	g.innerWidth = 255
	g.innerHeight = 255
	g.outerWidth = 255
	g.outerHeight = 255
	g.screenWidth = 255
	g.screenHeight = r(2000) + 1000 // 1000-3000
	g.screenAvailWidth = r(100) + 1 // 1-100

	return g.encode()
}

func (g *CTokenGenerator) encode() string {
	buffer := make([]byte, 16)

	// 按特定顺序填充数据
	positions := map[int]struct {
		value  int
		length int
	}{
		0:  {g.touchEvent, 1},
		1:  {g.scrollX, 1},
		2:  {g.visibilityChange, 1},
		3:  {g.scrollY, 1},
		4:  {g.innerWidth, 1},
		5:  {g.pageUnload, 1},
		6:  {g.innerHeight, 1},
		7:  {g.outerWidth, 1},
		8:  {g.timer, 2},
		10: {g.timeDifference, 2},
		12: {g.outerHeight, 1},
		13: {g.screenX, 1},
		14: {g.screenY, 1},
		15: {g.screenWidth, 1},
	}

	for i := 0; i < 16; i++ {
		if pos, ok := positions[i]; ok {
			if pos.length == 1 {
				v := pos.value
				if v > 255 {
					v = 255
				}
				if v < 0 {
					v = 0
				}
				buffer[i] = byte(v)
			} else {
				v := pos.value
				if v > 65535 {
					v = 65535
				}
				if v < 0 {
					v = 0
				}
				buffer[i] = byte((v >> 8) & 0xFF)
				if i+1 < 16 {
					buffer[i+1] = byte(v & 0xFF)
				}
			}
		} else {
			// 条件值
			condVal := g.scrollY
			if (4 & g.screenHeight) == 0 {
				condVal = g.screenAvailWidth
			}
			buffer[i] = byte(condVal & 0xFF)
		}
	}

	// 转换为二进制并 Base64 编码
	binaryStr := ""
	for _, b := range buffer {
		binaryStr += string(rune(b))
	}

	// Uint16Array 转换
	uint8Data := make([]byte, 0, len(binaryStr)*2)
	for _, c := range binaryStr {
		uint8Data = append(uint8Data, byte(c))
		uint8Data = append(uint8Data, byte(c>>8))
	}

	return base64.StdEncoding.EncodeToString(uint8Data)
}

// ============ 点击位置生成 ============

type ClickPosition struct {
	X      int   `json:"x"`
	Y      int   `json:"y"`
	Origin int64 `json:"origin"`
	Now    int64 `json:"now"`
}

func GenerateClickPosition(isMobile, fastMode bool) ClickPosition {
	now := time.Now().UnixMilli()
	r := rand.Intn

	var x, y, offsetRange int

	if isMobile {
		// 手机端确认按钮 (右下角)
		mobileWidth := 1080
		mobileHeight := 2400

		xRatio := float64(r(35)+55) / 100 // 0.55-0.9
		yRatio := float64(r(5)+90) / 100  // 0.9-0.95

		x = int(float64(mobileWidth) * xRatio)
		y = int(float64(mobileHeight) * yRatio)
		offsetRange = 5
	} else {
		// PC端确认按钮 (右侧中下部)
		x = 1131
		y = 636
		offsetRange = 10
	}

	// 随机偏移
	offsetX := r(offsetRange*2+1) - offsetRange
	offsetY := r(offsetRange*2+1) - offsetRange

	// 计算延迟时间
	var delay int
	if fastMode {
		delay = r(3800) + 800 // 800-4600ms
	} else {
		delay = r(8000) + 4000 // 4000-12000ms
	}

	origin := now - int64(delay)

	return ClickPosition{
		X:      x + offsetX,
		Y:      y + offsetY,
		Origin: origin,
		Now:    now,
	}
}

// ============ 响应结构 ============

type ProjectResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Errno   int    `json:"errno"`
	Msg     string `json:"msg"`
	Data    struct {
		ID          int    `json:"id"`
		Name        string `json:"name"`
		IsSale      int    `json:"is_sale"`
		StartTime   int64  `json:"start_time"`
		SaleBegin   int64  `json:"sale_begin"`
		SaleEnd     int64  `json:"sale_end"`
		CountDown   int64  `json:"count_down"`
		IDBind      int    `json:"id_bind"`
		HotProject  bool   `json:"hotProject"`
		ScreenList  []struct {
			ID          int    `json:"id"`
			Name        string `json:"name"`
			StartTime   int64  `json:"start_time"`
			TicketList  []struct {
				ID        int    `json:"id"`
				Price     int    `json:"price"`
				Desc      string `json:"desc"`
				IsSale    int    `json:"is_sale"`
				SaleStart int64  `json:"saleStart"`
			} `json:"ticket_list"`
		} `json:"screen_list"`
	} `json:"data"`
}

type BuyerInfoResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Errno   int    `json:"errno"`
	Data    any    `json:"data"`
}

type Buyer struct {
	ID        int64  `json:"id"`
	UID       int64  `json:"uid"`
	Name      string `json:"name"`
	Tel       string `json:"tel"`
	IsDefault int64  `json:"is_default"`
}

type BuyerAddress struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Tel  string `json:"tel"`
}

type BuyerAddressListResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Errno   int    `json:"errno"`
	Msg     string `json:"msg"`
	Data    any    `json:"data"`
}

func (r *BuyerAddressListResponse) GetAddressList() []BuyerAddress {
	if r.Data == nil {
		return nil
	}
	m, ok := r.Data.(map[string]any)
	if !ok {
		return nil
	}
	list, ok := m["address_list"].([]any)
	if !ok {
		return nil
	}
	out := make([]BuyerAddress, 0, len(list))
	for _, it := range list {
		mi, ok := it.(map[string]any)
		if !ok {
			continue
		}
		var a BuyerAddress
		if v, ok := mi["id"].(float64); ok {
			a.ID = int64(v)
		}
		if v, ok := mi["name"].(string); ok {
			a.Name = v
		}
		if v, ok := mi["tel"].(string); ok {
			a.Tel = v
		}
		if a.ID != 0 || a.Name != "" || a.Tel != "" {
			out = append(out, a)
		}
	}
	return out
}

// GetBuyerList safely extracts buyer list from response (data may be bool when not authenticated)
func (r *BuyerInfoResponse) GetBuyerList() []Buyer {
	if r.Data == nil {
		return nil
	}
	if m, ok := r.Data.(map[string]any); ok {
		if list, ok := m["list"].([]any); ok {
			result := make([]Buyer, 0, len(list))
			for _, item := range list {
				if mi, ok := item.(map[string]any); ok {
					entry := Buyer{}
					if v, ok := mi["id"].(float64); ok {
						entry.ID = int64(v)
					}
					if v, ok := mi["uid"].(float64); ok {
						entry.UID = int64(v)
					}
					if v, ok := mi["name"].(string); ok {
						entry.Name = v
					}
					if v, ok := mi["tel"].(string); ok {
						entry.Tel = v
					}
					if v, ok := mi["is_default"].(float64); ok {
						entry.IsDefault = int64(v)
					}
					result = append(result, entry)
				}
			}
			return result
		}
	}
	return nil
}

// GetBuyerAddressList 获取联系人/地址列表（部分项目下单需要联系人姓名+手机号）
func (c *Client) GetBuyerAddressList(ctx context.Context) (*BuyerAddressListResponse, error) {
	var resp BuyerAddressListResponse
	// 老接口：需要 POST + project_id（有的实现传 0 也能取到）
	_, err := c.http.R().
		SetContext(ctx).
		SetHeader("Content-Type", "application/x-www-form-urlencoded").
		SetFormData(map[string]string{"project_id": "0"}).
		SetResult(&resp).
		Post("https://show.bilibili.com/api/ticket/getBuyerAddressList")
	return &resp, err
}

func SelectBuyersForOrder(all []Buyer, selectedIDs []int64, count int, requireCount bool) ([]Buyer, error) {
	if len(all) == 0 {
		return nil, nil
	}

	byID := make(map[int64]Buyer, len(all))
	for _, b := range all {
		if b.ID == 0 {
			continue
		}
		byID[b.ID] = b
	}

	var chosen []Buyer
	if len(selectedIDs) > 0 {
		seen := make(map[int64]struct{}, len(selectedIDs))
		for _, id := range selectedIDs {
			if id == 0 {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			b, ok := byID[id]
			if !ok {
				return nil, fmt.Errorf("未找到购票人ID=%d（请先在B站添加/同步购票人）", id)
			}
			chosen = append(chosen, b)
		}
	} else {
		// 默认选择：优先默认购票人，其次按列表顺序补齐
		var def *Buyer
		for i := range all {
			if all[i].IsDefault != 0 {
				def = &all[i]
				break
			}
		}
		if def != nil {
			chosen = append(chosen, *def)
		} else {
			chosen = append(chosen, all[0])
		}

		if requireCount && count > 1 {
			used := map[int64]struct{}{chosen[0].ID: {}}
			for _, b := range all {
				if len(chosen) >= count {
					break
				}
				if b.ID == 0 {
					continue
				}
				if _, ok := used[b.ID]; ok {
					continue
				}
				used[b.ID] = struct{}{}
				chosen = append(chosen, b)
			}
		}
	}

	if requireCount && count > 0 && len(chosen) != count {
		return nil, fmt.Errorf("该项目需要按数量绑定实名购票人：数量=%d，但购票人=%d（请在页面勾选足够的购票人）", count, len(chosen))
	}
	if !requireCount && count > 0 && len(chosen) > count {
		chosen = chosen[:count]
	}
	return chosen, nil
}

func BuildBuyerInfoJSON(buyers []Buyer) (string, error) {
	if len(buyers) == 0 {
		return "", nil
	}
	type buyerInfo struct {
		ID   int64  `json:"id"`
		UID  int64  `json:"uid,omitempty"`
		Name string `json:"name,omitempty"`
		Tel  string `json:"tel,omitempty"`
	}
	out := make([]buyerInfo, 0, len(buyers))
	for _, b := range buyers {
		out = append(out, buyerInfo{
			ID:   b.ID,
			UID:  b.UID,
			Name: b.Name,
			Tel:  b.Tel,
		})
	}
	data, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

type TokenParams struct {
	ProjectID string
	ScreenID  string
	SkuID     string
	Count     int
	IsHot     bool
}

type TokenResponse struct {
	Code    int    `json:"code"`
	Errno   int    `json:"errno"`
	Message string `json:"message"`
	Msg     string `json:"msg"`
	Data    any    `json:"data"` // 可能为 bool 或 struct
}

// GetToken safely extracts token from response
func (r *TokenResponse) GetToken() (token, ptoken string) {
	if r.Data == nil {
		return "", ""
	}
	if m, ok := r.Data.(map[string]any); ok {
		if v, ok := m["token"].(string); ok {
			token = v
		}
		if v, ok := m["ptoken"].(string); ok {
			ptoken = v
		}
	}
	return token, ptoken
}

type ConfirmResponse struct {
	Success bool  `json:"success"`
	Errno   int   `json:"errno"`
	Code    int   `json:"code"`
	Msg     string `json:"msg"`
	Data    any    `json:"data"` // 可能为 bool 或 struct
}

// GetConfirmData safely extracts confirm data
func (r *ConfirmResponse) GetConfirmData() (count int, payMoney int64, ok bool) {
	if r.Data == nil {
		return 0, 0, false
	}
	if m, ok := r.Data.(map[string]any); ok {
		if v, ok := m["count"].(float64); ok {
			count = int(v)
		}
		if v, ok := m["pay_money"].(float64); ok {
			payMoney = int64(v)
		}
		return count, payMoney, true
	}
	return 0, 0, false
}

type CreateOrderParams struct {
	ProjectID  string
	ScreenID   string
	SkuID      string
	Token      string
	Ptoken     string
	BuyerInfo  string
	ContactName string
	ContactTel  string
	PayMoney   int64
	Count      int
	IsHot      bool
	IsMobile   bool
	FastMode   bool
}

type CreateOrderResponse struct {
	Success bool   `json:"success"`
	Errno   int    `json:"errno"`
	Code    int    `json:"code"`
	Msg     string `json:"msg"`
	Data    any    `json:"data"` // 可能为 bool 或 struct
}

// GetOrderID safely extracts order ID
func (r *CreateOrderResponse) GetOrderID() (orderID int64, ok bool) {
	if r.Data == nil {
		return 0, false
	}
	if m, ok := r.Data.(map[string]any); ok {
		if v, ok := m["orderId"].(float64); ok {
			return int64(v), true
		}
	}
	return 0, false
}

// IsSuccess checks if the response indicates success
func (r *CreateOrderResponse) IsSuccess() bool {
	if r.Success {
		return true
	}
	if id, ok := r.GetOrderID(); ok && id > 0 {
		return true
	}
	return r.Errno == 0 && r.Code == 0
}

// ============ 辅助函数 ============

func toValues(data map[string]interface{}) map[string]string {
	result := make(map[string]string)
	for k, v := range data {
		switch val := v.(type) {
		case string:
			result[k] = val
		case int:
			result[k] = strconv.Itoa(val)
		case int64:
			result[k] = strconv.FormatInt(val, 10)
		case bool:
			result[k] = strconv.FormatBool(val)
		case float64:
			result[k] = strconv.FormatFloat(val, 'f', -1, 64)
		}
	}
	return result
}

// ParseTokenResponse 解析 token 响应
func ParseTokenResponse(body []byte) (*TokenResponse, error) {
	var resp TokenResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ParseCreateOrderResponse 解析创建订单响应
func ParseCreateOrderResponse(body []byte) (*CreateOrderResponse, error) {
	var resp CreateOrderResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ============ HTTP 辅助 ============

func (c *Client) Post(ctx context.Context, url string, body interface{}) ([]byte, error) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, &buf)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", c.conf.UA)
	req.Header.Set("Referer", "https://show.bilibili.com/")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	buf.Reset()
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}
