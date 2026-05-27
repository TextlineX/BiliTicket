package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/spf13/cobra"
	"github.com/yourname/gobiliticket/internal/appdir"
)

var loginSavePath string

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "扫码登录 B站",
	Long: `通过扫码方式登录 B站，自动获取并保存登录凭证。
成功登录后凭证会保存到应用数据目录（可通过 GOBILITICKET_HOME 指定），
下次抢票时自动加载，无需重复登录。`,
	RunE: runLogin,
}

func init() {
	rootCmd.AddCommand(loginCmd)
	loginCmd.Flags().StringVarP(&loginSavePath, "save", "s", "", "保存凭证的文件路径")
}

func runLogin(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════════╗")
	fmt.Println("║       B站抢票工具 - 扫码登录               ║")
	fmt.Println("╚══════════════════════════════════════════════╝")
	fmt.Println()

	// 1. 获取登录二维码
	qrURL, qrcodeKey, err := getLoginQR(ctx)
	if err != nil {
		return fmt.Errorf("获取登录二维码失败: %w", err)
	}

	// 2. 生成 HTML 登录页面
	var qrPageURL string
	_, qrPageURL, err = generateLoginPage(qrURL)
	if err != nil {
		return fmt.Errorf("生成登录页面失败: %w", err)
	}

	// 3. 打开浏览器
	fmt.Println("  📱 正在打开浏览器扫码页面...")
	fmt.Println()
	openBrowser(qrPageURL)
	fmt.Printf("  🔗 或手动打开: %s\n", qrPageURL)
	fmt.Println()

	// 4. 轮询登录状态
	fmt.Println("  ⏳ 等待扫码...")
	fmt.Println()

	cookies, err := pollLoginStatus(ctx, qrcodeKey)
	if err != nil {
		if ctx.Err() == context.Canceled {
			return fmt.Errorf("登录已取消")
		}
		return fmt.Errorf("登录失败: %w", err)
	}

	// 5. 保存凭证
	if err := saveCookies(cookies); err != nil {
		return fmt.Errorf("保存凭证失败: %w", err)
	}

	fmt.Println()
	fmt.Println("  ✅ 登录成功！")
	fmt.Println()
	fmt.Printf("  📁 凭证已保存至: %s\n", getDefaultSavePath())
	fmt.Println()
	fmt.Println("  📋 凭证内容:")
	fmt.Printf("    SESSDATA:    %s...\n", maskString(cookies.SESSDATA))
	fmt.Printf("    BILI_JCT:    %s...\n", maskString(cookies.BILI_JCT))
	fmt.Printf("    BUVID3:      %s...\n", maskString(cookies.BUVID3))
	fmt.Printf("    DedeUserID:  %s\n", cookies.DedeUserID)
	fmt.Println()
	fmt.Println("  🎫 现在可以开始抢票了！")
	fmt.Println()

	return nil
}

// ============ 登录 API ============

type loginQRResp struct {
	Status bool `json:"status"`
	Data   struct {
		URL       string `json:"url"`
		OAuthKey  string `json:"oauthKey"`
	} `json:"data"`
}

type loginCookies struct {
	SESSDATA   string `json:"SESSDATA"`
	BILI_JCT   string `json:"BILI_JCT"`
	BUVID3     string `json:"BUVID3"`
	DedeUserID string `json:"DedeUserID"`
}

func getLoginQR(ctx context.Context) (qrURL, oauthKey string, err error) {
	reqURL := "https://passport.bilibili.com/x/passport-login/web/qrcode/generate"

	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Referer", "https://passport.bilibili.com/")

	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			URL       string `json:"url"`
			QrcodeKey string `json:"qrcode_key"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", err
	}

	if result.Code != 0 || result.Data.URL == "" || result.Data.QrcodeKey == "" {
		return "", "", fmt.Errorf("服务端返回异常: code=%d msg=%s", result.Code, result.Message)
	}

	return result.Data.URL, result.Data.QrcodeKey, nil
}

func pollLoginStatus(ctx context.Context, qrcodeKey string) (*loginCookies, error) {
	pollURL := "https://passport.bilibili.com/x/passport-login/web/qrcode/poll"

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	timeout := time.After(5 * time.Minute) // 5分钟超时
	tickCount := 0

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timeout:
			return nil, fmt.Errorf("扫码超时，请重试")
		case <-ticker.C:
			tickCount++
			cookies, status, msg, err := checkLoginStatus(ctx, pollURL, qrcodeKey)
			if err != nil {
				return nil, err
			}
			if cookies != nil {
				return cookies, nil
			}

			switch status {
			case 86101:
				// 尚未扫码，静默等待
			case 86090:
				fmt.Println("  📲 已扫码，请在手机端确认登录...")
			case 86038:
				return nil, fmt.Errorf("二维码已失效，请重新登录")
			default:
				// 有时会返回一些临时状态，避免刷屏：每10秒提示一次
				if msg != "" && tickCount%10 == 0 {
					fmt.Printf("  ⏳ %s\n", msg)
				}
				if msg != "" && status != 0 && status != 86101 && status != 86090 {
					return nil, fmt.Errorf("登录失败: %s", msg)
				}
			}
		}
	}
}

func checkLoginStatus(ctx context.Context, postURL, qrcodeKey string) (*loginCookies, int, string, error) {
	apiURL := postURL + "?qrcode_key=" + url.QueryEscape(qrcodeKey)

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Timeout: 30 * time.Second, Jar: jar}
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, 0, "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Referer", "https://passport.bilibili.com/")

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, "", err
	}
	defer resp.Body.Close()

	cookies := extractLoginCookiesFromJar(jar)
	mergeLoginCookiesFromHTTPCookies(&cookies, resp.Cookies())
	if cookies.SESSDATA != "" {
		return &cookies, 0, "", nil
	}

	// 解析响应体获取状态
	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Code   int    `json:"code"`
		Data   any    `json:"data"`
		Msg    string `json:"message"`
	}
	json.Unmarshal(body, &result)

	if result.Code != 0 {
		return nil, result.Code, result.Msg, nil
	}

	dataMap, _ := result.Data.(map[string]any)
	dataCode := 0
	if v, ok := dataMap["code"].(float64); ok {
		dataCode = int(v)
	}
	msg := ""
	if v, ok := dataMap["message"].(string); ok {
		msg = v
	}

	return nil, dataCode, msg, nil
}

// ============ HTML 登录页面 ============

func generateLoginPage(qrURL string) (pagePath, pageURL string, err error) {
	// 从响应URL中提取 qrcode_key
	qrcodeKey := ""
	loginPageURL := qrURL
	if u, err := url.Parse(qrURL); err == nil {
		qrcodeKey = u.Query().Get("qrcode_key")
		// 构建干净的登录URL
		loginPageURL = fmt.Sprintf("https://account.bilibili.com/h5/account-h5/auth/scan-web?qrcode_key=%s&from=", qrcodeKey)
	}

	tmpDir := os.TempDir()
	pagePath = filepath.Join(tmpDir, "gobiliticket_login.html")

	// 使用 qrserver.com 生成二维码图片
	qrImgURL := fmt.Sprintf("https://api.qrserver.com/v1/create-qr-code/?size=300x300&margin=10&data=%s",
		url.QueryEscape(loginPageURL))

	html := fmt.Sprintf(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>B站抢票工具 - 扫码登录</title>
<style>
    * { margin: 0; padding: 0; box-sizing: border-box; }
    body {
        font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
        background: linear-gradient(135deg, #1a1a2e 0%%, #16213e 50%%, #0f3460 100%%);
        min-height: 100vh;
        display: flex;
        align-items: center;
        justify-content: center;
        color: white;
    }
    .card {
        background: rgba(255,255,255,0.05);
        backdrop-filter: blur(10px);
        border: 1px solid rgba(255,255,255,0.1);
        border-radius: 24px;
        padding: 48px;
        text-align: center;
        max-width: 420px;
        width: 90%%;
    }
    .logo { font-size: 48px; margin-bottom: 8px; }
    h1 {
        font-size: 24px;
        font-weight: 600;
        margin-bottom: 8px;
        background: linear-gradient(90deg, #00d4ff, #9b59b6);
        -webkit-background-clip: text;
        -webkit-text-fill-color: transparent;
    }
    .subtitle {
        color: rgba(255,255,255,0.5);
        font-size: 14px;
        margin-bottom: 32px;
    }
    .qr-container {
        background: white;
        border-radius: 16px;
        padding: 20px;
        display: inline-block;
        margin-bottom: 24px;
        box-shadow: 0 8px 32px rgba(0,0,0,0.3);
    }
    .qr-container img {
        display: block;
        width: 240px;
        height: 240px;
    }
    .status {
        font-size: 16px;
        font-weight: 500;
        min-height: 24px;
        transition: all 0.3s;
    }
    .status.waiting { color: rgba(255,255,255,0.6); }
    .status.scanned { color: #f39c12; }
    .status.success { color: #2ecc71; }
    .status.error { color: #e74c3c; }
    .steps {
        text-align: left;
        margin-top: 24px;
        padding: 20px;
        background: rgba(255,255,255,0.05);
        border-radius: 12px;
    }
    .step {
        display: flex;
        align-items: center;
        gap: 12px;
        margin: 10px 0;
        color: rgba(255,255,255,0.7);
        font-size: 14px;
    }
    .step-num {
        width: 24px;
        height: 24px;
        background: rgba(255,255,255,0.1);
        border-radius: 50%%;
        display: flex;
        align-items: center;
        justify-content: center;
        font-size: 12px;
        flex-shrink: 0;
    }
    .footer {
        margin-top: 24px;
        font-size: 12px;
        color: rgba(255,255,255,0.3);
    }
</style>
</head>
<body>
<div class="card">
    <div class="logo">🎫</div>
    <h1>B站抢票工具</h1>
    <p class="subtitle">扫码登录以获取抢票凭证</p>

    <div class="qr-container">
        <img src="%s" alt="登录二维码" id="qr">
    </div>

    <div class="status waiting" id="status">👈 请使用B站APP扫码登录</div>

    <div class="steps">
        <div class="step"><div class="step-num">1</div><span>打开手机B站客户端</span></div>
        <div class="step"><div class="step-num">2</div><span>点击右上角扫一扫</span></div>
        <div class="step"><div class="step-num">3</div><span>扫描左侧二维码并在手机确认</span></div>
    </div>

    <div class="footer">凭证将自动保存，请勿关闭此页面</div>
</div>

<script>
// 扫码后会触发页面跳转
document.addEventListener('visibilitychange', function() {
    if (document.visibilityState === 'hidden') {
        const statusEl = document.getElementById('status');
        statusEl.textContent = '🎉 登录成功！请关闭此页面';
        statusEl.className = 'status success';
    }
});

// 2秒后提示等待扫码
setTimeout(function() {
    const statusEl = document.getElementById('status');
    statusEl.textContent = '⏳ 等待扫码中...';
}, 2000);
</script>
</body>
</html>`, qrImgURL)

	if err := os.WriteFile(pagePath, []byte(html), 0644); err != nil {
		return "", "", err
	}

	return pagePath, "file://" + pagePath, nil
}

// ============ 凭证保存 ============

type savedConfig struct {
	SESSDATA   string `json:"sessdata"`
	BILI_JCT   string `json:"bili_jct"`
	BUVID3     string `json:"buvid3"`
	DedeUserID string `json:"dede_user_id"`
	SavedAt    string `json:"saved_at"`
}

func saveCookies(cookies *loginCookies) error {
	savePath := loginSavePath
	if savePath == "" {
		savePath = getDefaultSavePath()
	}

	dir := filepath.Dir(savePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}

	cfg := savedConfig{
		SESSDATA:   cookies.SESSDATA,
		BILI_JCT:   cookies.BILI_JCT,
		BUVID3:     cookies.BUVID3,
		DedeUserID: cookies.DedeUserID,
		SavedAt:    time.Now().Format("2006-01-02 15:04:05"),
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(savePath, data, 0600); err != nil {
		return err
	}

	return nil
}

func getDefaultSavePath() string {
	return appdir.CookiesPath()
}

// ============ 辅助函数 ============

func maskString(s string) string {
	if len(s) <= 8 {
		return "***"
	}
	return s[:4] + "..." + s[len(s)-4:]
}

func openBrowser(url string) {
	var err error
	switch runtime.GOOS {
	case "windows":
		err = exec.Command("cmd", "/c", "start", url).Start()
	case "darwin":
		err = exec.Command("open", url).Start()
	default:
		err = exec.Command("xdg-open", url).Start()
	}
	if err != nil {
		fmt.Printf("  ⚠️  无法自动打开浏览器，请手动访问:\n    %s\n", url)
	}
}
