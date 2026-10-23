package web

import (
	"cmp"
	"context"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/f0rmatting/haobot/internal/agent/assistant"
	"github.com/f0rmatting/haobot/internal/agent/notify"
)

type telegramJSON struct {
	notify.TelegramConfig
	HasToken  bool   `json:"hasToken"`
	TokenHint string `json:"tokenHint"`
}

// webhookJSON is a group-bot channel as the browser sees it: the URL,
// which carries the token, only as a hint.
type webhookJSON struct {
	Enabled   bool   `json:"enabled"`
	HasURL    bool   `json:"hasUrl"`
	URLHint   string `json:"urlHint"`
	HasSecret bool   `json:"hasSecret"`
}

type notifyJSON struct {
	Rules notify.Rules `json:"rules"`
	// CallCategories are the kinds of call the assistant's notes use.
	CallCategories []string     `json:"callCategories"`
	Telegram       telegramJSON `json:"telegram"`
	Feishu         webhookJSON  `json:"feishu"`
	DingTalk       webhookJSON  `json:"dingtalk"`
	WeCom          webhookJSON  `json:"wecom"`
}

// urlHint shows a webhook URL's host and the end of its token.
func urlHint(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return keyHint(raw)
	}
	token := u.Query().Get("access_token") + u.Query().Get("key")
	if token == "" {
		token = u.Path[strings.LastIndex(u.Path, "/")+1:]
	}
	return u.Host + " · " + keyHint(token)
}

func toWebhookJSON(w notify.Webhook) webhookJSON {
	return webhookJSON{Enabled: w.Enabled, HasURL: w.URL != "", URLHint: urlHint(w.URL), HasSecret: w.Secret != ""}
}

func toNotifyJSON(cfg notify.Config) notifyJSON {
	return notifyJSON{
		Rules:          cfg.Rules,
		CallCategories: assistant.CallCategories,
		Telegram:       telegramJSON{TelegramConfig: cfg.Telegram, HasToken: cfg.Telegram.Token != "", TokenHint: keyHint(cfg.Telegram.Token)},
		Feishu:         toWebhookJSON(cfg.Feishu),
		DingTalk:       toWebhookJSON(cfg.DingTalk),
		WeCom:          toWebhookJSON(cfg.WeCom),
	}
}

func (s *Server) writeNotify(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.Settings.Notify(r.Context())
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toNotifyJSON(cfg))
}

func (s *Server) putNotifyRules(w http.ResponseWriter, r *http.Request) {
	var rules notify.Rules
	if !decode(w, r, &rules) {
		return
	}
	if err := s.Settings.SetNotifyRules(r.Context(), rules); err != nil {
		s.serverError(w, err)
		return
	}
	s.writeNotify(w, r)
}

// telegramRequest is the Telegram form. Token nil keeps the stored token;
// "" removes it.
type telegramRequest struct {
	notify.TelegramConfig
	Token *string `json:"token"`
}

// withTelegramToken applies the form's token over the stored one.
func (s *Server) withTelegramToken(ctx context.Context, req telegramRequest) (notify.TelegramConfig, error) {
	cfg := req.TelegramConfig
	cfg.ChatID = strings.TrimSpace(cfg.ChatID)
	cfg.Proxy = strings.TrimSpace(cfg.Proxy)
	cfg.APIBase = strings.TrimSpace(cfg.APIBase)
	if req.Token != nil {
		cfg.Token = strings.TrimSpace(*req.Token)
		return cfg, nil
	}
	stored, err := s.Settings.Notify(ctx)
	if err != nil {
		return cfg, err
	}
	if stored.Telegram.Token != "" && telegramHost(cfg.APIBase) != telegramHost(stored.Telegram.APIBase) {
		return cfg, errRetypeKey
	}
	cfg.Token = stored.Telegram.Token
	return cfg, nil
}

// telegramHost is the Bot API host a base URL points at; empty is the
// official one.
func telegramHost(base string) string {
	return cmp.Or(hostOf(base), "api.telegram.org")
}

func (s *Server) putTelegram(w http.ResponseWriter, r *http.Request) {
	var req telegramRequest
	if !decode(w, r, &req) {
		return
	}
	cfg, err := s.withTelegramToken(r.Context(), req)
	if err != nil {
		s.keyError(w, err)
		return
	}
	if err := cfg.Validate(); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	var token *string
	if req.Token != nil {
		token = &cfg.Token
	}
	if err := s.Settings.SetTelegram(r.Context(), cfg, token); err != nil {
		s.serverError(w, err)
		return
	}
	s.noteChannel(r.Context(), notify.KindTelegram, cfg.Enabled)
	s.writeNotify(w, r)
}

func (s *Server) noteChannel(ctx context.Context, kind notify.Kind, enabled bool) {
	state := "已关闭"
	if enabled {
		state = "已开启"
	}
	s.Devices.Note(ctx, "info", kind.Label()+" 推送"+state)
}

// testTelegram sends a test message with the form's settings.
func (s *Server) testTelegram(w http.ResponseWriter, r *http.Request) {
	var req telegramRequest
	if !decode(w, r, &req) {
		return
	}
	cfg, err := s.withTelegramToken(r.Context(), req)
	if err != nil {
		s.keyError(w, err)
		return
	}
	cfg.Enabled = true
	if err := cfg.Validate(); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	start := time.Now()
	bot, err := s.Notifier.Telegram.Bot(ctx, cfg)
	if err == nil {
		err = s.Notifier.Send(ctx, notify.KindTelegram, notify.Config{Telegram: cfg}, notify.TestNote(notify.KindTelegram))
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"bot": bot, "latencyMs": time.Since(start).Milliseconds()})
}

// telegramChats lists chats that wrote to the bot, to pick the Chat ID.
func (s *Server) telegramChats(w http.ResponseWriter, r *http.Request) {
	var req telegramRequest
	if !decode(w, r, &req) {
		return
	}
	cfg, err := s.withTelegramToken(r.Context(), req)
	if err != nil {
		s.keyError(w, err)
		return
	}
	if cfg.Token == "" {
		writeError(w, http.StatusUnprocessableEntity, "请先填写 Bot Token")
		return
	}
	cfg.ChatID = ""
	if err := cfg.Validate(); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	bot, err := s.Notifier.Telegram.Bot(ctx, cfg)
	var chats []notify.Chat
	if err == nil {
		chats, err = s.Notifier.Telegram.Chats(ctx, cfg)
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if chats == nil {
		chats = []notify.Chat{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"bot": bot, "chats": chats})
}

// webhookRequest is a group-bot form. URL and Secret nil keep the stored
// values; "" removes them.
type webhookRequest struct {
	Enabled bool    `json:"enabled"`
	URL     *string `json:"url"`
	Secret  *string `json:"secret"`
}

// webhookKind reads the {kind} path value, writing an error when it is not
// a group-bot channel.
func webhookKind(w http.ResponseWriter, r *http.Request) (notify.Kind, bool) {
	kind := notify.Kind(r.PathValue("kind"))
	if kind == notify.KindTelegram || !slices.Contains(notify.Kinds, kind) {
		writeError(w, http.StatusNotFound, "不支持的推送方式")
		return "", false
	}
	return kind, true
}

// withWebhook applies the form over the stored channel.
func (s *Server) withWebhook(ctx context.Context, kind notify.Kind, req webhookRequest) (notify.Webhook, error) {
	stored, err := s.Settings.Notify(ctx)
	if err != nil {
		return notify.Webhook{}, err
	}
	cfg := stored.Webhook(kind)
	cfg.Enabled = req.Enabled
	if req.URL != nil {
		cfg.URL = strings.TrimSpace(*req.URL)
	}
	if req.Secret != nil {
		cfg.Secret = strings.TrimSpace(*req.Secret)
	}
	return cfg, nil
}

func (s *Server) putWebhook(w http.ResponseWriter, r *http.Request) {
	kind, ok := webhookKind(w, r)
	if !ok {
		return
	}
	var req webhookRequest
	if !decode(w, r, &req) {
		return
	}
	cfg, err := s.withWebhook(r.Context(), kind, req)
	if err != nil {
		s.serverError(w, err)
		return
	}
	if err := cfg.Validate(kind); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	var u, secret *string
	if req.URL != nil {
		u = &cfg.URL
	}
	if req.Secret != nil {
		secret = &cfg.Secret
	}
	if err := s.Settings.SetWebhook(r.Context(), kind, cfg, u, secret); err != nil {
		s.serverError(w, err)
		return
	}
	s.noteChannel(r.Context(), kind, cfg.Enabled)
	s.writeNotify(w, r)
}

func (s *Server) testWebhook(w http.ResponseWriter, r *http.Request) {
	kind, ok := webhookKind(w, r)
	if !ok {
		return
	}
	var req webhookRequest
	if !decode(w, r, &req) {
		return
	}
	cfg, err := s.withWebhook(r.Context(), kind, req)
	if err != nil {
		s.serverError(w, err)
		return
	}
	cfg.Enabled = true
	if err := cfg.Validate(kind); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	start := time.Now()
	var all notify.Config
	switch kind {
	case notify.KindFeishu:
		all.Feishu = cfg
	case notify.KindDingTalk:
		all.DingTalk = cfg
	case notify.KindWeCom:
		all.WeCom = cfg
	}
	if err := s.Notifier.Send(ctx, kind, all, notify.TestNote(kind)); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"latencyMs": time.Since(start).Milliseconds()})
}

func (s *Server) routeNotify(authed func(string, http.HandlerFunc)) {
	authed("GET /api/settings/notify", s.writeNotify)
	authed("PUT /api/settings/notify/rules", s.putNotifyRules)
	authed("PUT /api/settings/notify/telegram", s.putTelegram)
	authed("POST /api/settings/notify/telegram/test", s.testTelegram)
	authed("POST /api/settings/notify/telegram/chats", s.telegramChats)
	authed("PUT /api/settings/notify/{kind}", s.putWebhook)
	authed("POST /api/settings/notify/{kind}/test", s.testWebhook)
}
