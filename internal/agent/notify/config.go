package notify

import (
	"errors"
	"net/url"
	"slices"
	"strings"
)

// Unclassified stands for SMS pushed before (or without) a category in
// Rules.SkipCategories.
const Unclassified = "_unclassified"

// Rules decide what is pushed; they apply to every channel.
type Rules struct {
	SMS       bool `json:"sms"`       // push new SMS
	MaskCodes bool `json:"maskCodes"` // hide verification codes
	// SkipCategories are SMS categories not pushed, so a category added
	// later is pushed until the user unticks it. Unclassified covers SMS
	// that have no category when they are pushed.
	SkipCategories []string `json:"skipCategories"`
	Ringing        bool     `json:"ringing"`   // a call is ringing
	Missed         bool     `json:"missed"`    // missed calls
	Summaries      bool     `json:"summaries"` // the assistant's call notes
	// SkipCallCategories are kinds of call (see assistant.CallCategories)
	// whose notes are not pushed; Unclassified covers notes without one,
	// such as a failed summary.
	SkipCallCategories []string `json:"skipCallCategories"`
	// SkipUrgentToo applies SkipCallCategories to urgent calls as well;
	// by default a call the assistant rates urgent is always pushed.
	SkipUrgentToo bool `json:"skipUrgentToo"`
}

// DefaultRules is used until the user saves rules.
func DefaultRules() Rules {
	return Rules{SMS: true, Missed: true, Summaries: true, SkipCategories: []string{}, SkipCallCategories: []string{}}
}

// wantsSummary reports whether the assistant's note on a call of this
// category and urgency is pushed.
func (r Rules) wantsSummary(category, urgency string) bool {
	if !r.Summaries {
		return false
	}
	if urgency == "高" && !r.SkipUrgentToo {
		return true
	}
	if category == "" {
		category = Unclassified
	}
	return !slices.Contains(r.SkipCallCategories, category)
}

// wantsSMS reports whether an SMS of this category is pushed.
func (r Rules) wantsSMS(category string) bool {
	if !r.SMS {
		return false
	}
	if category == "" {
		category = Unclassified
	}
	return !slices.Contains(r.SkipCategories, category)
}

// Kind names a push channel.
type Kind string

const (
	KindTelegram Kind = "telegram"
	KindFeishu   Kind = "feishu"
	KindDingTalk Kind = "dingtalk"
	KindWeCom    Kind = "wecom"
)

// Kinds lists every channel, in the order the UI shows them.
var Kinds = []Kind{KindTelegram, KindFeishu, KindDingTalk, KindWeCom}

// Label is the channel's name in messages and logs.
func (k Kind) Label() string {
	switch k {
	case KindTelegram:
		return "Telegram"
	case KindFeishu:
		return "飞书"
	case KindDingTalk:
		return "钉钉"
	case KindWeCom:
		return "企业微信"
	}
	return string(k)
}

// Webhook configures a group-bot channel (飞书, 钉钉, 企业微信): its
// webhook URL carries the access token, so URL and Secret are stored sealed
// and never sent to the browser.
type Webhook struct {
	Enabled bool   `json:"enabled"`
	URL     string `json:"-"`
	// Secret signs requests when the bot's 签名校验 is on (飞书, 钉钉).
	Secret string `json:"-"`
}

// Ready reports whether messages can be sent.
func (w Webhook) Ready() bool { return w.Enabled && w.URL != "" }

// webhookHosts are the official hosts; others are accepted (private
// deployments, proxies) as long as they use https.
var webhookHosts = map[Kind][]string{
	KindFeishu:   {"open.feishu.cn", "open.larksuite.com"},
	KindDingTalk: {"oapi.dingtalk.com"},
	KindWeCom:    {"qyapi.weixin.qq.com"},
}

// Validate checks a webhook configuration before it is saved or tested.
func (w Webhook) Validate(kind Kind) error {
	if w.URL == "" {
		if w.Enabled {
			return errors.New("请填写 Webhook 地址")
		}
		return nil
	}
	u, err := url.Parse(w.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return errors.New("Webhook 地址需要以 https:// 开头，从群机器人的设置里复制")
	}
	switch kind {
	case KindDingTalk:
		if slices.Contains(webhookHosts[kind], u.Host) && u.Query().Get("access_token") == "" {
			return errors.New("钉钉 Webhook 地址缺少 access_token，请完整复制")
		}
	case KindWeCom:
		if slices.Contains(webhookHosts[kind], u.Host) && u.Query().Get("key") == "" {
			return errors.New("企业微信 Webhook 地址缺少 key，请完整复制")
		}
	case KindFeishu:
		if slices.Contains(webhookHosts[kind], u.Host) && !strings.Contains(u.Path, "/bot/v2/hook/") {
			return errors.New("飞书 Webhook 地址应形如 https://open.feishu.cn/open-apis/bot/v2/hook/…")
		}
	}
	return nil
}

// Config is the whole push setup: the rules and every channel.
type Config struct {
	Rules    Rules
	Telegram TelegramConfig
	Feishu   Webhook
	DingTalk Webhook
	WeCom    Webhook
}

// Webhook returns a group-bot channel's configuration.
func (c Config) Webhook(kind Kind) Webhook {
	switch kind {
	case KindFeishu:
		return c.Feishu
	case KindDingTalk:
		return c.DingTalk
	case KindWeCom:
		return c.WeCom
	}
	return Webhook{}
}

// Ready reports whether a channel can send.
func (c Config) Ready(kind Kind) bool {
	if kind == KindTelegram {
		return c.Telegram.Ready()
	}
	return c.Webhook(kind).Ready()
}

// Any reports whether some channel can send.
func (c Config) Any() bool {
	return slices.ContainsFunc(Kinds, c.Ready)
}
