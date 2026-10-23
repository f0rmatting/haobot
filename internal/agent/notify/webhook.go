package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Bots posts to the group-bot webhooks of 飞书, 钉钉 and 企业微信.
type Bots struct {
	Client *http.Client // nil: a client with a 20 s timeout
	now    func() time.Time
}

var defaultBotClient = &http.Client{Timeout: 20 * time.Second}

// botError is a failure a group bot reported.
type botError struct {
	kind      Kind
	code      int
	text      string
	permanent bool
	limited   bool
}

func (e *botError) Error() string {
	return fmt.Sprintf("%s 返回 %d：%s", e.kind.Label(), e.code, e.text)
}

// Send posts a note to a group bot.
func (b *Bots) Send(ctx context.Context, kind Kind, w Webhook, n Note) error {
	endpoint, body, err := b.request(kind, w, n)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return scrub(err, w.URL)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	client := b.Client
	if client == nil {
		client = defaultBotClient
	}
	resp, err := client.Do(req)
	if err != nil {
		// net/http puts the URL, and with it the token, into the error.
		return fmt.Errorf("连接%s失败：%w", kind.Label(), scrub(scrub(err, endpoint), w.URL))
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var reply struct {
		Code    *int   `json:"code"`    // 飞书
		Msg     string `json:"msg"`     // 飞书
		ErrCode *int   `json:"errcode"` // 钉钉, 企业微信
		ErrMsg  string `json:"errmsg"`  // 钉钉, 企业微信
	}
	if json.Unmarshal(data, &reply) != nil {
		if resp.StatusCode == http.StatusNotFound {
			return &botError{kind: kind, code: resp.StatusCode, text: "Webhook 地址不存在，机器人可能已被删除", permanent: true}
		}
		return &botError{kind: kind, code: resp.StatusCode, text: "内容不是 JSON，Webhook 地址可能不对", permanent: resp.StatusCode < 500}
	}
	code, msg := 0, reply.Msg
	switch {
	case reply.Code != nil:
		code = *reply.Code
	case reply.ErrCode != nil:
		code, msg = *reply.ErrCode, reply.ErrMsg
	}
	if code == 0 && resp.StatusCode < 400 {
		return nil
	}
	if code == 0 {
		code = resp.StatusCode
	}
	return explain(kind, code, msg)
}

// explain turns a bot's error code into advice.
func explain(kind Kind, code int, msg string) *botError {
	e := &botError{kind: kind, code: code, text: msg, permanent: true}
	switch kind {
	case KindFeishu:
		switch code {
		case 19021:
			e.text = "签名校验失败：请检查“签名密钥”是否和机器人设置里的一致（" + msg + "）"
		case 19022:
			e.text = "IP 不在机器人的白名单里（" + msg + "）"
		case 19024:
			e.text = "消息里没有机器人要求的自定义关键词：可以把关键词设为“号小助”，或关闭关键词校验（" + msg + "）"
		case 9499, 11232:
			e.text, e.permanent, e.limited = "发送太频繁，被飞书限流", false, true
		}
	case KindDingTalk:
		switch code {
		case 310000:
			e.text = "安全设置校验失败：自定义关键词请包含“号小助”，加签请填写“签名密钥”，或检查 IP 白名单（" + msg + "）"
		case 300001, 400101, 400102:
			e.text = "Webhook 地址的 access_token 无效，机器人可能已被删除（" + msg + "）"
		case 130101, 410100:
			e.text, e.permanent, e.limited = "发送太频繁，被钉钉限流（每分钟最多 20 条）", false, true
		}
	case KindWeCom:
		switch code {
		case 93000:
			e.text = "Webhook 地址无效，机器人可能已被移出群聊（" + msg + "）"
		case 45009:
			e.text, e.permanent, e.limited = "发送太频繁，被企业微信限流（每分钟最多 20 条）", false, true
		}
	}
	if code >= 500 && code < 600 {
		e.permanent = false
	}
	return e
}

func (b *Bots) clock() time.Time {
	if b.now != nil {
		return b.now()
	}
	return time.Now()
}

// sign is the HMAC-SHA256 signature 飞书 and 钉钉 use: 飞书 keys the HMAC
// with "timestamp\nsecret" over nothing, 钉钉 keys it with the secret over
// "timestamp\nsecret".
func sign(key, data string) string {
	h := hmac.New(sha256.New, []byte(key))
	h.Write([]byte(data))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// request builds the endpoint and JSON body for a note.
func (b *Bots) request(kind Kind, w Webhook, n Note) (string, map[string]any, error) {
	switch kind {
	case KindFeishu:
		body := map[string]any{"msg_type": "interactive", "card": feishuCard(n)}
		if w.Secret != "" {
			ts := strconv.FormatInt(b.clock().Unix(), 10)
			body["timestamp"], body["sign"] = ts, sign(ts+"\n"+w.Secret, "")
		}
		return w.URL, body, nil
	case KindDingTalk:
		endpoint := w.URL
		if w.Secret != "" {
			ts := strconv.FormatInt(b.clock().UnixMilli(), 10)
			u, err := url.Parse(w.URL)
			if err != nil {
				return "", nil, err
			}
			q := u.Query()
			q.Set("timestamp", ts)
			q.Set("sign", sign(w.Secret, ts+"\n"+w.Secret))
			u.RawQuery = q.Encode()
			endpoint = u.String()
		}
		text := "#### " + mdSafe.Replace(n.heading()) + "\n\n" + n.markdown("  \n", func(s string) string { return "**" + s + "**" })
		return endpoint, map[string]any{"msgtype": "markdown", "markdown": map[string]string{"title": n.heading(), "text": text}}, nil
	case KindWeCom:
		text := "**" + mdSafe.Replace(n.heading()) + "**\n" + n.markdown("\n", func(s string) string { return "`" + s + "`" })
		// 企业微信 caps markdown at 4096 bytes.
		if len(text) > 4000 {
			text = truncateBytes(text, 4000) + "…"
		}
		return w.URL, map[string]any{"msgtype": "markdown", "markdown": map[string]string{"content": text}}, nil
	}
	return "", nil, fmt.Errorf("不支持的推送方式 %q", kind)
}

// truncateBytes cuts s to at most max bytes, at a rune boundary.
func truncateBytes(s string, max int) string {
	last := 0
	for i := range s {
		if i > max {
			return s[:last]
		}
		last = i
	}
	return s
}

// feishuCard renders a note as a message card, with the title in a coloured
// header.
func feishuCard(n Note) map[string]any {
	template := map[string]string{"blue": "blue", "green": "green", "orange": "orange", "red": "red"}[n.Color]
	if template == "" {
		template = "grey"
	}
	content := n.markdown("\n", func(s string) string { return "**" + s + "**" })
	return map[string]any{
		"config": map[string]bool{"wide_screen_mode": true},
		"header": map[string]any{
			"template": template,
			"title":    map[string]string{"tag": "plain_text", "content": n.heading()},
		},
		"elements": []any{map[string]string{"tag": "markdown", "content": strings.TrimSpace(content)}},
	}
}
