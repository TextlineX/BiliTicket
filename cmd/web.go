package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"github.com/yourname/gobiliticket/internal/api"
	"github.com/yourname/gobiliticket/internal/appdir"
)

// ============ 端口检测与启动 ============

var webPort int
var webServerMu sync.Mutex
var webServer *http.Server

func initLogging() {
	logDir := appdir.LogsDir()
	_ = os.MkdirAll(logDir, 0755)
	logFile := filepath.Join(logDir, "app.log")

	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err == nil {
		// 同时输出到文件和控制台
		multiWriter := io.MultiWriter(f, os.Stdout)
		log.SetOutput(multiWriter)
	}
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.SetPrefix("[gobiliticket] ")
}

// ============ 端口检测与启动 ============

var webCmd = &cobra.Command{
	Use:   "web",
	Short: "启动 Web 管理界面（双击运行）",
	Long:  `双击即可运行，自动打开浏览器。端口被占用时自动尝试下一个端口。`,
	RunE:  runWeb,
}

func init() {
	rootCmd.AddCommand(webCmd)
	webCmd.Flags().IntVarP(&webPort, "port", "p", 8080, "起始端口（端口冲突时自动递增）")
}

func runWeb(cmd *cobra.Command, args []string) error {
	port := webPort
	if port <= 0 {
		port = 8080
	}

	log.Println("B站抢票工具启动中...")

	const maxAttempts = 20
	var listener net.Listener
	var usedPorts []int

	for attempt := 0; attempt < maxAttempts; attempt++ {
		addr := fmt.Sprintf(":%d", port+attempt)
		l, err := net.Listen("tcp", addr)
		if err != nil {
			usedPorts = append(usedPorts, port+attempt)
			log.Printf("端口 %d 被占用，尝试下一个...", port+attempt)
			continue
		}
		listener = l
		port = port + attempt
		break
	}

	if listener == nil {
		msg := fmt.Sprintf("无法找到可用端口，已尝试: %s\n请关闭占用端口的程序后重试。", intsToString(usedPorts))
		log.Printf("错误: %s", msg)
		showFatalIfNoConsole("GoBiliTicket 启动失败", msg)
		return fmt.Errorf(msg)
	}

	actualPort := listener.Addr().(*net.TCPAddr).Port
	urlStr := fmt.Sprintf("http://localhost:%d", actualPort)

	hideConsoleWindow()

	log.Printf("服务已启动: %s", urlStr)
	if actualPort != webPort {
		log.Printf("端口 %d 被占用，自动切换到 %d", webPort, actualPort)
	}

	go openBrowserInBackground(urlStr)

	server := &http.Server{
		Handler: newWebRouter(),
	}
	webServerMu.Lock()
	webServer = server
	webServerMu.Unlock()

	log.Println("Web管理界面就绪，请在浏览器中打开上述地址")
	err := server.Serve(listener)
	if err == http.ErrServerClosed {
		log.Println("服务已关闭")
		return nil
	}
	if err != nil {
		showFatalIfNoConsole("GoBiliTicket 运行异常", err.Error())
	}
	return err
}

func intsToString(ports []int) string {
	var sb strings.Builder
	for i, p := range ports {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(strconv.Itoa(p))
	}
	return sb.String()
}

func hideConsoleWindow() {}

// ============ HTTP 路由 ============

func newWebRouter() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", handleWebIndex)
	mux.HandleFunc("/api/app/exit", handleAppExit)
	mux.HandleFunc("/api/login/qr", handleLoginQR)
	mux.HandleFunc("/api/login/poll", handleLoginPoll)
	mux.HandleFunc("/api/login/status", handleLoginStatus)
	mux.HandleFunc("/api/buyers", handleAPIBuyers)
	mux.HandleFunc("/api/projects", handleAPIProjects)
	mux.HandleFunc("/api/project/", handleAPIProjectDetail)
	mux.HandleFunc("/api/history", handleAPIHistory)
	mux.HandleFunc("/api/history/", handleAPIHistoryAction)
	mux.HandleFunc("/api/buy/start", handleBuyStart)
	mux.HandleFunc("/api/buy/stop", handleBuyStop)
	mux.HandleFunc("/api/buy/status", handleBuyStatus)
	mux.HandleFunc("/api/buy/stream", handleBuyStream)
	return mux
}

func handleAppExit(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != "POST" {
		json.NewEncoder(w).Encode(map[string]any{"code": 1, "error": "invalid method"})
		return
	}

	webServerMu.Lock()
	server := webServer
	webServerMu.Unlock()
	if server == nil {
		json.NewEncoder(w).Encode(map[string]any{"code": 1, "error": "server not running"})
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()

	json.NewEncoder(w).Encode(map[string]any{"code": 0})
}

// ============ 页面 ============

func handleWebIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(buildWebPage()))
}

// ============ 登录 API ============

type loginQRResult struct {
	QRURL   string `json:"qr_url"`
	OAuthKey string `json:"oauth_key"`
}

func handleLoginQR(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	ctx := r.Context()

	reqURL := "https://passport.bilibili.com/x/passport-login/web/qrcode/generate"
	client := &http.Client{Timeout: 30 * time.Second}
	req, _ := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Referer", "https://passport.bilibili.com/")

	resp, err := client.Do(req)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]any{"code": 1, "error": err.Error()})
		return
	}
	defer resp.Body.Close()

	var result struct {
		Code int `json:"code"`
		Data   struct {
			URL      string `json:"url"`
			QrcodeKey string `json:"qrcode_key"`
		} `json:"data"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&result)

	if result.Code != 0 || result.Data.URL == "" || result.Data.QrcodeKey == "" {
		json.NewEncoder(w).Encode(map[string]any{"code": 1, "error": "获取二维码失败"})
		return
	}

	// 保存 qrcode_key 到内存，供轮询使用
	webLoginKey = result.Data.QrcodeKey
	webQRURL = result.Data.URL

	// 生成二维码图片 URL
	qrImgURL := fmt.Sprintf("https://api.qrserver.com/v1/create-qr-code/?size=300x300&margin=10&data=%s",
		url.QueryEscape(result.Data.URL))

	out := map[string]any{
		"code":      0,
		"qr_url":    qrImgURL,
		"oauth_key": result.Data.QrcodeKey,
	}
	if isWebDebug(r) {
		out["debug"] = map[string]any{
			"http_status": resp.StatusCode,
			"final_url":   resp.Request.URL.String(),
			"qrcode_key":  maskCookieForDebug(result.Data.QrcodeKey),
		}
	}
	json.NewEncoder(w).Encode(out)
}

var (
	webLoginKey   string
	webQRURL      string
	webLoginDone  bool
	webLoginMu    sync.RWMutex
)

func isWebDebug(r *http.Request) bool {
	v := r.URL.Query().Get("debug")
	return v == "1" || strings.EqualFold(v, "true")
}

func maskCookieForDebug(v string) string {
	if v == "" {
		return ""
	}
	if len(v) <= 12 {
		return v[:1] + "***" + v[len(v)-1:]
	}
	return v[:6] + "..." + v[len(v)-4:]
}

func handleLoginPoll(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	ctx := r.Context()

	key := r.URL.Query().Get("key")
	if key == "" {
		key = webLoginKey
	}

	apiURL := "https://passport.bilibili.com/x/passport-login/web/qrcode/poll?qrcode_key=" + url.QueryEscape(key)

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Timeout: 30 * time.Second, Jar: jar}
	req, _ := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Referer", "https://passport.bilibili.com/")

	resp, err := client.Do(req)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]any{"code": 1, "status": -1, "error": err.Error()})
		return
	}
	defer resp.Body.Close()

	cookies := extractLoginCookiesFromJar(jar)
	mergeLoginCookiesFromHTTPCookies(&cookies, resp.Cookies())

	// 登录成功
	if cookies.SESSDATA != "" {
		saveCookiesWeb(&cookies)
		webLoginMu.Lock()
		webLoginDone = true
		webLoginMu.Unlock()
		out := map[string]any{
			"code":   0,
			"status": 200,
			"msg":    "登录成功",
		}
		if isWebDebug(r) {
			out["debug"] = map[string]any{
				"http_status": resp.StatusCode,
				"final_url":   resp.Request.URL.String(),
				"cookies": map[string]any{
					"SESSDATA":   maskCookieForDebug(cookies.SESSDATA),
					"BILI_JCT":   maskCookieForDebug(cookies.BILI_JCT),
					"BUVID3":     maskCookieForDebug(cookies.BUVID3),
					"DedeUserID": cookies.DedeUserID,
				},
			}
		}
		json.NewEncoder(w).Encode(out)
		return
	}

	// 解析响应体
	body, _ := io.ReadAll(resp.Body)
	var pollResult struct {
		Code int    `json:"code"`
		Msg  string `json:"message"`
		Data struct {
			Code int    `json:"code"`
			Msg  string `json:"message"`
		} `json:"data"`
	}
	_ = json.Unmarshal(body, &pollResult)

	// 新接口：root code==0 代表请求成功，data.code 表示扫码状态
	rootCode := pollResult.Code
	rootMsg := pollResult.Msg
	dataCode := pollResult.Data.Code
	dataMsg := pollResult.Data.Msg

	statusCode := rootCode
	statusMsg := rootMsg
	if rootCode == 0 {
		statusCode = dataCode
		statusMsg = dataMsg
	}

	// 映射成前端兼容的状态码
	// status: -1=等待扫码, 0=已扫码待确认, 4=过期, 5=取消, 200=成功
	mappedStatus := statusCode
	mappedMsg := statusMsg
	switch statusCode {
	case 86101:
		mappedStatus = -1
		mappedMsg = "等待扫码"
	case 86090:
		mappedStatus = 0
		mappedMsg = "已扫码，请在手机端确认"
	case 86038:
		mappedStatus = 4
		mappedMsg = "二维码已失效，请刷新"
	case 0:
		// 按理说 0 会伴随 Set-Cookie；若没有拿到 SESSDATA，就提示重试
		mappedStatus = 200
		if cookies.SESSDATA == "" {
			mappedMsg = "登录成功但未获取Cookie，请重试"
		} else {
			mappedMsg = "登录成功"
		}
	case 200000:
		mappedMsg = "系统繁忙"
	}
	if mappedMsg == "" {
		mappedMsg = statusMsg
	}

	out := map[string]any{
		"code":   0,
		"status": mappedStatus,
		"msg":    mappedMsg,
	}
	if isWebDebug(r) {
		bodyHead := string(body)
		bodyHead = strings.TrimSpace(bodyHead)
		if len(bodyHead) > 300 {
			bodyHead = bodyHead[:300]
		}
		out["debug"] = map[string]any{
			"http_status":  resp.StatusCode,
			"final_url":    resp.Request.URL.String(),
			"content_type": resp.Header.Get("Content-Type"),
			"set_cookie":   len(resp.Cookies()),
			"root_code":    rootCode,
			"root_msg":     rootMsg,
			"data_code":    dataCode,
			"data_msg":     dataMsg,
			"cookies": map[string]any{
				"SESSDATA":   maskCookieForDebug(cookies.SESSDATA),
				"BILI_JCT":   maskCookieForDebug(cookies.BILI_JCT),
				"BUVID3":     maskCookieForDebug(cookies.BUVID3),
				"DedeUserID": cookies.DedeUserID,
			},
			"body_head": bodyHead,
		}
	}
	json.NewEncoder(w).Encode(out)
}

func handleLoginStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	cookiePath := appdir.FindCookiesPath()

	data, err := os.ReadFile(cookiePath)
	var sessdata string
	if err == nil {
		var saved savedCookiesFile
		if json.Unmarshal(data, &saved) == nil && saved.SESSDATA != "" {
			sessdata = saved.SESSDATA
		}
	}

	loggedIn := sessdata != ""
	masked := ""
	if loggedIn && len(sessdata) > 12 {
		masked = sessdata[:6] + "..." + sessdata[len(sessdata)-4:]
	}

	json.NewEncoder(w).Encode(map[string]any{
		"code":     0,
		"loggedin": loggedIn,
		"user":     masked,
	})
}

func handleAPIBuyers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != "GET" {
		json.NewEncoder(w).Encode(map[string]any{"code": 1, "error": "invalid method"})
		return
	}

	// 检查是否已登录
	cookiePath := appdir.FindCookiesPath()
	data, err := os.ReadFile(cookiePath)
	if err != nil || len(data) == 0 {
		json.NewEncoder(w).Encode(map[string]any{"code": 2, "error": "请先登录"})
		return
	}

	var saved savedCookiesFile
	if err := json.Unmarshal(data, &saved); err != nil || saved.SESSDATA == "" {
		json.NewEncoder(w).Encode(map[string]any{"code": 2, "error": "请先登录"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	client := api.NewClient(&api.Config{
		SESSDATA:   saved.SESSDATA,
		BILI_JCT:   saved.BILI_JCT,
		BUVID3:     saved.BUVID3,
		DedeUserID: saved.DedeUserID,
	})

	buyerInfo, err := client.GetBuyerInfo(ctx)
	if err != nil || buyerInfo == nil {
		json.NewEncoder(w).Encode(map[string]any{"code": 0, "buyers": []any{}})
		return
	}

	list := buyerInfo.GetBuyerList()
	out := make([]map[string]any, 0, len(list))
	for _, b := range list {
		out = append(out, map[string]any{
			"id":         b.ID,
			"name":       b.Name,
			"tel_masked": maskTel(b.Tel),
			"is_default": b.IsDefault != 0,
		})
	}
	json.NewEncoder(w).Encode(map[string]any{"code": 0, "buyers": out})
}

func maskTel(tel string) string {
	tel = strings.TrimSpace(tel)
	if len(tel) < 7 {
		return tel
	}
	return tel[:3] + "****" + tel[len(tel)-4:]
}

func normalizeCNPhone(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "*") {
		// 有些接口可能返回脱敏手机号，不能直接用于下单
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
	// 兼容 +86 / 86 前缀
	if len(s) > 11 {
		last := s[len(s)-11:]
		if len(last) == 11 {
			return last
		}
	}
	return ""
}

func isContactRequiredMsg(msg string) bool {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return false
	}
	return strings.Contains(msg, "联系人") && (strings.Contains(msg, "手机号") || strings.Contains(msg, "姓名"))
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
	// 2^streak * base，最多放大到 32 倍（避免无限增长）
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

func saveCookiesWeb(cookies *loginCookies) error {
	savePath := appdir.CookiesPath()
	dir := filepath.Dir(savePath)
	os.MkdirAll(dir, 0755)

	cfg := savedConfig{
		SESSDATA:   cookies.SESSDATA,
		BILI_JCT:   cookies.BILI_JCT,
		BUVID3:     cookies.BUVID3,
		DedeUserID: cookies.DedeUserID,
		SavedAt:    time.Now().Format("2006-01-02 15:04:05"),
	}

	data, _ := json.MarshalIndent(cfg, "", "  ")
	return os.WriteFile(savePath, data, 0600)
}

// ============ 抢票 API ============

// BuyTask 抢票任务状态
type BuyTask struct {
	mu       sync.RWMutex
	running  bool
	done     bool
	success  bool
	orderID  int64
	stage    string
	logs     []BuyLog
	ctx      context.Context
	cancel   context.CancelFunc
	logChan  chan BuyLog
}

type BuyLog struct {
	Time    string `json:"time"`
	Type    string `json:"type"` // info, success, error
	Message string `json:"message"`
}

var buyTask = &BuyTask{
	logChan: make(chan BuyLog, 50),
}

func handleBuyStart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != "POST" {
		json.NewEncoder(w).Encode(map[string]any{"code": 1, "error": "invalid method"})
		return
	}

	var req struct {
		ProjectID   string `json:"project_id"`
		ScheduleID  string `json:"schedule_id"`
		ItemID      string `json:"item_id"`
		Count       int    `json:"count"`
		IntervalMs  int    `json:"interval_ms"`
		IsHot       bool   `json:"is_hot"`
		IsMobile    bool   `json:"is_mobile"`
		BuyerIDs    []int64 `json:"buyer_ids"`
		ContactName string `json:"contact_name"`
		ContactTel  string `json:"contact_tel"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(map[string]any{"code": 1, "error": "参数错误"})
		return
	}

	if req.Count <= 0 {
		req.Count = 1
	}
	if req.IntervalMs <= 0 {
		req.IntervalMs = 500
	}

	// 检查是否已登录
	cookiePath := appdir.FindCookiesPath()
	data, err := os.ReadFile(cookiePath)
	if err != nil || len(data) == 0 {
		json.NewEncoder(w).Encode(map[string]any{"code": 2, "error": "请先登录"})
		return
	}

	var saved savedCookiesFile
	if err := json.Unmarshal(data, &saved); err != nil || saved.SESSDATA == "" {
		json.NewEncoder(w).Encode(map[string]any{"code": 2, "error": "请先登录"})
		return
	}

	// 启动抢票
	go buyTask.start(req, saved)

	json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "抢票已开始"})
}

func (t *BuyTask) start(req struct {
	ProjectID  string `json:"project_id"`
	ScheduleID string `json:"schedule_id"`
	ItemID     string `json:"item_id"`
	Count      int    `json:"count"`
	IntervalMs int    `json:"interval_ms"`
	IsHot      bool   `json:"is_hot"`
	IsMobile   bool   `json:"is_mobile"`
	BuyerIDs   []int64 `json:"buyer_ids"`
	ContactName string `json:"contact_name"`
	ContactTel  string `json:"contact_tel"`
}, saved savedCookiesFile) {

	t.mu.Lock()
	if t.running {
		t.mu.Unlock()
		return
	}
	t.running = true
	t.done = false
	t.success = false
	t.orderID = 0
	t.logs = nil
	t.ctx, t.cancel = context.WithCancel(context.Background())
	logChan := t.logChan
	t.mu.Unlock()

	t.emit(logChan, "info", "===== 抢票任务启动 =====")
	t.emit(logChan, "info", "项目ID: "+req.ProjectID+"  场次: "+req.ScheduleID+"  票档: "+req.ItemID)

	// 使用 internal/api（新接口）避免旧接口失效
	client := api.NewClient(&api.Config{
		SESSDATA:   saved.SESSDATA,
		BILI_JCT:   saved.BILI_JCT,
		BUVID3:     saved.BUVID3,
		DedeUserID: saved.DedeUserID,
	})

	projectID := req.ProjectID
	screenID := req.ScheduleID
	skuID := req.ItemID
	count := req.Count
	intervalMs := req.IntervalMs

	attempts := 0
	interval := time.Duration(intervalMs) * time.Millisecond
	congestionStreak := 0

	// 获取项目详情（仅用于日志）
	requireBuyerCount := false
	if proj, err := client.GetProject(t.ctx, projectID); err == nil && proj != nil && proj.Data.Name != "" {
		t.emit(logChan, "info", "项目: "+proj.Data.Name)
		requireBuyerCount = proj.Data.IDBind != 0
	}

	// 获取购票人信息（createV2 需要 buyer_info）
	buyerJSON := ""
	contactName := strings.TrimSpace(req.ContactName)
	contactTel := normalizeCNPhone(req.ContactTel)
	if buyerInfo, err := client.GetBuyerInfo(t.ctx); err == nil && buyerInfo != nil {
		list := buyerInfo.GetBuyerList()
		chosen, selErr := api.SelectBuyersForOrder(list, req.BuyerIDs, count, requireBuyerCount)
		if selErr != nil {
			t.emit(logChan, "error", selErr.Error())
			goto done
		}
		if len(chosen) > 0 {
			if contactName == "" {
				contactName = strings.TrimSpace(chosen[0].Name)
			}
			if contactTel == "" {
				contactTel = normalizeCNPhone(chosen[0].Tel)
			}
		}
		if j, err := api.BuildBuyerInfoJSON(chosen); err == nil {
			buyerJSON = j
		}
	}
	// 兜底：购票人列表里手机号可能为空/脱敏，尝试从联系人地址列表获取
	if contactName == "" || contactTel == "" {
		if addrResp, err := client.GetBuyerAddressList(t.ctx); err == nil && addrResp != nil {
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
	if buyerJSON == "" {
		t.emit(logChan, "info", "购票人信息为空，可能影响下单（请确认账号已绑定购票人）")
	}
	if contactName == "" || contactTel == "" {
		t.emit(logChan, "info", "提示: 本项目可能需要联系人信息（姓名+手机号），当前未获取到完整联系人信息")
	}

	for {
		select {
		case <-t.ctx.Done():
			t.emit(logChan, "info", "用户取消抢票")
			goto done
		default:
		}

		attempts++
		t.emit(logChan, "info", "第 "+fmt.Sprintf("%d", attempts)+" 次尝试...")

		// 1) prepare: 获取 token
		tokenResp, err := client.GetTicketToken(t.ctx, api.TokenParams{
			ProjectID: projectID,
			ScreenID:  screenID,
			SkuID:     skuID,
			Count:     count,
			IsHot:     req.IsHot,
		})
		if err != nil {
			t.emit(logChan, "error", "获取Token失败: "+err.Error())
			congestionStreak = 0
			time.Sleep(interval)
			continue
		}
		if tokenResp == nil {
			t.emit(logChan, "error", "获取Token失败: 空响应")
			congestionStreak = 0
			time.Sleep(interval)
			continue
		}
		if tokenResp.Code != 0 || tokenResp.Errno != 0 {
			msg := tokenResp.Msg
			if msg == "" {
				msg = tokenResp.Message
			}
			t.emit(logChan, "error", fmt.Sprintf("获取Token失败: code=%d errno=%d msg=%s", tokenResp.Code, tokenResp.Errno, msg))
			if tokenResp.Code == 83000004 {
				t.emit(logChan, "info", "提示: 该错误通常是风控/人机验证/环境异常导致。建议先在B站官方页面/APP完成验证，避免频繁刷新或关闭代理后重试。")
			}
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
			t.emit(logChan, "error", "获取Token失败: token为空（可能需要人机验证/风控）")
			congestionStreak = 0
			time.Sleep(interval)
			continue
		}

		// 2) confirm
		confirmResp, err := client.ConfirmOrder(t.ctx, projectID, token)
		if err != nil {
			t.emit(logChan, "error", "确认订单失败: "+err.Error())
			congestionStreak = 0
			time.Sleep(interval)
			continue
		}
		if confirmResp == nil || confirmResp.Errno != 0 || confirmResp.Code != 0 {
			msg := "确认订单失败"
			if confirmResp != nil {
				msg = fmt.Sprintf("确认订单失败: code=%d errno=%d msg=%s", confirmResp.Code, confirmResp.Errno, confirmResp.Msg)
			}
			t.emit(logChan, "error", msg)
			if confirmResp != nil && isCongestionErr(confirmResp.Errno, confirmResp.Msg) {
				congestionStreak++
				time.Sleep(congestionBackoff(interval, congestionStreak))
			} else {
				congestionStreak = 0
				time.Sleep(interval)
			}
			continue
		}

		count2, payMoney, hasData := confirmResp.GetConfirmData()
		if !hasData {
			t.emit(logChan, "error", "确认订单无有效数据")
			congestionStreak = 0
			time.Sleep(interval)
			continue
		}

		// 3) createV2
		createResp, err := client.CreateOrder(t.ctx, api.CreateOrderParams{
			ProjectID:  projectID,
			ScreenID:   screenID,
			SkuID:      skuID,
			Token:      token,
			Ptoken:     ptoken,
			BuyerInfo:  buyerJSON,
			ContactName: contactName,
			ContactTel:  contactTel,
			PayMoney:   payMoney,
			Count:      count2,
			IsHot:      req.IsHot,
			IsMobile:   req.IsMobile,
			FastMode:   false,
		})
		if err != nil {
			t.emit(logChan, "error", "创建订单失败: "+err.Error())
			congestionStreak = 0
			time.Sleep(interval)
			continue
		}
		if createResp == nil {
			t.emit(logChan, "error", "创建订单失败: 空响应")
			congestionStreak = 0
			time.Sleep(interval)
			continue
		}

		orderID, _ := createResp.GetOrderID()
		if createResp.IsSuccess() && orderID > 0 {
			t.mu.Lock()
			t.success = true
			t.done = true
			t.mu.Unlock()

			t.mu.Lock()
			t.orderID = orderID
			t.mu.Unlock()

			t.emit(logChan, "success", "🎉 抢票成功！！！")
			t.emit(logChan, "success", "订单号: "+fmt.Sprintf("%d", orderID))
			t.emit(logChan, "success", "金额: ¥"+fmt.Sprintf("%.2f", float64(payMoney)/100))
			goto done
		}

		t.emit(logChan, "info", fmt.Sprintf("下单未成功: code=%d errno=%d msg=%s", createResp.Code, createResp.Errno, createResp.Msg))
		if isContactRequiredMsg(createResp.Msg) || strings.Contains(createResp.Msg, "请填写正确的联系人手机号") {
			t.emit(logChan, "error", "该项目要求联系人信息，请在页面填写联系人姓名与11位手机号后重试")
			goto done
		}
		if isCongestionErr(createResp.Errno, createResp.Msg) {
			congestionStreak++
			time.Sleep(congestionBackoff(interval, congestionStreak))
		} else {
			congestionStreak = 0
			time.Sleep(interval)
		}
	}

done:
	t.mu.Lock()
	t.running = false
	t.done = true
	t.mu.Unlock()
}

func (t *BuyTask) emit(ch chan BuyLog, typ, msg string) {
	log := BuyLog{
		Time:    time.Now().Format("15:04:05"),
		Type:    typ,
		Message: msg,
	}
	t.mu.Lock()
	t.logs = append(t.logs, log)
	t.mu.Unlock()
	select {
	case ch <- log:
	default:
	}
}

type apiConfig struct {
	SESSDATA, BILI_JCT, BUVID3, DedeUserID string
}

func (t *BuyTask) httpPost(ctx context.Context, apiURL string, data url.Values, conf apiConfig) map[string]any {
	client := &http.Client{Timeout: 30 * time.Second}
	req, _ := http.NewRequestWithContext(ctx, "POST", apiURL, strings.NewReader(data.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Referer", "https://show.bilibili.com/")
	cookie := "SESSDATA=" + conf.SESSDATA + "; BUVID3=" + conf.BUVID3
	if conf.BILI_JCT != "" {
		cookie += "; bili_jct=" + conf.BILI_JCT
	}
	if conf.DedeUserID != "" {
		cookie += "; DedeUserID=" + conf.DedeUserID
	}
	req.Header.Set("Cookie", cookie)

	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	return result
}

func (t *BuyTask) httpGet(ctx context.Context, apiURL string, conf apiConfig) map[string]any {
	client := &http.Client{Timeout: 30 * time.Second}
	req, _ := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Referer", "https://show.bilibili.com/")
	cookie := "SESSDATA=" + conf.SESSDATA + "; BUVID3=" + conf.BUVID3
	if conf.BILI_JCT != "" {
		cookie += "; bili_jct=" + conf.BILI_JCT
	}
	if conf.DedeUserID != "" {
		cookie += "; DedeUserID=" + conf.DedeUserID
	}
	req.Header.Set("Cookie", cookie)

	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	return result
}

func (t *BuyTask) getBuyer(conf apiConfig) (buyerID int64, buyerJSON string) {
	ctx := t.ctx
	data := url.Values{}
	data.Set("project_id", "0")

	resp := t.httpPost(ctx, "https://show.bilibili.com/api/ticket/getBuyerAddressList", data, conf)
	if resp == nil {
		return
	}

	dataVal, ok := resp["data"].(map[string]any)
	if !ok {
		return
	}
	listVal, ok := dataVal["address_list"].([]any)
	if !ok || len(listVal) == 0 {
		return
	}

	first, ok := listVal[0].(map[string]any)
	if !ok {
		return
	}

	if id, ok := first["id"].(float64); ok {
		buyerID = int64(id)
	}
	name := ""
	if n, ok := first["name"].(string); ok {
		name = n
	}
	tel := ""
	if t, ok := first["tel"].(string); ok {
		tel = t
	}

	buyerJSON = fmt.Sprintf(`[{"id":%d,"name":"%s","tel":"%s"}]`, buyerID, name, tel)
	return
}

func (t *BuyTask) getToken(conf apiConfig, projectID, screenID, skuID string, count int, isHot bool) map[string]any {
	ctx := t.ctx
	apiURL := "https://show.bilibili.com/api/ticket/getTicketToken"
	data := url.Values{}
	data.Set("project_id", projectID)
	data.Set("screen_id", screenID)
	data.Set("sku_id", skuID)
	data.Set("count", strconv.Itoa(count))

	resp := t.httpPost(ctx, apiURL, data, conf)
	if resp == nil {
		return nil
	}
	code := 0
	if c, ok := resp["code"].(float64); ok {
		code = int(c)
	}
	if code != 0 {
		return nil
	}
	return resp
}

func (t *BuyTask) extractToken(resp map[string]any) (token, ptoken string) {
	if d, ok := resp["data"].(map[string]any); ok {
		if v, ok := d["token"].(string); ok {
			token = v
		}
		if v, ok := d["ptoken"].(string); ok {
			ptoken = v
		}
	}
	return
}

func (t *BuyTask) confirmOrder(conf apiConfig, projectID, token string) *confirmResp {
	ctx := t.ctx
	apiURL := "https://show.bilibili.com/api/ticket/confirmOrder"
	data := url.Values{}
	data.Set("project_id", projectID)
	data.Set("token", token)

	resp := t.httpPost(ctx, apiURL, data, conf)
	if resp == nil {
		return nil
	}

	r := &confirmResp{}
	if d, ok := resp["data"].(map[string]any); ok {
		if v, ok := d["count"].(float64); ok {
			r.Count = int(v)
		}
		if v, ok := d["pay_money"].(float64); ok {
			r.PayMoney = int(v)
		}
		if v, ok := d["has_data"].(bool); ok {
			r.HasData = v
		}
	}
	if v, ok := resp["errno"].(float64); ok {
		r.Errno = int(v)
	}
	if v, ok := resp["code"].(float64); ok {
		r.Code = int(v)
	}
	if v, ok := resp["msg"].(string); ok {
		r.Msg = v
	}
	return r
}

type confirmResp struct {
	Count   int
	PayMoney int
	HasData bool
	Errno   int
	Code    int
	Msg     string
}

func (t *BuyTask) extractConfirm(r *confirmResp) (count, payMoney int, hasData bool) {
	return r.Count, r.PayMoney, r.HasData
}

func (t *BuyTask) createOrder(conf apiConfig, projectID, screenID, skuID, token, ptoken, buyerInfo string, payMoney, count int, isHot, isMobile bool) map[string]any {
	ctx := t.ctx
	apiURL := "https://show.bilibili.com/api/ticket/createTicketOrder"

	// 构建 ctoken
	ctoken := generateCToken(token, projectID, screenID)

	// 构建 extra_data
	extraData := map[string]any{
		"devicere": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
		"reVisited": 1,
	}
	extraJSON, _ := json.Marshal(extraData)

	data := url.Values{}
	data.Set("project_id", projectID)
	data.Set("screen_id", screenID)
	data.Set("sku_id", skuID)
	data.Set("token", token)
	data.Set("ctoken", ctoken)
	data.Set("count", strconv.Itoa(count))
	data.Set("pay_money", strconv.Itoa(payMoney))
	data.Set("buyer_info", buyerInfo)
	data.Set("extra_data", string(extraJSON))
	if isMobile {
		data.Set("device", "h5")
		data.Set("platform", "h5")
	}

	resp := t.httpPost(ctx, apiURL, data, conf)
	return resp
}

func handleBuyStop(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	buyTask.mu.RLock()
	running := buyTask.running
	buyTask.mu.RUnlock()

	if running {
		buyTask.cancel()
		json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "已停止"})
	} else {
		json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "未在运行"})
	}
}

func handleBuyStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	buyTask.mu.RLock()
	running := buyTask.running
	done := buyTask.done
	success := buyTask.success
	orderID := buyTask.orderID
	logs := make([]BuyLog, len(buyTask.logs))
	copy(logs, buyTask.logs)
	buyTask.mu.RUnlock()

	json.NewEncoder(w).Encode(map[string]any{
		"code":    0,
		"running": running,
		"done":    done,
		"success": success,
		"order_id": orderID,
		"logs":    logs,
	})
}

func handleBuyStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}

	// 发送初始日志
	buyTask.mu.RLock()
	logs := make([]BuyLog, len(buyTask.logs))
	copy(logs, buyTask.logs)
	buyTask.mu.RUnlock()
	for _, l := range logs {
		data, _ := json.Marshal(l)
		fmt.Fprintf(w, "data: %s\n\n", data)
	}
	flusher.Flush()

	// 监听新日志
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	closed := r.Context().Done()
	for {
		select {
		case <-closed:
			return
		case <-ticker.C:
			buyTask.mu.RLock()
			currLogs := make([]BuyLog, len(buyTask.logs))
			copy(currLogs, buyTask.logs)
			buyTask.mu.RUnlock()
			for _, l := range currLogs[len(logs):] {
				data, _ := json.Marshal(l)
				fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
			}
			if len(currLogs) > len(logs) {
				logs = currLogs
			}
			// 如果完成，发送结束信号
			buyTask.mu.RLock()
			done := buyTask.done
			buyTask.mu.RUnlock()
			if done {
				fmt.Fprintf(w, "data: {\"type\":\"done\"}\n\n")
				flusher.Flush()
				return
			}
		}
	}
}

// ============ ctoken 生成 (简化版) ============

func generateCToken(token, projectID, screenID string) string {
	// 实际 B站 使用 JS 生成，这里用简化版
	input := token + projectID + screenID + "BilibiliTicket"
	hash := simpleHash(input)
	return hash
}

func simpleHash(s string) string {
	var h uint32 = 5381
	for i := 0; i < len(s); i++ {
		h = ((h << 5) + h) + uint32(s[i])
	}
	result := ""
	chars := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"
	for h > 0 {
		result = string(chars[h&63]) + result
		h >>= 6
	}
	if result == "" {
		result = "."
	}
	return result
}

// ============ 演出发现 API ============

type savedCookiesFile struct {
	SESSDATA   string `json:"sessdata"`
	BILI_JCT   string `json:"bili_jct"`
	BUVID3     string `json:"buvid3"`
	DedeUserID string `json:"dede_user_id"`
}

func handleAPIProjects(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 30 {
		limit = 12
	}
	typ := r.URL.Query().Get("type")

	var projects []projectItem
	var err error

	switch typ {
	case "hot":
		projects, err = fetchHotProjectsWeb(ctx, limit)
	case "upcoming":
		projects, err = fetchUpcomingProjectsWeb(ctx, limit)
	default:
		projects, err = fetchHotProjectsWeb(ctx, limit)
	}

	if err != nil || projects == nil {
		projects = []projectItem{}
	}
	json.NewEncoder(w).Encode(map[string]any{"code": 0, "projects": projects})
}

func fetchHotProjectsWeb(ctx context.Context, limit int) ([]projectItem, error) {
	apiURL := fmt.Sprintf("https://show.bilibili.com/api/ticket/project/listV2?filterHt=false&page=1&pagesize=%d&platform=h5&type=1&area=0", limit)
	client := &http.Client{Timeout: 30 * time.Second}
	req, _ := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 14_0 like Mac OS X) AppleWebKit/605.1.15")
	req.Header.Set("Referer", "https://show.bilibili.com/")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Code  int `json:"code"`
		Errno int `json:"errno"`
		Data  struct {
			Result []map[string]any `json:"result"`
		} `json:"data"`
	}

	_ = json.NewDecoder(resp.Body).Decode(&result)
	if result.Code != 0 || result.Errno != 0 {
		return nil, fmt.Errorf("API error: code=%d errno=%d", result.Code, result.Errno)
	}

	items := make([]projectItem, 0, len(result.Data.Result))
	for _, p := range result.Data.Result {
		id := 0
		if v, ok := p["project_id"].(float64); ok {
			id = int(v)
		} else if v, ok := p["id"].(float64); ok {
			id = int(v)
		}

		name := ""
		if v, ok := p["project_name"].(string); ok {
			name = v
		} else if v, ok := p["name"].(string); ok {
			name = v
		}

		priceLow, priceHigh := 0, 0
		if v, ok := p["price_low"].(float64); ok {
			priceLow = int(v)
		}
		if v, ok := p["price_high"].(float64); ok {
			priceHigh = int(v)
		}

		startTime := int64(0)
		if v, ok := p["start_unix"].(float64); ok {
			startTime = int64(v)
		} else if v, ok := p["start_time"].(float64); ok {
			startTime = int64(v)
		}

		cover := ""
		if v, ok := p["cover"].(string); ok {
			cover = v
		}

		city := ""
		if v, ok := p["city"].(string); ok {
			city = v
		}
		venueName := ""
		if v, ok := p["venue_name"].(string); ok {
			venueName = v
		}
		venue := strings.TrimSpace(strings.Trim(city+" "+venueName, " "))
		if venue == "" {
			venue = "B站官方"
		}

		saleFlag := ""
		if sf, ok := p["sale_flag"].(map[string]any); ok {
			if v, ok := sf["display_name"].(string); ok {
				saleFlag = v
			}
		}
		if saleFlag == "" {
			if v, ok := p["sale_flag"].(string); ok {
				saleFlag = v
			}
		}
		if saleFlag == "" {
			saleFlag = "未知"
		}

		if id == 0 {
			continue
		}

		items = append(items, projectItem{
			ID:        id,
			Name:      name,
			Venue:     venue,
			PriceLow:  priceLow,
			PriceHigh: priceHigh,
			StartTime: startTime,
			Cover:     cover,
			SaleFlag:  saleFlag,
		})
	}
	return items, nil
}

func fetchUpcomingProjectsWeb(ctx context.Context, limit int) ([]projectItem, error) {
	apiURL := fmt.Sprintf("https://show.bilibili.com/api/ticket/project/listV2?filterHt=false&page=1&pagesize=%d&platform=h5&type=2&area=0", limit)
	client := &http.Client{Timeout: 30 * time.Second}
	req, _ := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 14_0 like Mac OS X) AppleWebKit/605.1.15")
	req.Header.Set("Referer", "https://show.bilibili.com/")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Code  int `json:"code"`
		Errno int `json:"errno"`
		Data  struct {
			Result []map[string]any `json:"result"`
		} `json:"data"`
	}

	_ = json.NewDecoder(resp.Body).Decode(&result)
	if result.Code != 0 || result.Errno != 0 {
		return nil, fmt.Errorf("API error: code=%d errno=%d", result.Code, result.Errno)
	}

	items := make([]projectItem, 0, len(result.Data.Result))
	for _, p := range result.Data.Result {
		id := 0
		if v, ok := p["project_id"].(float64); ok {
			id = int(v)
		} else if v, ok := p["id"].(float64); ok {
			id = int(v)
		}

		name := ""
		if v, ok := p["project_name"].(string); ok {
			name = v
		} else if v, ok := p["name"].(string); ok {
			name = v
		}

		priceLow, priceHigh := 0, 0
		if v, ok := p["price_low"].(float64); ok {
			priceLow = int(v)
		}
		if v, ok := p["price_high"].(float64); ok {
			priceHigh = int(v)
		}

		startTime := int64(0)
		if v, ok := p["start_unix"].(float64); ok {
			startTime = int64(v)
		} else if v, ok := p["start_time"].(float64); ok {
			startTime = int64(v)
		}

		saleTime := int64(0)
		if v, ok := p["sale_start_time"].(float64); ok {
			saleTime = int64(v)
		}

		cover := ""
		if v, ok := p["cover"].(string); ok {
			cover = v
		}

		city := ""
		if v, ok := p["city"].(string); ok {
			city = v
		}
		venueName := ""
		if v, ok := p["venue_name"].(string); ok {
			venueName = v
		}
		venue := strings.TrimSpace(strings.Trim(city+" "+venueName, " "))
		if venue == "" {
			venue = "B站官方"
		}

		saleFlag := ""
		if sf, ok := p["sale_flag"].(map[string]any); ok {
			if v, ok := sf["display_name"].(string); ok {
				saleFlag = v
			}
		}
		if saleFlag == "" {
			if v, ok := p["sale_flag"].(string); ok {
				saleFlag = v
			}
		}
		if saleFlag == "" {
			if saleTime > 0 {
				t := time.Unix(saleTime, 0)
				saleFlag = t.Format("01-02 15:04") + " 开售"
			} else {
				saleFlag = "即将开售"
			}
		}

		if id == 0 {
			continue
		}

		items = append(items, projectItem{
			ID:        id,
			Name:      name,
			Venue:     venue,
			PriceLow:  priceLow,
			PriceHigh: priceHigh,
			StartTime: startTime,
			SaleTime:  saleTime,
			Cover:     cover,
			SaleFlag:  saleFlag,
		})
	}
	return items, nil
}

// ============ 项目详情 API ============

type webProj struct {
	ID         int         `json:"id"`
	Name       string      `json:"name"`
	Venue      string      `json:"venue"`
	PriceLow   int         `json:"price_low"`
	PriceHigh  int         `json:"price_high"`
	SaleFlag   string      `json:"sale_flag"`
	StartTime  int64       `json:"start_time"`
	SaleTime   int64       `json:"sale_time"`
	Screens    []webScreen `json:"screens"`
	Cover      string      `json:"cover"`
}

type webScreen struct {
	ID      int         `json:"id"`
	Name    string      `json:"name"`
	Tickets []webTicket `json:"tickets"`
}

type webTicket struct {
	ID       int    `json:"id"`
	Price    int    `json:"price"`
	Desc     string `json:"desc"`
	IsSale   int    `json:"is_sale"`
	SaleFlag string `json:"sale_flag"`
}

func handleAPIProjectDetail(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	path := strings.TrimPrefix(r.URL.Path, "/api/project/")
	id, _ := strconv.Atoi(path)
	if id == 0 {
		json.NewEncoder(w).Encode(map[string]any{"code": 1, "error": "invalid id"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	proj := fetchWebProject(ctx, id)
	if proj == nil {
		json.NewEncoder(w).Encode(map[string]any{"code": 1, "error": "project not found"})
		return
	}

	// 添加到历史
	addToHistory(HistoryItem{
		ProjectID: proj.ID,
		Name:      proj.Name,
		Venue:     proj.Venue,
		PriceLow:  proj.PriceLow,
		SaleFlag:  proj.SaleFlag,
		Cover:     proj.Cover,
	})

	json.NewEncoder(w).Encode(map[string]any{"code": 0, "project": proj})
}

func fetchWebProject(ctx context.Context, id int) *webProj {
	apiURL := fmt.Sprintf("https://show.bilibili.com/api/ticket/project/getV2?id=%d", id)
	client := &http.Client{Timeout: 30 * time.Second}
	req, _ := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Referer", "https://show.bilibili.com/")

	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	var result struct {
		Code int `json:"code"`
		Data struct {
			ID        int    `json:"id"`
			Name      string `json:"name"`
			PriceLow  int    `json:"price_low"`
			PriceHigh int    `json:"price_high"`
			StartTime int64  `json:"start_time"`
			SaleBegin int64  `json:"sale_begin"`
			SaleStart int64  `json:"saleStart"`
			VenueInfo struct {
				Name string `json:"name"`
			} `json:"venue_info"`
			ScreenList []struct {
				ID   int `json:"id"`
				Name string `json:"name"`
				TicketList []struct {
					ID   int    `json:"id"`
					Price int   `json:"price"`
					Desc string `json:"desc"`
					IsSale int  `json:"is_sale"`
					SaleFlag struct {
						DisplayName string `json:"display_name"`
					} `json:"sale_flag"`
				} `json:"ticket_list"`
			} `json:"screen_list"`
			Cover string `json:"cover"`
		} `json:"data"`
	}

	if json.NewDecoder(resp.Body).Decode(&result) != nil || result.Code != 0 {
		return nil
	}

	proj := &webProj{
		ID:        result.Data.ID,
		Name:      result.Data.Name,
		Venue:     result.Data.VenueInfo.Name,
		PriceLow:  result.Data.PriceLow,
		PriceHigh: result.Data.PriceHigh,
		StartTime: result.Data.StartTime,
		Cover:     result.Data.Cover,
	}

	if result.Data.SaleBegin > 0 {
		proj.SaleTime = result.Data.SaleBegin
	} else if result.Data.SaleStart > 0 {
		proj.SaleTime = result.Data.SaleStart
	}

	saleFlag := ""
	for _, s := range result.Data.ScreenList {
		screen := webScreen{ID: s.ID, Name: s.Name}
		for _, t := range s.TicketList {
			screen.Tickets = append(screen.Tickets, webTicket{
				ID:       t.ID,
				Price:    t.Price,
				Desc:     t.Desc,
				IsSale:   t.IsSale,
				SaleFlag: t.SaleFlag.DisplayName,
			})
			if saleFlag == "" && t.SaleFlag.DisplayName != "" {
				saleFlag = t.SaleFlag.DisplayName
			}
		}
		proj.Screens = append(proj.Screens, screen)
	}
	proj.SaleFlag = saleFlag

	return proj
}

// ============ 历史记录 API ============

func handleAPIHistory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == "POST" {
		var item HistoryItem
		if json.NewDecoder(r.Body).Decode(&item) == nil && item.ProjectID > 0 {
			addToHistory(item)
		}
	}
	items, _ := loadHistory()
	sort.Slice(items, func(i, j int) bool { return items[i].AddedAt > items[j].AddedAt })
	json.NewEncoder(w).Encode(map[string]any{"code": 0, "history": items})
}

func handleAPIHistoryAction(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	path := strings.TrimPrefix(r.URL.Path, "/api/history/")
	if path == "clear" {
		clearHistory()
		json.NewEncoder(w).Encode(map[string]any{"code": 0})
		return
	}
	idx, _ := strconv.Atoi(path)
	if idx > 0 {
		deleteHistory(idx)
	}
	items, _ := loadHistory()
	json.NewEncoder(w).Encode(map[string]any{"code": 0, "history": items})
}

// ============ 辅助函数 ============

func openBrowserInBackground(url string) {
	time.Sleep(800 * time.Millisecond)
	var err error
	switch runtime.GOOS {
	case "windows":
		c := exec.Command("cmd", "/c", "start", url)
		c.Stdout = nil
		c.Stderr = nil
		err = c.Start()
	case "darwin":
		err = exec.Command("open", url).Start()
	default:
		err = exec.Command("xdg-open", url).Start()
	}
	_ = err
}

// ============ Web 页面 ============

func buildWebPage() string {
	var sb strings.Builder
	sb.WriteString(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>B站抢票工具</title>
<style>
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;background:#0f0f23;color:#fff;min-height:100vh}
.container{max-width:1100px;margin:0 auto;padding:20px}

/* header */
.header{background:rgba(255,255,255,0.04);border-bottom:1px solid rgba(255,255,255,0.08);padding:14px 0;position:sticky;top:0;z-index:100;backdrop-filter:blur(10px)}
.header .container{display:flex;align-items:center;justify-content:space-between}
.logo{font-size:22px;font-weight:bold;color:#00d4ff;text-decoration:none}
.nav{display:flex;gap:4px}
.nav a{padding:8px 16px;border-radius:8px;color:rgba(255,255,255,0.7);text-decoration:none;transition:all 0.2s;font-size:14px;cursor:pointer}
.nav a:hover,.nav a.active{background:rgba(0,212,255,0.15);color:#00d4ff}
.nav a.exit{color:rgba(231,76,60,0.9)}
.nav a.exit:hover{background:rgba(231,76,60,0.12);color:#e74c3c}
.login-badge{background:rgba(231,76,60,0.2);color:#e74c3c;padding:4px 14px;border-radius:20px;font-size:12px;cursor:pointer;border:none}
.login-badge.logged{background:rgba(46,204,113,0.2);color:#2ecc71}

/* card */
.card{background:rgba(255,255,255,0.04);border:1px solid rgba(255,255,255,0.08);border-radius:16px;padding:24px;margin-bottom:20px}

/* grid */
.project-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(240px,1fr));gap:14px}
.project-card{background:rgba(255,255,255,0.03);border:1px solid rgba(255,255,255,0.08);border-radius:12px;padding:16px;cursor:pointer;transition:all 0.2s}
.project-card:hover{border-color:#00d4ff;transform:translateY(-2px);box-shadow:0 4px 20px rgba(0,212,255,0.1)}
.project-name{font-weight:600;font-size:14px;margin-bottom:8px;line-height:1.35;white-space:normal;word-break:break-word;overflow-wrap:anywhere}
.project-meta{font-size:12px;color:rgba(255,255,255,0.5);margin-bottom:4px}
.tag{display:inline-block;padding:3px 8px;border-radius:4px;font-size:10px}
.tag-sale{background:rgba(46,204,113,0.2);color:#2ecc71}
.tag-upcoming{background:rgba(241,196,15,0.2);color:#f1c40f}
.tag-soldout{background:rgba(231,76,60,0.2);color:#e74c3c}

/* modal */
.modal{position:fixed;top:0;left:0;right:0;bottom:0;background:rgba(0,0,0,0.85);display:none;align-items:center;justify-content:center;z-index:1000;padding:20px}
.modal.show{display:flex}
.modal-content{background:#1a1a2e;border-radius:16px;max-width:680px;width:100%;max-height:90vh;overflow-y:auto;padding:28px;position:relative}
.modal-close{position:absolute;top:12px;right:16px;background:none;border:none;color:rgba(255,255,255,0.5);font-size:28px;cursor:pointer;line-height:1}
.modal-close:hover{color:#fff}
.modal-title{font-size:20px;font-weight:600;margin-bottom:16px;padding-right:36px;white-space:normal;word-break:break-word;overflow-wrap:anywhere}
.modal-meta{display:grid;grid-template-columns:repeat(2,1fr);gap:10px;margin-bottom:16px}
.meta-item{background:rgba(255,255,255,0.05);padding:12px;border-radius:8px}
.meta-label{font-size:11px;color:rgba(255,255,255,0.4);margin-bottom:4px}
.meta-value{font-size:14px;font-weight:500}
.screen-tabs{display:flex;gap:8px;overflow-x:auto;padding-bottom:8px;margin-bottom:12px}
.screen-tab{padding:8px 16px;border-radius:8px;border:1px solid rgba(255,255,255,0.15);background:rgba(255,255,255,0.05);color:rgba(255,255,255,0.7);cursor:pointer;font-size:13px;white-space:nowrap;transition:all 0.2s;flex-shrink:0}
.screen-tab:hover{border-color:#00d4ff;color:#00d4ff}
.screen-tab.active{background:rgba(0,212,255,0.15);border-color:#00d4ff;color:#00d4ff;font-weight:600}
.ticket-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(200px,1fr));gap:10px;margin-bottom:16px}
.ticket-card{padding:14px;border-radius:10px;border:2px solid rgba(255,255,255,0.1);background:rgba(255,255,255,0.03);cursor:pointer;transition:all 0.2s}
.ticket-card:hover{border-color:rgba(0,212,255,0.5);background:rgba(0,212,255,0.05)}
.ticket-card.selected{border-color:#00d4ff;background:rgba(0,212,255,0.12)}
.ticket-card.disabled{opacity:0.4;cursor:not-allowed}
.ticket-card-name{font-size:14px;font-weight:500;margin-bottom:6px;white-space:normal;word-break:break-word;overflow-wrap:anywhere}
.ticket-card-price{font-size:20px;font-weight:700;color:#00d4ff;margin-bottom:4px}
.ticket-card-tag{font-size:11px;padding:2px 8px;border-radius:4px;display:inline-block}
.tag-sale-tag{background:rgba(46,204,113,0.2);color:#2ecc71}
.tag-upcoming-tag{background:rgba(241,196,15,0.2);color:#f1c40f}
.tag-soldout-tag{background:rgba(231,76,60,0.2);color:#e74c3c}
.ticket-card-id{font-size:11px;color:rgba(255,255,255,0.3);margin-top:4px}
.selected-summary{margin-bottom:12px;padding:10px 14px;background:rgba(0,212,255,0.08);border-radius:8px;font-size:13px;color:#00d4ff;border:1px solid rgba(0,212,255,0.2)}
.history-item{display:flex;align-items:center;justify-content:space-between;padding:12px 0;border-bottom:1px solid rgba(255,255,255,0.06)}
.history-item:last-child{border-bottom:none}
.history-name{font-weight:500;margin-bottom:4px;white-space:normal;word-break:break-word;overflow-wrap:anywhere}
.history-meta{font-size:12px;color:rgba(255,255,255,0.4)}
.history-expanded{padding:12px 0 8px;display:none}
.history-expanded.show{display:block}
.history-sel{display:flex;gap:8px;margin-bottom:8px;flex-wrap:wrap}
.history-sel label{font-size:12px;color:rgba(255,255,255,0.5);display:flex;align-items:center;gap:4px;flex:1;min-width:120px}
.history-sel select{flex:1;min-width:100px;padding:6px 8px;border:1px solid rgba(255,255,255,0.15);border-radius:6px;background:rgba(255,255,255,0.06);color:#fff;font-size:12px;outline:none}
select{background:rgba(255,255,255,0.06);color:#fff}
select option{background:#1a1a2e;color:#fff}
select,select option{background-color:#1a1a2e;color:#fff}


/* btn */
.btn{padding:10px 22px;border:none;border-radius:8px;cursor:pointer;font-size:14px;font-weight:500;transition:all 0.2s;display:inline-flex;align-items:center;gap:6px}
.btn:active{transform:scale(0.97)}
.btn-primary{background:#00d4ff;color:#000}
.btn-primary:hover{background:#00b8e6}
.btn-success{background:#2ecc71;color:#fff}
.btn-success:hover{background:#27ae60}
.btn-danger{background:#e74c3c;color:#fff}
.btn-danger:hover{background:#c0392b}
.btn-outline{background:transparent;border:1px solid rgba(255,255,255,0.2);color:#fff}
.btn-outline:hover{background:rgba(255,255,255,0.08)}
.btn-small{padding:6px 14px;font-size:12px}
.btn:disabled{opacity:0.5;cursor:not-allowed}
.btn-row{display:flex;gap:8px;flex-wrap:wrap;margin-top:16px}

/* login */
.login-center{display:flex;flex-direction:column;align-items:center;padding:40px 20px}
.qr-box{background:#fff;border-radius:16px;padding:20px;margin:20px 0;display:inline-block}
.qr-box img{display:block;width:200px;height:200px}
.login-status{margin-top:16px;font-size:15px;text-align:center}
.login-status.waiting{color:rgba(255,255,255,0.5)}
.login-status.scanned{color:#f39c12}
.login-status.success{color:#2ecc71}
.login-status.error{color:#e74c3c}
.login-steps{text-align:left;margin-top:24px;background:rgba(255,255,255,0.04);border-radius:12px;padding:20px;font-size:13px;color:rgba(255,255,255,0.6);line-height:2}
.login-steps div{display:flex;align-items:center;gap:10px}
.step-num{width:22px;height:22px;background:rgba(255,255,255,0.08);border-radius:50%;display:flex;align-items:center;justify-content:center;font-size:11px;flex-shrink:0}

/* buy */
.buy-form{display:grid;grid-template-columns:repeat(3,1fr);gap:10px;margin-bottom:12px}
.buy-form input{padding:10px 14px;border:1px solid rgba(255,255,255,0.15);border-radius:8px;background:rgba(255,255,255,0.05);color:#fff;font-size:14px;outline:none}
.buy-form input:focus{border-color:#00d4ff}
.buy-options{display:flex;gap:10px;margin-bottom:12px;flex-wrap:wrap;align-items:center}
.buy-options label{font-size:13px;color:rgba(255,255,255,0.6);display:flex;align-items:center;gap:6px}
.buy-options input[type="checkbox"]{width:16px;height:16px}
.buyer-toolbar{display:flex;gap:10px;align-items:center;flex-wrap:wrap;margin:6px 0 10px}
.buyer-tip{color:rgba(255,255,255,0.4);font-size:12px}
.buyer-list{display:flex;flex-wrap:wrap;gap:8px;margin-bottom:12px}
.buyer-item{display:flex;align-items:center;gap:8px;padding:8px 10px;border:1px solid rgba(255,255,255,0.12);border-radius:10px;background:rgba(255,255,255,0.03);cursor:pointer;user-select:none}
.buyer-item:hover{border-color:rgba(0,212,255,0.5)}
.buyer-item input{width:16px;height:16px}
.buyer-name{font-size:13px;color:rgba(255,255,255,0.85)}
.buyer-meta{font-size:12px;color:rgba(255,255,255,0.45)}
.contact-form{display:grid;grid-template-columns:repeat(2,1fr);gap:10px;margin-bottom:12px}
.contact-form input{padding:10px 14px;border:1px solid rgba(255,255,255,0.15);border-radius:8px;background:rgba(255,255,255,0.05);color:#fff;font-size:14px;outline:none}
.contact-form input:focus{border-color:#00d4ff}
.buy-console{background:#0a0a1a;border:1px solid rgba(255,255,255,0.08);border-radius:12px;padding:16px;font-family:"Consolas","Monaco",monospace;font-size:13px;max-height:400px;overflow-y:auto;margin-top:12px}
.console-line{margin-bottom:4px;display:flex;gap:8px}
.console-time{color:rgba(255,255,255,0.3);flex-shrink:0}
.console-info{color:rgba(255,255,255,0.8)}
.console-success{color:#2ecc71;font-weight:600}
.console-error{color:#e74c3c}
.console-result{background:rgba(46,204,113,0.1);border:1px solid rgba(46,204,113,0.3);border-radius:8px;padding:12px;margin-top:8px;font-size:14px}

/* toast */
.toast{position:fixed;bottom:24px;right:24px;padding:14px 22px;border-radius:10px;font-size:14px;z-index:3000;transform:translateY(100px);opacity:0;transition:all 0.3s;pointer-events:none}
.toast.show{transform:translateY(0);opacity:1}
.toast-success{background:#2ecc71}
.toast-error{background:#e74c3c}
.toast-info{background:#3498db}

.empty{text-align:center;padding:60px 20px;color:rgba(255,255,255,0.35)}
.loading{text-align:center;padding:40px;color:rgba(255,255,255,0.4)}

.search-row{display:flex;gap:8px;margin-bottom:16px;flex-wrap:wrap}
.search-row input{flex:1;min-width:180px;padding:10px 14px;border:1px solid rgba(255,255,255,0.15);border-radius:8px;background:rgba(255,255,255,0.05);color:#fff;font-size:14px;outline:none}
.search-row input:focus{border-color:#00d4ff}
.search-row input::placeholder{color:rgba(255,255,255,0.3)}

.section-title{font-size:15px;font-weight:600;margin:16px 0 10px}
</style>
</head>
<body>
<div class="header">
  <div class="container">
    <div class="logo">B站抢票工具</div>
    <nav class="nav">
      <a href="#" id="nav-discover" onclick="showPage('discover');return false" class="active">发现演出</a>
      <a href="#" id="nav-login" onclick="showPage('login');return false">扫码登录</a>
      <a href="#" id="nav-buy" onclick="showPage('buy');return false">开始抢票</a>
      <a href="#" id="nav-history" onclick="showPage('history');return false">我的收藏</a>
      <a href="#" id="nav-exit" onclick="exitApp();return false" class="exit">退出程序</a>
    </nav>
    <button class="login-badge" id="loginBadge" onclick="showPage('login')">未登录</button>
  </div>
</div>

<div class="container">
  <!-- 发现演出 -->
  <div id="page-discover">
    <div class="card">
      <div class="search-row">
        <input type="text" id="searchInput" placeholder="输入项目ID后回车搜索..." onkeypress="if(event.key==='Enter')searchById()">
        <button class="btn btn-primary" onclick="loadHot()">热门演出</button>
        <button class="btn btn-outline" onclick="loadUpcoming()">即将开售</button>
      </div>
      <div id="projectList"><div class="empty">点击上方按钮开始发现演出</div></div>
    </div>
  </div>

  <!-- 扫码登录 -->
  <div id="page-login" style="display:none">
    <div class="card">
      <div class="login-center" id="loginCenter">
        <div style="text-align:center">
          <h2 style="font-size:20px;margin-bottom:4px">B站账号登录</h2>
          <p style="color:rgba(255,255,255,0.4);font-size:13px">扫码登录以获取抢票凭证</p>
        </div>
        <div class="qr-box" id="qrBox">
          <img src="" alt="二维码" id="qrImg">
        </div>
        <div class="login-status waiting" id="loginStatus">正在加载二维码...</div>
        <button class="btn btn-outline btn-small" onclick="refreshQR()" style="margin-top:12px">刷新二维码</button>
        <div style="margin-top:12px;width:100%;max-width:520px">
          <label style="font-size:12px;color:rgba(255,255,255,0.6);display:flex;align-items:center;gap:8px">
            <input type="checkbox" id="debugToggle" onchange="toggleDebug()" style="width:16px;height:16px">
            调试模式（不会显示完整Cookie）
          </label>
          <div id="debugPanel" style="display:none;margin-top:10px">
            <div style="display:flex;gap:8px;flex-wrap:wrap">
              <button class="btn btn-outline btn-small" onclick="copyDebug()">复制调试信息</button>
              <button class="btn btn-outline btn-small" onclick="clearDebug()">清空</button>
            </div>
            <pre id="debugBox" style="margin-top:10px;white-space:pre-wrap;word-break:break-word;background:#0a0a1a;border:1px solid rgba(255,255,255,0.08);border-radius:10px;padding:12px;color:rgba(255,255,255,0.75);font-family:Consolas,Monaco,monospace;font-size:12px;max-height:240px;overflow:auto"></pre>
          </div>
        </div>
        <div class="login-steps">
          <div class="step-num">1</div><span>打开手机B站客户端</span>
          <div style="margin:8px 0"></div>
          <div class="step-num">2</div><span>点击右上角扫一扫</span>
          <div style="margin:8px 0"></div>
          <div class="step-num">3</div><span>扫描左侧二维码并在手机确认登录</span>
        </div>
      </div>
      <div id="loginDoneArea" style="display:none;text-align:center;padding:40px 0">
        <div style="font-size:48px;margin-bottom:16px">&#x1F389;</div>
        <h2 style="font-size:22px;margin-bottom:8px">登录成功！</h2>
        <p style="color:rgba(255,255,255,0.5);margin-bottom:24px" id="loginUser"></p>
        <button class="btn btn-primary" onclick="showPage('buy')">去抢票</button>
      </div>
    </div>
  </div>

  <!-- 开始抢票 -->
  <div id="page-buy" style="display:none">
    <div class="card">
      <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:16px">
        <div style="font-size:16px;font-weight:600">快速抢票</div>
        <button class="btn btn-danger btn-small" id="btnStop" onclick="stopBuy()" style="display:none">停止抢票</button>
      </div>
      <div class="buy-form">
        <input type="text" id="buyAreaId" placeholder="项目ID (area-id)">
        <input type="text" id="buyScheduleId" placeholder="场次ID (schedule-id)">
        <input type="text" id="buyItemId" placeholder="票档ID (item-id)">
      </div>
      <div class="buy-options">
        <label><input type="number" id="buyCount" value="1" min="1" max="10" style="width:70px;padding:6px 10px;border:1px solid rgba(255,255,255,0.15);border-radius:6px;background:rgba(255,255,255,0.05);color:#fff;font-size:13px;outline:none"> 数量</label>
        <label><input type="number" id="buyInterval" value="500" min="100" max="5000" step="100" style="width:80px;padding:6px 10px;border:1px solid rgba(255,255,255,0.15);border-radius:6px;background:rgba(255,255,255,0.05);color:#fff;font-size:13px;outline:none"> ms间隔</label>
        <label><input type="checkbox" id="buyHot"> 热门项目</label>
        <label><input type="checkbox" id="buyMobile"> 手机端</label>
      </div>
      <div class="section-title">购票人（实名信息）</div>
      <div class="buyer-toolbar">
        <button class="btn btn-outline btn-small" onclick="loadBuyers()">刷新购票人</button>
        <div class="buyer-tip">多人购票建议先在B站「购票人」里预先添加身份证信息</div>
      </div>
      <div id="buyerList" class="buyer-list"><div class="empty" style="padding:20px 0">未加载购票人（请先登录后刷新）</div></div>
      <div class="section-title">联系人（部分项目必填）</div>
      <div class="contact-form">
        <input type="text" id="contactName" placeholder="联系人姓名（部分项目必填）">
        <input type="text" id="contactTel" placeholder="联系人手机号（11位，部分项目必填）">
      </div>
      <button class="btn btn-success" id="btnStart" onclick="startBuy()">&#x1F3C6; 开始抢票</button>
      <div class="buy-console" id="buyConsole">
        <div style="color:rgba(255,255,255,0.3);text-align:center;padding:20px">点击"开始抢票"启动任务，日志将实时显示在这里</div>
      </div>
      <div id="buyResult" style="display:none"></div>
    </div>
  </div>

  <!-- 我的收藏 -->
  <div id="page-history" style="display:none">
    <div class="card">
      <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:16px">
        <div style="font-size:16px;font-weight:600">我的收藏</div>
        <button class="btn btn-danger btn-small" onclick="clearHistory()">清空全部</button>
      </div>
      <div id="historyList"><div class="empty">暂无收藏记录</div></div>
    </div>
  </div>
</div>

<!-- 项目详情弹窗 -->
<div class="modal" id="detailModal">
  <div class="modal-content">
    <button class="modal-close" onclick="closeModal()">x</button>
    <div class="modal-title" id="modalTitle"></div>
    <div class="modal-meta" id="modalMeta"></div>

    <div class="section-title">选择场次</div>
    <div class="screen-tabs" id="screenTabs"></div>

    <div class="section-title">选择票档</div>
    <div class="ticket-grid" id="ticketGrid"></div>
    <div class="selected-summary" id="selectedSummary">请选择场次和票档</div>

    <div class="btn-row">
      <button class="btn btn-success" id="btnBuySelected" onclick="buySelected()">立即抢票</button>
      <button class="btn btn-outline" onclick="favFromModal()">收藏</button>
    </div>
  </div>
</div>

<div class="toast" id="toast"></div>

<script>
var currentProject=null;
var buyEventSource=null;
var pollTimer=null;
var debugMode=false;
var debugLines=[];
var buyers=[];
var selectedBuyerIds=[];
var buyerTouched=false;

document.addEventListener('DOMContentLoaded',function(){
  checkLogin();
  loadHot();
  // 联系人信息：本地记忆（避免每次手填）
  try{
    var n=localStorage.getItem('gobiliticket_contact_name')||'';
    var t=localStorage.getItem('gobiliticket_contact_tel')||'';
    if(document.getElementById('contactName')&&n) document.getElementById('contactName').value=n;
    if(document.getElementById('contactTel')&&t) document.getElementById('contactTel').value=t;
    if(document.getElementById('contactName')) document.getElementById('contactName').addEventListener('input',function(){localStorage.setItem('gobiliticket_contact_name',this.value||'');});
    if(document.getElementById('contactTel')) document.getElementById('contactTel').addEventListener('input',function(){localStorage.setItem('gobiliticket_contact_tel',this.value||'');});
  }catch(e){}
  var countEl=document.getElementById('buyCount');
  if(countEl){
    countEl.addEventListener('change',function(){
      if(!buyerTouched){
        autoSelectBuyers();
        renderBuyerList();
      }
    });
  }
});

// ========== 登录 ==========
function checkLogin(){
  fetch('/api/login/status').then(function(r){return r.json()}).then(function(d){
    if(d.loggedin){
      document.getElementById('loginBadge').textContent=d.user;
      document.getElementById('loginBadge').className='login-badge logged';
      document.getElementById('loginCenter').style.display='none';
      document.getElementById('loginDoneArea').style.display='block';
      document.getElementById('loginUser').textContent='凭证已保存: '+d.user;
    } else {
      refreshQR();
    }
  });
}

function refreshQR(){
  document.getElementById('loginStatus').textContent='正在加载二维码...';
  document.getElementById('loginStatus').className='login-status waiting';
  document.getElementById('loginCenter').style.display='flex';
  document.getElementById('loginDoneArea').style.display='none';
  fetch('/api/login/qr'+(debugMode?'?debug=1':'')).then(function(r){return r.json()}).then(function(d){
    debugLog({at:'login/qr',resp:d});
    if(d.code===0){
      document.getElementById('qrImg').src=d.qr_url;
      document.getElementById('loginStatus').textContent='请使用B站APP扫码';
      document.getElementById('loginStatus').className='login-status waiting';
      if(pollTimer) clearInterval(pollTimer);
      pollTimer=setInterval(function(){pollLogin(d.oauth_key);},2000);
    } else {
      document.getElementById('loginStatus').textContent='加载失败: '+d.error;
      document.getElementById('loginStatus').className='login-status error';
    }
  });
}

function pollLogin(key){
  fetch('/api/login/poll?key='+encodeURIComponent(key)+(debugMode?'&debug=1':'')).then(function(r){return r.json()}).then(function(d){
    debugLog({at:'login/poll',resp:d});
    if(d.status===200){
      clearInterval(pollTimer);
      document.getElementById('loginStatus').textContent='登录成功！';
      document.getElementById('loginStatus').className='login-status success';
      document.getElementById('loginBadge').textContent='已登录';
      document.getElementById('loginBadge').className='login-badge logged';
      setTimeout(function(){
        document.getElementById('loginCenter').style.display='none';
        document.getElementById('loginDoneArea').style.display='block';
        document.getElementById('loginUser').textContent='凭证已保存';
        toast('登录成功，可以开始抢票了！','success');
      },800);
    } else if(d.status===0){
      document.getElementById('loginStatus').textContent='已扫码，请在手机端确认登录...';
      document.getElementById('loginStatus').className='login-status scanned';
    } else if(d.status===4||d.status===5){
      clearInterval(pollTimer);
      document.getElementById('loginStatus').textContent=d.msg;
      document.getElementById('loginStatus').className='login-status error';
    }
  });
}

function toggleDebug(){
  debugMode=!!document.getElementById('debugToggle').checked;
  document.getElementById('debugPanel').style.display=debugMode?'block':'none';
  if(debugMode && debugLines.length===0){
    debugLog({at:'debug',msg:'调试模式已开启'});
  }
}
function debugLog(obj){
  if(!debugMode)return;
  try{
    debugLines.push(JSON.stringify(obj,null,2));
  }catch(e){
    debugLines.push(String(obj));
  }
  if(debugLines.length>30)debugLines=debugLines.slice(debugLines.length-30);
  document.getElementById('debugBox').textContent=debugLines.join('\\n\\n');
}
function clearDebug(){
  debugLines=[];
  var box=document.getElementById('debugBox');
  if(box)box.textContent='';
}
function copyDebug(){
  var txt=debugLines.join('\\n\\n');
  if(!txt){toast('没有调试信息','info');return;}
  if(navigator.clipboard&&navigator.clipboard.writeText){
    navigator.clipboard.writeText(txt).then(function(){toast('已复制','success');}).catch(function(){toast('复制失败','error');});
    return;
  }
  // fallback
  var ta=document.createElement('textarea');
  ta.value=txt;
  document.body.appendChild(ta);
  ta.select();
  try{document.execCommand('copy');toast('已复制','success');}catch(e){toast('复制失败','error');}
  document.body.removeChild(ta);
}

// ========== 页面切换 ==========
function showPage(p){
  document.querySelectorAll('.nav a').forEach(function(a){a.classList.remove('active')});
  var navMap={discover:'nav-discover',login:'nav-login',buy:'nav-buy',history:'nav-history'};
  if(navMap[p]) document.getElementById(navMap[p]).classList.add('active');
  document.querySelectorAll('[id^="page-"]').forEach(function(el){el.style.display='none'});
  document.getElementById('page-'+p).style.display='block';
  if(p==='history') loadHistory();
  if(p==='buy') loadBuyers();
}

// ========== 演出发现 ==========
function loadHot(){
  document.getElementById('projectList').innerHTML='<div class="loading">加载热门演出...</div>';
  fetch('/api/projects?type=hot&limit=12').then(function(r){return r.json()}).then(function(d){
    renderProjects(d.projects||[]);
  });
}
function loadUpcoming(){
  document.getElementById('projectList').innerHTML='<div class="loading">加载即将开售...</div>';
  fetch('/api/projects?type=upcoming&limit=12').then(function(r){return r.json()}).then(function(d){
    renderProjects(d.projects||[]);
  });
}
function searchById(){
  var id=document.getElementById('searchInput').value.trim();
  if(!id)return;
  var match=id.match(/(\d+)/);
  if(match){
    document.getElementById('projectList').innerHTML='<div class="loading">加载项目...</div>';
    fetch('/api/project/'+match[1]).then(function(r){return r.json()}).then(function(d){
      if(d.project){
        currentProject=d.project;
        showModal(d.project);
        document.getElementById('page-discover').style.display='block';
      } else {
        document.getElementById('projectList').innerHTML='<div class="empty">未找到项目</div>';
      }
    });
  }
}
function renderProjects(list){
  var el=document.getElementById('projectList');
  if(!list.length){el.innerHTML='<div class="empty">没有找到相关演出</div>';return}
  var html='<div class="project-grid">';
  for(var i=0;i<list.length;i++){
    var p=list[i];
    var tag='';
    if(p.sale_flag){
      if(p.sale_flag.indexOf('售卖')>=0||p.sale_flag.indexOf('热卖')>=0)tag='<span class="tag tag-sale">'+esc(p.sale_flag)+'</span>';
      else if(p.sale_flag.indexOf('待开')>=0||p.sale_flag.indexOf('预售')>=0||p.sale_flag.indexOf('即将')>=0)tag='<span class="tag tag-upcoming">'+esc(p.sale_flag)+'</span>';
      else tag='<span class="tag tag-soldout">'+esc(p.sale_flag)+'</span>';
    }
    var price='待定';
    if(p.price_low>0){
      price='¥'+(p.price_low/100).toFixed(0);
      if(p.price_high>p.price_low)price+=' - ¥'+(p.price_high/100).toFixed(0);
    }
    html+='<div class="project-card" onclick="showProject('+p.id+')">';
    html+='<div class="project-name">'+esc(p.name)+'</div>';
    html+='<div class="project-meta">'+esc(p.venue||'未知场馆')+'</div>';
    html+='<div class="project-meta">'+price+'</div>';
    if(p.start_time>0)html+='<div class="project-meta">'+fmtTime(p.start_time)+'</div>';
    if(tag)html+=tag;
    html+='</div>';
  }
  html+='</div>';
  el.innerHTML=html;
}
function showProject(id){
  fetch('/api/project/'+id).then(function(r){return r.json()}).then(function(d){
    if(d.project){currentProject=d.project;showModal(d.project);}
  });
}

// ========== 详情弹窗 ==========
var selScreenIdx=-1;
var selTicketIdx=-1;
function showModal(p){
  currentProject=p;
  selScreenIdx=-1;
  selTicketIdx=-1;
  document.getElementById('modalTitle').textContent=p.name||'项目详情';
  var meta=document.getElementById('modalMeta');
  meta.innerHTML='<div class="meta-item"><div class="meta-label">项目ID</div><div class="meta-value">'+p.id+'</div></div>';
  meta.innerHTML+='<div class="meta-item"><div class="meta-label">场馆</div><div class="meta-value">'+esc(p.venue||'未知')+'</div></div>';
  var price='待定';
  if(p.price_low>0){
    price='¥'+(p.price_low/100).toFixed(0);
    if(p.price_high>p.price_low)price+=' - ¥'+(p.price_high/100).toFixed(0);
  }
  meta.innerHTML+='<div class="meta-item"><div class="meta-label">价格区间</div><div class="meta-value">'+price+'</div></div>';
  meta.innerHTML+='<div class="meta-item"><div class="meta-label">状态</div><div class="meta-value">'+esc(p.sale_flag||'未知')+'</div></div>';
  if(p.start_time>0){
    meta.innerHTML+='<div class="meta-item"><div class="meta-label">开始时间</div><div class="meta-value">'+fmtTime(p.start_time)+'</div></div>';
  }
  if(p.sale_time>0){
    meta.innerHTML+='<div class="meta-item"><div class="meta-label">开售时间</div><div class="meta-value">'+fmtTime(p.sale_time)+'</div></div>';
  }

  // 渲染场次 tabs
  var tabsEl=document.getElementById('screenTabs');
  var screens=p.screens||[];
  if(screens.length===0){
    tabsEl.innerHTML='<div style="color:rgba(255,255,255,0.4);font-size:13px">暂无可选场次</div>';
  } else {
    var tabsHtml='';
    for(var si=0;si<screens.length;si++){
      var s=screens[si];
      var activeCls=si===0?' active':'';
      tabsHtml+='<div class="screen-tab'+activeCls+'" onclick="selectScreen('+si+')" id="screenTab'+si+'">'+esc(s.name||'场次'+(si+1))+' (ID:'+s.id+')</div>';
    }
    tabsEl.innerHTML=tabsHtml;
  }

  // 默认选中第一场，渲染其票档
  if(screens.length>0){
    selectScreen(0);
  } else {
    document.getElementById('ticketGrid').innerHTML='<div class="empty" style="padding:20px">暂无票档信息</div>';
    updateSelectedSummary();
  }

  document.getElementById('detailModal').classList.add('show');
}
function selectScreen(idx){
  selScreenIdx=idx;
  selTicketIdx=-1;
  var screens=currentProject.screens||[];
  document.querySelectorAll('.screen-tab').forEach(function(el,i){el.classList.toggle('active',i===idx);});
  var gridEl=document.getElementById('ticketGrid');
  if(idx<0||idx>=screens.length){
    gridEl.innerHTML='<div class="empty" style="padding:20px">请选择场次</div>';
    updateSelectedSummary();
    return;
  }
  var s=screens[idx];
  var tickets=s.tickets||[];
  if(tickets.length===0){
    gridEl.innerHTML='<div class="empty" style="padding:20px">该场次暂无票档</div>';
    updateSelectedSummary();
    return;
  }
  var html='';
  for(var ti=0;ti<tickets.length;ti++){
    var t=tickets[ti];
    var sf=(t.sale_flag||'');
    // 可售判断：优先按文案，其次按 is_sale（不同项目含义可能略有差异）
    var isDisabled=(t.is_sale===3||t.is_sale===4);
    if(sf.indexOf('不可售')>=0||sf.indexOf('售罄')>=0||sf.indexOf('缺货')>=0||sf.indexOf('停售')>=0) isDisabled=true;
    if(t.is_sale===2) isDisabled=true;

    var tagCls='tag-upcoming-tag';
    if(sf){
      if(sf.indexOf('售卖')>=0||sf.indexOf('热卖')>=0||sf.indexOf('预售')>=0) tagCls='tag-sale-tag';
      else if(sf.indexOf('待开')>=0||sf.indexOf('即将')>=0) tagCls='tag-upcoming-tag';
      else if(sf.indexOf('不可售')>=0||sf.indexOf('售罄')>=0) tagCls='tag-soldout-tag';
      else tagCls='tag-upcoming-tag';
    }
    var disabledCls=isDisabled?' disabled':'';
    html+='<div class="ticket-card'+disabledCls+'" id="ticketCard'+ti+'" onclick="'+(isDisabled?'':'selectTicket('+ti+')')+'">';
    html+='<div class="ticket-card-name">'+esc(t.desc||'票档'+(ti+1))+'</div>';
    html+='<div class="ticket-card-price">¥'+(t.price/100).toFixed(0)+'</div>';
    html+='<span class="ticket-card-tag '+tagCls+'">'+esc(t.sale_flag||'未知')+'</span>';
    html+='<div class="ticket-card-id">ID: '+t.id+'</div>';
    html+='</div>';
  }
  gridEl.innerHTML=html;
  updateSelectedSummary();
}
function selectTicket(idx){
  selTicketIdx=idx;
  document.querySelectorAll('.ticket-card').forEach(function(el,i){el.classList.toggle('selected',i===idx);});
  updateSelectedSummary();
}
function updateSelectedSummary(){
  var el=document.getElementById('selectedSummary');
  var screens=currentProject.screens||[];
  if(selScreenIdx<0||selScreenIdx>=screens.length){
    el.textContent='请选择场次';
    return;
  }
  var s=screens[selScreenIdx];
  var tickets=s.tickets||[];
  if(selTicketIdx<0||selTicketIdx>=tickets.length){
    el.textContent='已选场次: '+esc(s.name)+'，请选择票档';
    return;
  }
  var t=tickets[selTicketIdx];
  el.textContent='已选: '+esc(s.name)+' | '+esc(t.desc)+' ¥'+(t.price/100).toFixed(0)+' (screen:'+s.id+' item:'+t.id+')';
}
function buySelected(){
  var screens=currentProject.screens||[];
  if(selScreenIdx<0||selScreenIdx>=screens.length){
    toast('请选择场次','error');return;
  }
  var tickets=screens[selScreenIdx].tickets||[];
  if(selTicketIdx<0||selTicketIdx>=tickets.length){
    toast('请选择票档','error');return;
  }
  var s=screens[selScreenIdx];
  var t=tickets[selTicketIdx];
  document.getElementById('buyAreaId').value=currentProject.id;
  document.getElementById('buyScheduleId').value=s.id;
  document.getElementById('buyItemId').value=t.id;
  closeModal();
  showPage('buy');
  toast('已选择 '+esc(t.desc)+'，点击开始抢票','success');
}
function closeModal(){
  document.getElementById('detailModal').classList.remove('show');
}
function favFromModal(){
  if(!currentProject)return;
  fetch('/api/history',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({project_id:currentProject.id,name:currentProject.name,venue:currentProject.venue,price_low:currentProject.price_low})}).then(function(){
    toast('已收藏','success');
  });
  closeModal();
}

// ========== 购票人（实名） ==========
function loadBuyers(){
  var el=document.getElementById('buyerList');
  if(!el)return;
  el.innerHTML='<div class="loading" style="padding:20px 0">加载购票人...</div>';
  fetch('/api/buyers').then(function(r){return r.json()}).then(function(d){
    if(d.code===2){
      buyers=[];
      selectedBuyerIds=[];
      buyerTouched=false;
      el.innerHTML='<div class="empty" style="padding:20px 0">请先登录后再加载购票人</div>';
      return;
    }
    buyers=d.buyers||[];
    renderBuyerList();
  }).catch(function(){
    buyers=[];
    el.innerHTML='<div class="empty" style="padding:20px 0">加载购票人失败</div>';
  });
}

function autoSelectBuyers(){
  if(!buyers||!buyers.length)return;
  var count=parseInt(document.getElementById('buyCount').value)||1;
  selectedBuyerIds=[];

  var def=null;
  for(var i=0;i<buyers.length;i++){
    if(buyers[i]&&buyers[i].is_default){def=buyers[i];break;}
  }
  if(def&&def.id) selectedBuyerIds.push(def.id);

  for(var j=0;j<buyers.length;j++){
    if(selectedBuyerIds.length>=count) break;
    var id=buyers[j].id;
    if(!id) continue;
    if(selectedBuyerIds.indexOf(id)>=0) continue;
    selectedBuyerIds.push(id);
  }

  if(count<=1&&selectedBuyerIds.length>1){
    selectedBuyerIds=[selectedBuyerIds[0]];
  }
}

function renderBuyerList(){
  var el=document.getElementById('buyerList');
  if(!el)return;
  if(!buyers||buyers.length===0){
    el.innerHTML='<div class="empty" style="padding:20px 0">没有购票人（请先在B站添加实名购票人）</div>';
    return;
  }

  if(!buyerTouched&&(selectedBuyerIds.length===0)){
    autoSelectBuyers();
  }

  var html='';
  for(var i=0;i<buyers.length;i++){
    var b=buyers[i]||{};
    var id=parseInt(b.id)||0;
    if(!id) continue;
    var checked=selectedBuyerIds.indexOf(id)>=0;
    var meta='ID:'+id;
    if(b.tel_masked) meta+=' | '+esc(b.tel_masked);
    if(b.is_default) meta+=' | 默认';
    html+='<label class="buyer-item">'
      +'<input type="checkbox" data-id="'+id+'" '+(checked?'checked':'')+' onchange="onBuyerToggle(this)">'
      +'<div>'
      +'<div class="buyer-name">'+esc(b.name||'')+'</div>'
      +'<div class="buyer-meta">'+meta+'</div>'
      +'</div>'
      +'</label>';
  }
  el.innerHTML=html||'<div class="empty" style="padding:20px 0">没有可用购票人</div>';
}

function onBuyerToggle(cb){
  buyerTouched=true;
  var id=parseInt(cb.getAttribute('data-id'))||0;
  if(!id) return;
  if(cb.checked){
    if(selectedBuyerIds.indexOf(id)<0) selectedBuyerIds.push(id);
  } else {
    selectedBuyerIds=selectedBuyerIds.filter(function(x){return x!==id;});
  }
}

// ========== 抢票 ==========
function startBuy(){
  var areaId=document.getElementById('buyAreaId').value.trim();
  var scheduleId=document.getElementById('buyScheduleId').value.trim();
  var itemId=document.getElementById('buyItemId').value.trim();
  if(!areaId||!scheduleId||!itemId){toast('请填写完整的项目ID、场次ID和票档ID','error');return;}

  var count=parseInt(document.getElementById('buyCount').value)||1;
  var interval=parseInt(document.getElementById('buyInterval').value)||500;
  var isHot=document.getElementById('buyHot').checked;
  var isMobile=document.getElementById('buyMobile').checked;
  var buyerIds=(selectedBuyerIds||[]).slice(0);
  var contactName=(document.getElementById('contactName')?document.getElementById('contactName').value:'').trim();
  var contactTelRaw=(document.getElementById('contactTel')?document.getElementById('contactTel').value:'').trim();
  var contactTel=contactTelRaw.replace(/\\D/g,'');
  if(contactTel && contactTel.length!==11){
    toast('联系人手机号需为11位数字','error');return;
  }
  if(buyerIds.length>0&&buyerIds.length!==count){
    toast('提示：数量='+count+'，但已选购票人='+buyerIds.length+'（可能下单失败）','info');
  }

  document.getElementById('btnStart').disabled=true;
  document.getElementById('btnStop').style.display='inline-flex';
  document.getElementById('buyConsole').innerHTML='';
  document.getElementById('buyResult').style.display='none';

  fetch('/api/buy/start',{
    method:'POST',
    headers:{'Content-Type':'application/json'},
    body:JSON.stringify({project_id:areaId,schedule_id:scheduleId,item_id:itemId,count:count,interval_ms:interval,is_hot:isHot,is_mobile:isMobile,buyer_ids:buyerIds,contact_name:contactName,contact_tel:contactTel})
  }).then(function(r){return r.json()}).then(function(d){
    if(d.code===2){toast('请先登录','error');document.getElementById('btnStart').disabled=false;document.getElementById('btnStop').style.display='none';return;}
    if(d.code!==0){toast(d.error||'启动失败','error');document.getElementById('btnStart').disabled=false;document.getElementById('btnStop').style.display='none';return;}
    startBuyStream();
  }).catch(function(e){
    toast('请求失败','error');
    document.getElementById('btnStart').disabled=false;
    document.getElementById('btnStop').style.display='none';
  });
}

function startBuyStream(){
  if(buyEventSource)buyEventSource.close();
  buyEventSource=new EventSource('/api/buy/stream');
  buyEventSource.onmessage=function(e){
    try {
      var d=JSON.parse(e.data);
      if(d.type==='done'){
        buyEventSource.close();
        checkBuyResult();
        return;
      }
      appendLog(d);
    } catch(err){
      checkBuyResult();
    }
  };
  buyEventSource.onerror=function(){
    buyEventSource.close();
    checkBuyResult();
  };
}

function appendLog(log){
  // 兼容 Go 后端的 json tag (time/type/message) 与前端自造的 (Time/Type/Message)
  if(log && (!log.Time && log.time)) log.Time=log.time;
  if(log && (!log.Type && log.type)) log.Type=log.type;
  if(log && (!log.Message && log.message)) log.Message=log.message;
  if(log && (!log.Time)) log.Time=timeNow();
  if(log && (!log.Type)) log.Type='info';
  if(log && (!log.Message)) log.Message='';
  var el=document.getElementById('buyConsole');
  var cls=log.Type==='success'?'console-success':(log.Type==='error'?'console-error':'console-info');
  var div=document.createElement('div');
  div.className='console-line';
  div.innerHTML='<span class="console-time">'+esc(log.Time)+'</span><span class="'+cls+'">'+esc(log.Message)+'</span>';
  el.appendChild(div);
  el.scrollTop=el.scrollHeight;
}

function checkBuyResult(){
  fetch('/api/buy/status').then(function(r){return r.json()}).then(function(d){
    document.getElementById('btnStart').disabled=false;
    document.getElementById('btnStop').style.display='none';
    if(d.success){
      var resultEl=document.getElementById('buyResult');
      resultEl.style.display='block';
      resultEl.innerHTML='<div class="console-result"><div style="font-size:20px;margin-bottom:8px">&#x1F389; 抢票成功！</div><div>订单号: <strong>'+d.order_id+'</strong></div><div style="margin-top:8px;font-size:13px;color:rgba(255,255,255,0.5)">请在30分钟内前往B站APP完成支付</div></div>';
    }
  });
}

function stopBuy(){
  fetch('/api/buy/stop',{method:'POST'}).then(function(r){return r.json()}).then(function(d){
    toast('已停止','info');
    if(buyEventSource)buyEventSource.close();
    document.getElementById('btnStart').disabled=false;
    document.getElementById('btnStop').style.display='none';
    appendLog({Time:timeNow(),Type:'info',Message:'用户停止了抢票任务'});
  });
}

// ========== 历史记录 ==========
function loadHistory(){
  fetch('/api/history').then(function(r){return r.json()}).then(function(d){
    renderHistory(d.history||[]);
  });
}
function renderHistory(list){
  var el=document.getElementById('historyList');
  if(!list.length){el.innerHTML='<div class="empty">暂无收藏记录<br><br>在演出详情中点击"收藏"即可添加</div>';return;}
  var html='';
  for(var i=0;i<list.length;i++){
    var h=list[i];
    var price='待定';
    if(h.price_low>0)price='¥'+(h.price_low/100).toFixed(0);
    html+='<div class="history-item" id="histItem'+h.project_id+'">';
    html+='<div style="flex:1">';
    html+='<div class="history-name">'+esc(h.name||'(无标题)')+'</div>';
    html+='<div class="history-meta">ID:'+h.project_id+' | '+esc(h.venue||'未知')+' | '+price+'</div>';
    html+='</div>';
    html+='<div style="display:flex;gap:6px;align-items:center">';
    html+='<button class="btn btn-primary btn-small" onclick="openFromHistory('+h.project_id+')">打开面板</button>';
    html+='<button class="btn btn-danger btn-small" onclick="delHist('+h.project_id+')">删除</button>';
    html+='</div>';
    html+='</div>';
  }
  el.innerHTML=html;
}
function openFromHistory(id){
  showProject(id);
}
function delHist(id){
  fetch('/api/history/'+id,{method:'DELETE'}).then(function(){toast('已删除','info');loadHistory();});
}
function clearHistory(){
  if(!confirm('确定清空所有收藏?'))return;
  fetch('/api/history/clear',{method:'DELETE'}).then(function(){toast('已清空','info');loadHistory();});
}

function exitApp(){
  if(!confirm('确定退出程序?'))return;
  fetch('/api/app/exit',{method:'POST'}).then(function(r){return r.json()}).then(function(d){
    if(d&&d.code===0){
      toast('正在退出...','info');
      // 服务器关闭后页面会断开，提示用户即可
    }else{
      toast('退出失败','error');
    }
  }).catch(function(){toast('退出失败','error');});
}

// ========== 工具 ==========
function esc(s){if(!s)return'';return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;');}
function fmtTime(ts){if(!ts)return'未知';var d=new Date(ts*1000);return d.toLocaleString('zh-CN');}
function timeNow(){var d=new Date();return(d.getHours()+'').padStart(2,'0')+':'+(d.getMinutes()+'').padStart(2,'0')+':'+(d.getSeconds()+'').padStart(2,'0');}
function toast(msg,type){var t=document.getElementById('toast');t.textContent=msg;t.className='toast toast-'+type+' show';setTimeout(function(){t.className='toast';},3000);}

// ESC 关闭弹窗
document.addEventListener('keydown',function(e){if(e.key==='Escape')closeModal();});
</script>
</body>
</html>`)
	return sb.String()
}
