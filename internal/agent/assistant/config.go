// Package assistant answers phone calls for the owner. A realtime speech
// model (Qwen-Omni-Realtime on Alibaba Cloud Model Studio) talks with the
// caller to find out who is calling and why; afterwards a text model turns
// the conversation into a short note.
package assistant

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/f0rmatting/haobot/internal/agent/classify"
)

// Defaults for a configuration that was never saved.
const (
	DefaultModel    = "qwen3.8-omni-flash-realtime"
	DefaultVoice    = "Tina"
	DefaultGreeting = "喂，您好，请问是哪位呀？"
	defaultDelay    = 10
	defaultMaxSec   = 180
	defaultRate     = 1.1
	defaultDaily    = 20
	defaultPerPeer  = 3
	defaultTurns    = 12
	defaultKeepDays = 90
)

// Config is the assistant setup edited in the admin UI. The API key and the
// workspace come from the classifier configuration.
type Config struct {
	Enabled bool `json:"enabled"`
	// RingDelaySec lets a call ring that long first, so the owner can still
	// answer in the browser.
	RingDelaySec int    `json:"ringDelaySec"`
	Greeting     string `json:"greeting"`
	// Instructions is what the owner wants the assistant to know, e.g.
	// "快递请放门口".
	Instructions   string `json:"instructions"`
	Model          string `json:"model"`
	Voice          string `json:"voice"`
	MaxDurationSec int    `json:"maxDurationSec"`
	// URL is the realtime WebSocket endpoint; empty derives it from the
	// classifier's Base URL.
	URL string `json:"url"`
	// OwnerName is how the assistant refers to the owner ("张先生", "王姐");
	// empty avoids naming them.
	OwnerName string `json:"ownerName"`
	// Style is the manner of speaking: StyleWarm, StyleFormal or StyleBrief.
	Style string `json:"style"`
	// DiscloseAI has the assistant say it is an AI right after the
	// greeting, where the law or the owner wants callers told. Otherwise it
	// only says so when sincerely asked.
	DiscloseAI bool `json:"discloseAI"`

	// SpeechRate speeds the assistant's voice up (1 = as the model speaks).
	SpeechRate float64 `json:"speechRate"`

	// Limits against callers who keep the assistant talking to run up the
	// bill: calls per day, calls per number per day, model replies per call.
	DailyLimit   int `json:"dailyLimit"`
	PerCallerMax int `json:"perCallerMax"`
	MaxTurns     int `json:"maxTurns"`

	// Record keeps a recording of RecordAssistant calls, every call
	// (RecordAll) or none; recordings older than KeepDays are deleted.
	Record   string `json:"record"`
	KeepDays int    `json:"keepDays"`

	// APIKey is the assistant's own key; empty uses the classifier's. It is
	// stored sealed, never in this JSON.
	APIKey string `json:"-"`
}

// Models lists the realtime speech models offered in the UI.
var Models = []string{"qwen3.8-omni-flash-realtime", "qwen3.5-omni-plus-realtime", "qwen3.5-omni-flash-realtime", "qwen3-omni-flash-realtime"}

// Key returns the API key to use: the assistant's own, else the classifier's.
func (c Config) Key(llm classify.Config) string {
	if c.APIKey != "" {
		return c.APIKey
	}
	return llm.APIKey
}

// turnDetection picks the voice activity detection: semantic VAD ignores
// "嗯" and background noise, but only Qwen3.5 and later have it.
func (c Config) turnDetection() map[string]any {
	if strings.HasPrefix(c.Model, "qwen3-omni") {
		return map[string]any{"type": "server_vad", "silence_duration_ms": 800}
	}
	return map[string]any{"type": "semantic_vad", "silence_duration_ms": 700}
}

// DefaultConfig is used until the user saves a configuration.
func DefaultConfig() Config {
	return Config{
		RingDelaySec: defaultDelay, Greeting: DefaultGreeting, Model: DefaultModel,
		Voice: DefaultVoice, MaxDurationSec: defaultMaxSec, SpeechRate: defaultRate,
		DailyLimit: defaultDaily, PerCallerMax: defaultPerPeer, MaxTurns: defaultTurns,
		Record: RecordAssistant, KeepDays: defaultKeepDays, Style: StyleWarm,
	}
}

// Speaking styles.
const (
	StyleWarm   = "warm"   // friendly, like a thoughtful assistant
	StyleFormal = "formal" // polite and businesslike
	StyleBrief  = "brief"  // courteous but short
)

// Recording choices.
const (
	RecordOff       = "off"
	RecordAssistant = "assistant"
	RecordAll       = "all"
)

// ShouldRecord reports whether a call is kept as a recording.
func (c Config) ShouldRecord(byAssistant bool) bool {
	return c.Record == RecordAll || (c.Record == RecordAssistant && byAssistant)
}

// Normalize fills in defaults for empty fields.
func (c *Config) Normalize() {
	d := DefaultConfig()
	if strings.TrimSpace(c.Greeting) == "" {
		c.Greeting = d.Greeting
	}
	if c.Model == "" {
		c.Model = d.Model
	}
	if c.Voice == "" {
		c.Voice = d.Voice
	}
	if c.MaxDurationSec == 0 {
		c.MaxDurationSec = d.MaxDurationSec
	}
	// Settings saved before these existed get the defaults.
	if c.SpeechRate == 0 {
		c.SpeechRate = d.SpeechRate
	}
	if c.DailyLimit == 0 {
		c.DailyLimit = d.DailyLimit
	}
	if c.PerCallerMax == 0 {
		c.PerCallerMax = d.PerCallerMax
	}
	if c.MaxTurns == 0 {
		c.MaxTurns = d.MaxTurns
	}
	if c.Record == "" {
		c.Record = d.Record
	}
	if c.Style == "" {
		c.Style = StyleWarm
	}
	c.OwnerName = strings.TrimSpace(c.OwnerName)
	if c.KeepDays == 0 {
		c.KeepDays = d.KeepDays
	}
}

// Validate checks a configuration before it is saved.
func (c Config) Validate() error {
	switch {
	case c.RingDelaySec < 0 || c.RingDelaySec > 60:
		return errors.New("响铃等待时间需要在 0 到 60 秒之间")
	case c.MaxDurationSec < 30 || c.MaxDurationSec > 600:
		return errors.New("单次通话时长需要在 30 到 600 秒之间")
	case c.SpeechRate < 0.8 || c.SpeechRate > 1.5:
		return errors.New("语速需要在 0.8 到 1.5 倍之间")
	case c.DailyLimit < 1 || c.DailyLimit > 500:
		return errors.New("每天最多接听次数需要在 1 到 500 之间")
	case c.PerCallerMax < 1 || c.PerCallerMax > 50:
		return errors.New("同一号码每天最多接听次数需要在 1 到 50 之间")
	case c.MaxTurns < 3 || c.MaxTurns > 50:
		return errors.New("每通电话最多回复次数需要在 3 到 50 之间")
	case c.Style != StyleWarm && c.Style != StyleFormal && c.Style != StyleBrief:
		return errors.New("未知的说话风格")
	case len([]rune(c.OwnerName)) > 20:
		return errors.New("称呼不能超过 20 个字")
	case c.Record != RecordOff && c.Record != RecordAssistant && c.Record != RecordAll:
		return errors.New("未知的录音选项")
	case c.KeepDays < 1 || c.KeepDays > 3650:
		return errors.New("录音保留天数需要在 1 到 3650 之间")
	case len([]rune(c.Greeting)) > 200:
		return errors.New("开场白不能超过 200 字")
	case len([]rune(c.Instructions)) > 2000:
		return errors.New("补充说明不能超过 2000 字")
	}
	if strings.TrimSpace(c.Model) == "" || strings.ContainsAny(c.Model, " /?&") {
		return errors.New("请填写正确的实时语音模型名称")
	}
	// Speech synthesis, recognition and translation models share the
	// realtime endpoint but cannot hold a conversation.
	if !strings.Contains(c.Model, "omni") || !strings.Contains(c.Model, "realtime") {
		return fmt.Errorf("%s 不是实时对话模型。请选择 Omni Realtime 系列，例如 %s（语音合成 TTS、语音识别 ASR、同传 LiveTranslate 模型不能接电话）", c.Model, DefaultModel)
	}
	if c.URL != "" {
		if strings.Contains(c.URL, "{") {
			return errors.New("请把实时接口地址里的 {WorkspaceId} 换成你的业务空间 ID")
		}
		u, err := url.Parse(c.URL)
		if err != nil || (u.Scheme != "wss" && u.Scheme != "ws") || u.Host == "" {
			return errors.New("实时接口地址需要以 wss:// 开头")
		}
	}
	return nil
}

// RingDelay is how long a call rings before the assistant answers.
func (c Config) RingDelay() time.Duration { return time.Duration(c.RingDelaySec) * time.Second }

// Endpoint returns the realtime WebSocket URL including the model. Without
// an explicit URL it uses the host of the classifier's OpenAI-compatible
// Base URL, e.g. https://ws-xxx.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1
// becomes wss://ws-xxx.ap-southeast-1.maas.aliyuncs.com/api-ws/v1/realtime.
func (c Config) Endpoint(llm classify.Config) (string, error) {
	raw := c.URL
	if raw == "" {
		base, err := url.Parse(llm.BaseURL)
		if err != nil || base.Host == "" || llm.Provider != classify.ProviderOpenAI {
			return "", errors.New("请先在“短信分类”里填写百炼的 Base URL（OpenAI 兼容接口），或在这里填写实时接口地址")
		}
		raw = "wss://" + base.Host + "/api-ws/v1/realtime"
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	q := u.Query()
	if q.Get("model") == "" {
		q.Set("model", c.Model)
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}
