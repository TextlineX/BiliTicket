package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/yourname/gobiliticket/internal/appdir"
)

// Config B站抢票配置
type Config struct {
	// 认证信息
	BiliBili struct {
		SESSDATA    string `mapstructure:"SESSDATA"`    // SESSDATA cookie
		BILI_JCT    string `mapstructure:"BILI_JCT"`    // BILI_JCT cookie
		BUVID3      string `mapstructure:"BUVID3"`      // BUVID3 cookie
		DedeUserID  string `mapstructure:"DEDEUSERID"`  // DedeUserID
	} `mapstructure:"bilibili"`

	// 抢票配置
	Ticket struct {
		AreaID      int    `mapstructure:"area_id"`       // 场馆ID
		ScheduleID  int    `mapstructure:"schedule_id"`   // 场次ID
		ItemID      int    `mapstructure:"item_id"`       // 票档ID
		UserID      int    `mapstructure:"user_id"`       // 用户ID
		Quantity    int    `mapstructure:"quantity"`      // 购买数量
		IntervalMs  int    `mapstructure:"interval_ms"`   // 轮询间隔(毫秒)
		MaxAttempts int    `mapstructure:"max_attempts"`  // 最大尝试次数
	} `mapstructure:"ticket"`

	// 推送配置
	Push struct {
		Enabled      bool   `mapstructure:"enabled"`
		ServerChan   struct {
			APIURL string `mapstructure:"api_url"`
			Key    string `mapstructure:"key"`
		} `mapstructure:"serverchan"`
		Bark struct {
			Token string `mapstructure:"token"`
		} `mapstructure:"bark"`
		PushPlus struct {
			Token string `mapstructure:"token"`
		} `mapstructure:"pushplus"`
		Ntfy struct {
			URL      string `mapstructure:"url"`
			Username string `mapstructure:"username"`
			Password string `mapstructure:"password"`
		} `mapstructure:"ntfy"`
	} `mapstructure:"push"`

	// 代理配置
	Proxy struct {
		HTTPProxy  string `mapstructure:"http_proxy"`
		HTTPSProxy string `mapstructure:"https_proxy"`
	} `mapstructure:"proxy"`

	// 服务器模式
	Server struct {
		Enabled   bool   `mapstructure:"enabled"`
		Port     int    `mapstructure:"port"`
		Share    bool   `mapstructure:"share"`
		Master   string `mapstructure:"master"`
		SelfIP   string `mapstructure:"self_ip"`
	} `mapstructure:"server"`
}

// LoadFromEnv 从环境变量加载配置
func (c *Config) LoadFromEnv() {
	// B站 Cookie - 优先环境变量，其次凭证文件
	c.BiliBili.SESSDATA = getEnv("BTB_SESSDATA", "")
	c.BiliBili.BILI_JCT = getEnv("BTB_BILI_JCT", "")
	c.BiliBili.BUVID3 = getEnv("BTB_BUVID3", "")
	c.BiliBili.DedeUserID = getEnvFirst("", "BTB_DEDEUSERID", "BTB_DEDE_USER_ID", "BTB_DEDEUSER_ID")

	// 如果环境变量为空，尝试从凭证文件加载
	if c.BiliBili.SESSDATA == "" {
		loadCookiesFromFile(c)
	}

	// 抢票配置
	c.Ticket.IntervalMs = 500 // 默认500ms
	c.Ticket.MaxAttempts = 1000

	// 推送
	c.Push.ServerChan.APIURL = getEnvFirst("", "BTB_SERVERCHAN_APIURL", "BTB_SERVERCHAN3APIURL", "BTB_SERVERCHAN_URL")
	c.Push.ServerChan.Key = getEnvFirst("", "BTB_SERVERCHAN_KEY", "BTB_SERVERCHANKEY")
	c.Push.Bark.Token = getEnvFirst("", "BTB_BARK_TOKEN", "BTB_BARKTOKEN")
	c.Push.PushPlus.Token = getEnvFirst("", "BTB_PUSHPLUS_TOKEN", "BTB_PUSHPLUSTOKEN")
	c.Push.Ntfy.URL = getEnvFirst("", "BTB_NTFY_URL")
	c.Push.Ntfy.Username = getEnvFirst("", "BTB_NTFY_USERNAME")
	c.Push.Ntfy.Password = getEnvFirst("", "BTB_NTFY_PASSWORD")

	// 推送总开关：优先显式配置，否则“有任意推送凭证即启用”
	pushEnabledRaw := getEnvFirst("", "BTB_PUSH_ENABLED", "BTB_PUSH_ENABLE")
	if pushEnabledRaw != "" {
		c.Push.Enabled = parseEnvBool(pushEnabledRaw)
	} else {
		c.Push.Enabled = c.Push.ServerChan.Key != "" ||
			c.Push.Bark.Token != "" ||
			c.Push.PushPlus.Token != "" ||
			c.Push.Ntfy.URL != ""
	}

	// 代理
	c.Proxy.HTTPProxy = getEnv("HTTP_PROXY", "")
	c.Proxy.HTTPSProxy = getEnv("HTTPS_PROXY", "")
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func getEnvFirst(defaultVal string, keys ...string) string {
	for _, key := range keys {
		if val := os.Getenv(key); val != "" {
			return val
		}
	}
	return defaultVal
}

func parseEnvBool(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if b, err := strconv.ParseBool(s); err == nil {
		return b
	}
	switch strings.ToLower(s) {
	case "y", "yes", "on", "enable", "enabled", "1":
		return true
	default:
		return false
	}
}

type savedCookiesFile struct {
	SESSDATA   string `json:"sessdata"`
	BILI_JCT   string `json:"bili_jct"`
	BUVID3     string `json:"buvid3"`
	DedeUserID string `json:"dede_user_id"`
}

func loadCookiesFromFile(c *Config) {
	path := appdir.FindCookiesPath()

	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	var saved savedCookiesFile
	if err := json.Unmarshal(data, &saved); err != nil {
		return
	}

	if saved.SESSDATA != "" {
		c.BiliBili.SESSDATA = saved.SESSDATA
	}
	if saved.BILI_JCT != "" {
		c.BiliBili.BILI_JCT = saved.BILI_JCT
	}
	if saved.BUVID3 != "" {
		c.BiliBili.BUVID3 = saved.BUVID3
	}
	if saved.DedeUserID != "" {
		c.BiliBili.DedeUserID = saved.DedeUserID
	}
}

// Validate 验证配置
func (c *Config) Validate() error {
	if c.BiliBili.SESSDATA == "" {
		return fmt.Errorf("SESSDATA is required")
	}
	if c.BiliBili.BILI_JCT == "" {
		return fmt.Errorf("BILI_JCT is required")
	}
	if c.Ticket.AreaID == 0 {
		return fmt.Errorf("area_id is required")
	}
	if c.Ticket.ScheduleID == 0 {
		return fmt.Errorf("schedule_id is required")
	}
	if c.Ticket.ItemID == 0 {
		return fmt.Errorf("item_id is required")
	}
	return nil
}
