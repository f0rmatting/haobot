// Package notify pushes new messages, calls and the call assistant's notes
// to the owner through Telegram and group bots (飞书, 钉钉, 企业微信).
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultAPIBase = "https://api.telegram.org"

// TelegramConfig is the Telegram channel's setup.
type TelegramConfig struct {
	Enabled bool `json:"enabled"`
	// ChatID is the private chat, group or channel the bot writes to.
	ChatID string `json:"chatId"`
	// Proxy reaches Telegram where it is blocked, e.g. socks5://127.0.0.1:7890.
	Proxy string `json:"proxy"`
	// APIBase replaces https://api.telegram.org, e.g. for a self-hosted Bot
	// API server or a reverse proxy.
	APIBase string `json:"apiBase"`

	// Token is the bot token from @BotFather; stored sealed, never in JSON.
	Token string `json:"-"`
}

var tokenPattern = regexp.MustCompile(`^\d{5,}:[A-Za-z0-9_-]{30,}$`)

// Validate checks a configuration before it is saved or tested.
func (c TelegramConfig) Validate() error {
	if c.Token != "" && !tokenPattern.MatchString(c.Token) {
		return errors.New("Bot Token 格式不对，应形如 123456789:AAE…（在 @BotFather 里获取）")
	}
	if c.Enabled && c.Token == "" {
		return errors.New("请填写 Bot Token")
	}
	if c.Enabled && strings.TrimSpace(c.ChatID) == "" {
		return errors.New("请填写 Chat ID，或点“获取”自动查找")
	}
	if c.ChatID != "" && !validChatID(c.ChatID) {
		return errors.New("Chat ID 应该是数字（群组是负数），或频道的 @用户名")
	}
	if c.Proxy != "" {
		u, err := url.Parse(c.Proxy)
		if err != nil || u.Host == "" {
			return errors.New("代理地址格式不对，例如 socks5://127.0.0.1:7890 或 http://127.0.0.1:7890")
		}
		switch u.Scheme {
		case "http", "https", "socks5", "socks5h":
		default:
			return errors.New("代理只支持 http、https 和 socks5")
		}
	}
	if c.APIBase != "" {
		if u, err := url.Parse(c.APIBase); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return errors.New("API 地址需要以 https:// 开头")
		}
	}
	return nil
}

func validChatID(id string) bool {
	if strings.HasPrefix(id, "@") {
		return len(id) > 1
	}
	_, err := strconv.ParseInt(id, 10, 64)
	return err == nil
}

// Ready reports whether messages can be sent.
func (c TelegramConfig) Ready() bool { return c.Enabled && c.Token != "" && c.ChatID != "" }

// Telegram calls the Bot API.
type Telegram struct {
	mu      sync.Mutex
	clients map[string]*http.Client // by proxy
}

func (t *Telegram) client(proxy string) (*http.Client, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if c := t.clients[proxy]; c != nil {
		return c, nil
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if proxy != "" {
		u, err := url.Parse(proxy)
		if err != nil {
			return nil, err
		}
		transport.Proxy = http.ProxyURL(u)
	}
	c := &http.Client{Transport: transport, Timeout: 20 * time.Second}
	if t.clients == nil {
		t.clients = map[string]*http.Client{}
	}
	t.clients[proxy] = c
	return c, nil
}

// apiError is a failure the Bot API reported. RetryAfter is set on 429.
type apiError struct {
	Code        int
	Description string
	RetryAfter  time.Duration
}

func (e *apiError) Error() string {
	text := e.Description
	switch {
	case e.Code == 401:
		text = "Bot Token 无效（" + text + "）"
	case e.Code == 400 && strings.Contains(text, "chat not found"):
		text = "找不到这个聊天：请先给机器人发一条消息，或者把机器人拉进群里，再检查 Chat ID"
	case e.Code == 403:
		text = "机器人没有权限给这个聊天发消息（" + text + "）：可能被屏蔽或移出了群"
	case e.Code == 429:
		text = "发送太频繁，被 Telegram 限流"
	}
	return fmt.Sprintf("Telegram 返回 %d：%s", e.Code, text)
}

// permanent errors will fail again however often they are retried.
func (e *apiError) permanent() bool {
	return e.Code == 400 || e.Code == 401 || e.Code == 403 || e.Code == 404
}

// call posts a Bot API method. The token is scrubbed from every error, as
// net/http puts the full URL into them.
func (t *Telegram) call(ctx context.Context, cfg TelegramConfig, method string, body, out any) error {
	base := strings.TrimRight(cfg.APIBase, "/")
	if base == "" {
		base = defaultAPIBase
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	client, err := t.client(cfg.Proxy)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/bot"+cfg.Token+"/"+method, bytes.NewReader(payload))
	if err != nil {
		return scrub(err, cfg.Token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("连接 Telegram 失败（国内网络需要设置代理）：%w", scrub(err, cfg.Token))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return scrub(err, cfg.Token)
	}
	var envelope struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		ErrorCode   int             `json:"error_code"`
		Description string          `json:"description"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("Telegram 返回 %d，内容不是 JSON（API 地址或代理可能不对）", resp.StatusCode)
	}
	if !envelope.OK {
		return &apiError{Code: cmpInt(envelope.ErrorCode, resp.StatusCode), Description: envelope.Description, RetryAfter: time.Duration(envelope.Parameters.RetryAfter) * time.Second}
	}
	if out != nil {
		return json.Unmarshal(envelope.Result, out)
	}
	return nil
}

func cmpInt(a, b int) int {
	if a != 0 {
		return a
	}
	return b
}

type scrubbed struct{ text string }

func (e scrubbed) Error() string { return e.text }

func scrub(err error, token string) error {
	if err == nil || token == "" {
		return err
	}
	return scrubbed{strings.ReplaceAll(err.Error(), token, "<token>")}
}

// maxMessage is Telegram's limit for one message, in characters.
const maxMessage = 4096

// Send posts an HTML message to the configured chat.
func (t *Telegram) Send(ctx context.Context, cfg TelegramConfig, html string) error {
	if r := []rune(html); len(r) > maxMessage {
		html = string(r[:maxMessage-1]) + "…"
	}
	return t.call(ctx, cfg, "sendMessage", map[string]any{
		"chat_id": cfg.ChatID, "text": html, "parse_mode": "HTML",
		"link_preview_options": map[string]bool{"is_disabled": true},
	}, nil)
}

// Bot returns the bot's @username, which also checks the token.
func (t *Telegram) Bot(ctx context.Context, cfg TelegramConfig) (string, error) {
	var me struct {
		Username string `json:"username"`
	}
	err := t.call(ctx, cfg, "getMe", map[string]any{}, &me)
	return me.Username, err
}

// Chat is a chat the bot has seen, offered as the push target.
type Chat struct {
	ID    int64  `json:"id"`
	Type  string `json:"type"` // private, group, supergroup, channel
	Title string `json:"title"`
}

// Chats lists the chats that recently wrote to the bot or added it.
func (t *Telegram) Chats(ctx context.Context, cfg TelegramConfig) ([]Chat, error) {
	type tgChat struct {
		ID        int64  `json:"id"`
		Type      string `json:"type"`
		Title     string `json:"title"`
		Username  string `json:"username"`
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
	}
	type holder struct {
		Chat tgChat `json:"chat"`
	}
	var updates []struct {
		Message           *holder `json:"message"`
		ChannelPost       *holder `json:"channel_post"`
		MyChatMember      *holder `json:"my_chat_member"`
		EditedMessage     *holder `json:"edited_message"`
		EditedChannelPost *holder `json:"edited_channel_post"`
	}
	if err := t.call(ctx, cfg, "getUpdates", map[string]any{"limit": 100, "timeout": 0}, &updates); err != nil {
		var api *apiError
		if errors.As(err, &api) && api.Code == 409 {
			return nil, errors.New("这个机器人设置了 Webhook，无法自动获取 Chat ID，请手动填写")
		}
		return nil, err
	}
	seen := map[int64]bool{}
	var out []Chat
	for i := len(updates) - 1; i >= 0; i-- { // newest first
		u := updates[i]
		for _, h := range []*holder{u.Message, u.ChannelPost, u.MyChatMember, u.EditedMessage, u.EditedChannelPost} {
			if h == nil || h.Chat.ID == 0 || seen[h.Chat.ID] {
				continue
			}
			seen[h.Chat.ID] = true
			title := h.Chat.Title
			if title == "" {
				title = strings.TrimSpace(h.Chat.FirstName + " " + h.Chat.LastName)
			}
			if h.Chat.Username != "" {
				title = strings.TrimSpace(title + " @" + h.Chat.Username)
			}
			out = append(out, Chat{ID: h.Chat.ID, Type: h.Chat.Type, Title: title})
		}
	}
	return out, nil
}
