package notify

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/f0rmatting/haobot/internal/agent/service"
	"github.com/f0rmatting/haobot/internal/agent/store"
)

const testToken = "123456789:AAEabcdefghijklmnopqrstuvwxyz012345"

// fakeBotAPI records sendMessage calls; the first `fail` of them get 429.
type fakeBotAPI struct {
	mu   sync.Mutex
	sent []map[string]any
	fail int
}

func (f *fakeBotAPI) server(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/bot"+testToken+"/") {
			w.WriteHeader(401)
			io.WriteString(w, `{"ok":false,"error_code":401,"description":"Unauthorized"}`)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		defer f.mu.Unlock()
		switch strings.TrimPrefix(r.URL.Path, "/bot"+testToken+"/") {
		case "sendMessage":
			if f.fail > 0 {
				f.fail--
				w.WriteHeader(429)
				io.WriteString(w, `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":1}}`)
				return
			}
			f.sent = append(f.sent, body)
			io.WriteString(w, `{"ok":true,"result":{}}`)
		case "getMe":
			io.WriteString(w, `{"ok":true,"result":{"username":"relay_bot"}}`)
		case "getUpdates":
			io.WriteString(w, `{"ok":true,"result":[{"message":{"chat":{"id":42,"type":"private","first_name":"Alice"}}},{"my_chat_member":{"chat":{"id":-100,"type":"group","title":"家里"}}}]}`)
		}
	}))
}

func (f *fakeBotAPI) texts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, b := range f.sent {
		out = append(out, b["text"].(string))
	}
	return out
}

func testConfig(base string) TelegramConfig {
	return TelegramConfig{Enabled: true, Token: testToken, ChatID: "42", APIBase: base}
}

func TestValidate(t *testing.T) {
	cfg := testConfig("")
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*TelegramConfig){
		"bad token":  func(c *TelegramConfig) { c.Token = "nope" },
		"no chat":    func(c *TelegramConfig) { c.ChatID = "" },
		"bad chat":   func(c *TelegramConfig) { c.ChatID = "abc" },
		"bad proxy":  func(c *TelegramConfig) { c.Proxy = "ftp://x:1" },
		"bad base":   func(c *TelegramConfig) { c.APIBase = "api.telegram.org" },
		"no token":   func(c *TelegramConfig) { c.Token = "" },
		"bare proxy": func(c *TelegramConfig) { c.Proxy = "127.0.0.1:7890" },
	} {
		c := cfg
		change(&c)
		if c.Validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
	c := cfg
	c.ChatID, c.Proxy = "@my_channel", "socks5://127.0.0.1:7890"
	if err := c.Validate(); err != nil {
		t.Errorf("channel name and socks5 proxy rejected: %v", err)
	}
}

func TestTokenNeverInErrors(t *testing.T) {
	var tg Telegram
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close() // connection refused, with the URL in the error
	err := tg.Send(context.Background(), testConfig(base), "x")
	if err == nil || strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), "AAEabc") {
		t.Fatalf("error leaks the token: %v", err)
	}
}

func TestBotChatsAndBadToken(t *testing.T) {
	api := &fakeBotAPI{}
	srv := api.server(t)
	defer srv.Close()
	var tg Telegram
	cfg := testConfig(srv.URL)
	if bot, err := tg.Bot(context.Background(), cfg); err != nil || bot != "relay_bot" {
		t.Fatalf("bot = %q, %v", bot, err)
	}
	chats, err := tg.Chats(context.Background(), cfg)
	if err != nil || len(chats) != 2 || chats[0].ID != -100 || chats[1].Title != "Alice" {
		t.Fatalf("chats = %+v, %v", chats, err)
	}
	cfg.Token = "987654321:AAEzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"
	if _, err := tg.Bot(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "Token 无效") {
		t.Fatalf("bad token error = %v", err)
	}
}

func openStore(t *testing.T) *store.Store {
	st, err := store.Open(filepath.Join(t.TempDir(), "n.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestNotifierPushesAndRetries(t *testing.T) {
	api := &fakeBotAPI{fail: 1}
	srv := api.server(t)
	defer srv.Close()
	st := openStore(t)
	ctx := context.Background()
	st.SeeDevice(ctx, store.Device{ID: "d1", Name: "EC20-8901"}, time.Now())
	st.SaveSegment(ctx, store.Segment{PDUHash: "h1", PDU: "p", Sender: "10690", SentAt: time.Now(), ReceivedAt: time.Now(), Total: 1, Seq: 1,
		Body: "【银行】您的验证码 654321，<5 分钟>内有效", DeviceID: "d1"})
	msgs, _ := st.AssemblePending(ctx, time.Now(), time.Hour, nil)

	var mu sync.Mutex
	cfg := Config{Rules: DefaultRules(), Telegram: testConfig(srv.URL)}
	cfg.Rules.Ringing = true
	n := &Notifier{
		Config: func(context.Context) (Config, error) { mu.Lock(); defer mu.Unlock(); return cfg, nil },
		Store:  st, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Note: func(context.Context, string, string) {},
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go n.Run(runCtx)

	n.Messages(msgs)
	n.Call(service.CallEvent{Kind: service.CallEventRinging, DeviceName: "EC20-8901", Record: store.CallRecord{Number: "13800000000", StartedAt: time.Now()}})
	n.Call(service.CallEvent{Kind: service.CallEventNote, Record: store.CallRecord{Number: "13900000000", StartedAt: time.Now(),
		Summary: `{"caller":"顺丰快递员","purpose":"送包裹","urgency":"低","callback":"不需要","brief":"顺丰快递，已放门口"}`}})

	deadline := time.Now().Add(8 * time.Second)
	for len(api.texts()) < 3 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	texts := strings.Join(api.texts(), "\n----\n")
	if len(api.texts()) != 3 {
		t.Fatalf("sent %d messages (429 must be retried):\n%s", len(api.texts()), texts)
	}
	for _, want := range []string{"&lt;5 分钟&gt;", "验证码 <code>654321</code>", "EC20-8901", "📞 <b>来电</b>", "<b>顺丰快递，已放门口</b>", "来电者：顺丰快递员"} {
		if !strings.Contains(texts, want) {
			t.Errorf("missing %q in:\n%s", want, texts)
		}
	}
	// Turning SMS off stops SMS pushes.
	mu.Lock()
	cfg.Rules.SMS = false
	mu.Unlock()
	n.Messages(msgs)
	time.Sleep(300 * time.Millisecond)
	if len(api.texts()) != 3 {
		t.Fatal("SMS pushed while turned off")
	}
}

func TestFormatMessageMasksCodes(t *testing.T) {
	m := store.Message{Sender: "10690", Body: "验证码 654321，请勿泄露", Category: "验证码", Complete: true, ReceivedAt: time.Now()}
	text := MessageNote(m, "", "", true).HTML()
	if strings.Contains(text, "654321") || !strings.Contains(text, "••••••") {
		t.Fatalf("code not masked: %s", text)
	}
}

func TestRulesPickCategories(t *testing.T) {
	r := DefaultRules()
	r.SkipCategories = []string{"营销推广", Unclassified}
	for category, want := range map[string]bool{"验证码": true, "营销推广": false, "": false, "新加的类别": true} {
		if got := r.wantsSMS(category); got != want {
			t.Errorf("wantsSMS(%q) = %v, want %v", category, got, want)
		}
	}
	r.SMS = false
	if r.wantsSMS("验证码") {
		t.Error("SMS pushed while turned off")
	}
}

// fakeBot records group-bot requests and answers with reply.
type fakeBot struct {
	mu    sync.Mutex
	reqs  []*http.Request
	body  []map[string]any
	reply string
}

func (f *fakeBot) server(t *testing.T) *httptest.Server {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.reqs, f.body = append(f.reqs, r), append(f.body, body)
		reply := f.reply
		f.mu.Unlock()
		io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestBotsSignAndRender(t *testing.T) {
	now := time.Unix(1700000000, 0)
	note := MessageNote(store.Message{Sender: "95588", Body: "验证码 *654321*，[勿告诉他人]", Category: "验证码", Complete: true, ReceivedAt: now}, "中国工商银行", "EC20", false)

	feishu := &fakeBot{reply: `{"code":0,"msg":"success"}`}
	srv := feishu.server(t)
	bots := Bots{Client: srv.Client(), now: func() time.Time { return now }}
	if err := bots.Send(context.Background(), KindFeishu, Webhook{Enabled: true, URL: srv.URL + "/open-apis/bot/v2/hook/abc", Secret: "s3"}, note); err != nil {
		t.Fatal(err)
	}
	b := feishu.body[0]
	if b["timestamp"] != "1700000000" || b["sign"] != sign("1700000000\ns3", "") || b["msg_type"] != "interactive" {
		t.Fatalf("feishu body = %v", b)
	}
	card, _ := json.Marshal(b["card"])
	for _, want := range []string{"验证码 **654321**", "＊654321＊", "［勿告诉他人］", `"template":"green"`, "EC20", "中国工商银行"} {
		if !strings.Contains(string(card), want) {
			t.Errorf("feishu card missing %q: %s", want, card)
		}
	}

	ding := &fakeBot{reply: `{"errcode":0,"errmsg":"ok"}`}
	srv = ding.server(t)
	bots.Client = srv.Client()
	if err := bots.Send(context.Background(), KindDingTalk, Webhook{Enabled: true, URL: srv.URL + "/robot/send?access_token=tok", Secret: "SEC1"}, note); err != nil {
		t.Fatal(err)
	}
	q := ding.reqs[0].URL.Query()
	if q.Get("access_token") != "tok" || q.Get("timestamp") != "1700000000000" || q.Get("sign") != sign("SEC1", "1700000000000\nSEC1") {
		t.Fatalf("dingtalk query = %v", q)
	}
	md := ding.body[0]["markdown"].(map[string]any)
	if !strings.Contains(md["text"].(string), "#### 💬 短信 · 验证码 · EC20") {
		t.Errorf("dingtalk markdown = %v", md)
	}

	wecom := &fakeBot{reply: `{"errcode":0,"errmsg":"ok"}`}
	srv = wecom.server(t)
	bots.Client = srv.Client()
	if err := bots.Send(context.Background(), KindWeCom, Webhook{Enabled: true, URL: srv.URL + "/cgi-bin/webhook/send?key=k1"}, note); err != nil {
		t.Fatal(err)
	}
	content := wecom.body[0]["markdown"].(map[string]any)["content"].(string)
	if !strings.Contains(content, "验证码 `654321`") || !strings.Contains(content, "`95588`") {
		t.Errorf("wecom markdown = %s", content)
	}
}

func TestBotErrorsExplainedAndScrubbed(t *testing.T) {
	ding := &fakeBot{reply: `{"errcode":310000,"errmsg":"keywords not in content"}`}
	srv := ding.server(t)
	bots := Bots{Client: srv.Client()}
	err := bots.Send(context.Background(), KindDingTalk, Webhook{URL: srv.URL + "/robot/send?access_token=tok"}, TestNote(KindDingTalk))
	if err == nil || !strings.Contains(err.Error(), "号小助") {
		t.Fatalf("dingtalk error = %v", err)
	}
	if _, retry := retryable(err, 0); retry {
		t.Error("keyword error retried")
	}
	ding.reply = `{"errcode":130101,"errmsg":"send too fast"}`
	err = bots.Send(context.Background(), KindDingTalk, Webhook{URL: srv.URL + "/robot/send?access_token=tok"}, TestNote(KindDingTalk))
	if wait, retry := retryable(err, 0); !retry || wait < time.Minute {
		t.Errorf("rate limit: wait %v, retry %v", wait, retry)
	}

	closed := httptest.NewServer(http.NotFoundHandler())
	u := closed.URL + "/cgi-bin/webhook/send?key=secret-key-123456"
	closed.Close()
	err = (&Bots{}).Send(context.Background(), KindWeCom, Webhook{URL: u}, TestNote(KindWeCom))
	if err == nil || strings.Contains(err.Error(), "secret-key-123456") {
		t.Fatalf("error leaks the webhook key: %v", err)
	}
}

func TestWebhookValidate(t *testing.T) {
	for _, c := range []struct {
		kind Kind
		url  string
		ok   bool
	}{
		{KindFeishu, "https://open.feishu.cn/open-apis/bot/v2/hook/abc", true},
		{KindFeishu, "https://open.feishu.cn/other", false},
		{KindFeishu, "http://open.feishu.cn/open-apis/bot/v2/hook/abc", false},
		{KindDingTalk, "https://oapi.dingtalk.com/robot/send?access_token=x", true},
		{KindDingTalk, "https://oapi.dingtalk.com/robot/send", false},
		{KindWeCom, "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=x", true},
		{KindWeCom, "https://qyapi.weixin.qq.com/cgi-bin/webhook/send", false},
		{KindWeCom, "https://relay.example.com/wecom", true},
	} {
		err := Webhook{Enabled: true, URL: c.url}.Validate(c.kind)
		if (err == nil) != c.ok {
			t.Errorf("%s %s: %v", c.kind, c.url, err)
		}
	}
}

func TestTruncateBytesKeepsRunes(t *testing.T) {
	s := strings.Repeat("短信", 10)
	got := truncateBytes(s, 10)
	if len(got) > 10 || !strings.HasPrefix(s, got) || strings.ContainsRune(got, '�') {
		t.Fatalf("truncateBytes = %q", got)
	}
}

func TestSummaryRules(t *testing.T) {
	r := DefaultRules()
	r.SkipCallCategories = []string{"推销广告", Unclassified}
	for _, c := range []struct {
		category, urgency string
		want              bool
	}{{"快递外卖", "低", true}, {"推销广告", "低", false}, {"推销广告", "高", true}, {"", "中", false}} {
		if got := r.wantsSummary(c.category, c.urgency); got != c.want {
			t.Errorf("wantsSummary(%q, %q) = %v", c.category, c.urgency, got)
		}
	}
	r.SkipUrgentToo = true
	if r.wantsSummary("推销广告", "高") {
		t.Error("urgent skipped category pushed with SkipUrgentToo")
	}
	r.Summaries = false
	if r.wantsSummary("快递外卖", "高") {
		t.Error("summary pushed while turned off")
	}
}

// A long body full of characters HTML escapes still fits Telegram's limit
// without splitting an entity.
func TestHTMLFitsTelegram(t *testing.T) {
	out := Note{Title: "短信", Body: strings.Repeat("&<", 3000)}.HTML()
	if n := utf8.RuneCountInString(out); n > maxMessage {
		t.Fatalf("%d characters", n)
	}
	if i := strings.LastIndex(out, "&"); i >= 0 && !strings.HasPrefix(out[i:], "&lt;") && !strings.HasPrefix(out[i:], "&amp;") {
		t.Fatalf("entity split: %q", out[max(0, i-10):])
	}
}
