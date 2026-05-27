package push

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Pusher 推送接口
type Pusher interface {
	Send(title, content string) error
}

// Config 推送配置
type Config struct {
	ServerChan ServerChanConfig
	Bark      BarkConfig
	PushPlus  PushPlusConfig
	Ntfy      NtfyConfig
}

// ServerChanConfig Server酱配置
type ServerChanConfig struct {
	APIURL string
	Key    string
}

// BarkConfig Bark配置
type BarkConfig struct {
	Token string
}

// PushPlusConfig PushPlus配置
type PushPlusConfig struct {
	Token string
}

// NtfyConfig Ntfy配置
type NtfyConfig struct {
	URL      string
	Username string
	Password string
}

// Manager 推送管理器
type Manager struct {
	pushers []Pusher
}

// NewManager 创建推送管理器
func NewManager(cfg *Config) *Manager {
	m := &Manager{}

	if cfg.ServerChan.APIURL != "" && cfg.ServerChan.Key != "" {
		m.pushers = append(m.pushers, NewServerChan(cfg.ServerChan.APIURL, cfg.ServerChan.Key))
	}

	if cfg.Bark.Token != "" {
		m.pushers = append(m.pushers, NewBark(cfg.Bark.Token))
	}

	if cfg.PushPlus.Token != "" {
		m.pushers = append(m.pushers, NewPushPlus(cfg.PushPlus.Token))
	}

	if cfg.Ntfy.URL != "" {
		m.pushers = append(m.pushers, NewNtfy(cfg.Ntfy.URL, cfg.Ntfy.Username, cfg.Ntfy.Password))
	}

	return m
}

// Send 发送所有推送
func (m *Manager) Send(title, content string) {
	for _, p := range m.pushers {
		go func(pusher Pusher) {
			if err := pusher.Send(title, content); err != nil {
				fmt.Printf("[WARN] 推送失败: %v\n", err)
			}
		}(p)
	}
}

// ============ ServerChan 推送 ============

// ServerChan Server酱推送
type ServerChan struct {
	apiURL string
	key    string
}

func NewServerChan(apiURL, key string) *ServerChan {
	return &ServerChan{apiURL: apiURL, key: key}
}

func (s *ServerChan) Send(title, content string) error {
	if s.apiURL == "" {
		s.apiURL = "https://sctapi.ftqq.com/"
	}

	url := fmt.Sprintf("%s%s.send", s.apiURL, s.key)

	data := map[string]string{
		"title":   title,
		"content": content,
	}

	body, _ := json.Marshal(data)
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

// ============ Bark 推送 ============

// Bark Bark 推送
type Bark struct {
	token string
	serverURL string
}

func NewBark(token string) *Bark {
	return &Bark{
		token:     token,
		serverURL: "https://api.day.app",
	}
}

func (b *Bark) Send(title, content string) error {
	url := fmt.Sprintf("%s/%s", b.serverURL, b.token)

	data := map[string]interface{}{
		"title": title,
		"body":  content,
		"icon":  "https://www.bilibili.com/favicon.ico",
	}

	body, _ := json.Marshal(data)
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

// ============ PushPlus 推送 ============

// PushPlus 微信推送+
type PushPlus struct {
	token string
}

func NewPushPlus(token string) *PushPlus {
	return &PushPlus{token: token}
}

func (p *PushPlus) Send(title, content string) error {
	url := "http://www.pushplus.plus/send"

	data := map[string]string{
		"token":    p.token,
		"title":    title,
		"content":  content,
		"template": "html",
	}

	body, _ := json.Marshal(data)
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

// ============ Ntfy 推送 ============

// Ntfy Ntfy 推送
type Ntfy struct {
	url      string
	username string
	password string
}

func NewNtfy(url, username, password string) *Ntfy {
	return &Ntfy{
		url:      strings.TrimSuffix(url, "/"),
		username: username,
		password: password,
	}
}

func (n *Ntfy) Send(title, content string) error {
	url := fmt.Sprintf("%s/%s", n.url, title)

	req, err := http.NewRequest("POST", url, strings.NewReader(content))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "text/plain")

	if n.username != "" {
		req.SetBasicAuth(n.username, n.password)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}
