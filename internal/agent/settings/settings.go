// Package settings persists what the admin UI configures: the SIM PIN used
// for automatic unlocking and the classification model. Secrets are sealed
// with a local key before they reach the database.
package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/f0rmatting/haobot/internal/agent/assistant"
	"github.com/f0rmatting/haobot/internal/agent/classify"
	"github.com/f0rmatting/haobot/internal/agent/notify"
	"github.com/f0rmatting/haobot/internal/agent/secret"
	"github.com/f0rmatting/haobot/internal/agent/store"
)

const (
	// keySIM and keySIMPIN hold the settings saved before devices had their
	// own; a device without settings of its own still uses them.
	keySIM             = "sim"
	keySIMPIN          = "sim.pin"
	keyLLM             = "llm"
	keyLLMAPIKey       = "llm.api_key"
	keyAssistant       = "assistant"
	keyAssistantAPIKey = "assistant.api_key"
	keyTelegram        = "notify.telegram"
	keyTelegramToken   = "notify.telegram.token"
	// keyNotifyRules holds what is pushed. Before there were several
	// channels the rules were part of keyTelegram, and are read from there
	// until saved.
	keyNotifyRules = "notify.rules"
)

// webhookKeys are where a group-bot channel's settings, URL and secret live.
func webhookKeys(kind notify.Kind) (settingsKey, urlKey, secretKey string) {
	base := "notify." + string(kind)
	return base, base + ".url", base + ".secret"
}

func simKeys(deviceID string) (settingsKey, pinKey string) {
	return keySIM + "." + deviceID, keySIMPIN + "." + deviceID
}

// SIM controls automatic unlocking.
type SIM struct {
	AutoUnlock bool `json:"autoUnlock"`
	HasPIN     bool `json:"hasPin"` // read-only: a PIN is stored
}

type Settings struct {
	store *store.Store
	box   *secret.Box

	mu   sync.Mutex
	llm  *classify.Config // cache; the worker reads it for every batch
	push *notify.Config   // cache; read for every notification
}

func New(st *store.Store, box *secret.Box) *Settings {
	return &Settings{store: st, box: box}
}

func (s *Settings) getJSON(ctx context.Context, key string, out any) (bool, error) {
	raw, err := s.store.Setting(ctx, key)
	if err != nil || raw == "" {
		return false, err
	}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return false, fmt.Errorf("设置 %s 已损坏：%w", key, err)
	}
	return true, nil
}

func (s *Settings) setJSON(ctx context.Context, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return s.store.SetSetting(ctx, key, string(raw))
}

func (s *Settings) secret(ctx context.Context, key string) (string, error) {
	sealed, err := s.store.Setting(ctx, key)
	if err != nil {
		return "", err
	}
	return s.box.Open(sealed)
}

func (s *Settings) setSecret(ctx context.Context, key, value string) error {
	sealed, err := s.box.Seal(value)
	if err != nil {
		return err
	}
	return s.store.SetSetting(ctx, key, sealed)
}

// SIM returns a device's unlock settings.
func (s *Settings) SIM(ctx context.Context, deviceID string) (SIM, error) {
	key, pinKey := s.simKeysFor(ctx, deviceID)
	var sim SIM
	if _, err := s.getJSON(ctx, key, &sim); err != nil {
		return sim, err
	}
	sealed, err := s.store.Setting(ctx, pinKey)
	sim.HasPIN = sealed != ""
	return sim, err
}

// simKeysFor picks the device's own keys, or the legacy global ones when the
// device has none yet.
func (s *Settings) simKeysFor(ctx context.Context, deviceID string) (string, string) {
	key, pinKey := simKeys(deviceID)
	if own, err := s.store.Setting(ctx, key); err == nil && own == "" {
		if legacy, err := s.store.Setting(ctx, keySIM); err == nil && legacy != "" {
			return keySIM, keySIMPIN
		}
	}
	return key, pinKey
}

// SetSIM saves a device's unlock settings. pin replaces the stored PIN unless
// it is nil; an empty pin removes it. The first save copies a legacy PIN over
// so the device keeps it.
func (s *Settings) SetSIM(ctx context.Context, deviceID string, autoUnlock bool, pin *string) error {
	key, pinKey := simKeys(deviceID)
	if pin == nil {
		if usedKey, usedPIN := s.simKeysFor(ctx, deviceID); usedKey == keySIM {
			legacy, err := s.store.Setting(ctx, usedPIN)
			if err != nil {
				return err
			}
			if err := s.store.SetSetting(ctx, pinKey, legacy); err != nil {
				return err
			}
		}
	} else if err := s.setSecret(ctx, pinKey, *pin); err != nil {
		return err
	}
	return s.setJSON(ctx, key, SIM{AutoUnlock: autoUnlock})
}

// SIMPIN returns a device's stored PIN when automatic unlocking is on.
func (s *Settings) SIMPIN(ctx context.Context, deviceID string) (string, error) {
	sim, err := s.SIM(ctx, deviceID)
	if err != nil || !sim.AutoUnlock {
		return "", err
	}
	_, pinKey := s.simKeysFor(ctx, deviceID)
	return s.secret(ctx, pinKey)
}

// LLM returns the classification configuration including the API key.
func (s *Settings) LLM(ctx context.Context) (classify.Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.llm != nil {
		return *s.llm, nil
	}
	cfg := classify.DefaultConfig()
	if _, err := s.getJSON(ctx, keyLLM, &cfg); err != nil {
		return cfg, err
	}
	if len(cfg.Categories) == 0 {
		cfg.Categories = classify.DefaultCategories()
	}
	key, err := s.secret(ctx, keyLLMAPIKey)
	if err != nil {
		return cfg, fmt.Errorf("读取 API Key：%w", err)
	}
	cfg.APIKey = key
	s.llm = &cfg
	return cfg, nil
}

// SetLLM saves the configuration. apiKey replaces the stored key unless it
// is nil; an empty key removes it.
func (s *Settings) SetLLM(ctx context.Context, cfg classify.Config, apiKey *string) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if apiKey != nil {
		if err := s.setSecret(ctx, keyLLMAPIKey, *apiKey); err != nil {
			return err
		}
	}
	if err := s.setJSON(ctx, keyLLM, cfg); err != nil {
		return err
	}
	s.llm = nil
	return nil
}

func deviceKey(deviceID, name string) string { return "device." + deviceID + "." + name }

// AutoRecover reports whether the agent may reconnect, reboot or USB-reset a
// device on its own. It is on unless turned off.
func (s *Settings) AutoRecover(ctx context.Context, deviceID string) bool {
	value, err := s.store.Setting(ctx, deviceKey(deviceID, "autorecover"))
	return err != nil || value != "off"
}

func (s *Settings) SetAutoRecover(ctx context.Context, deviceID string, on bool) error {
	value := "off"
	if on {
		value = "" // the default; deleting the row means on
	}
	return s.store.SetSetting(ctx, deviceKey(deviceID, "autorecover"), value)
}

// SelfTestTarget is the number the last self-test sent to.
func (s *Settings) SelfTestTarget(ctx context.Context, deviceID string) (string, error) {
	return s.store.Setting(ctx, deviceKey(deviceID, "selftest_to"))
}

func (s *Settings) SetSelfTestTarget(ctx context.Context, deviceID, number string) error {
	return s.store.SetSetting(ctx, deviceKey(deviceID, "selftest_to"), number)
}

// Assistant returns the call assistant configuration.
func (s *Settings) Assistant(ctx context.Context) (assistant.Config, error) {
	cfg := assistant.DefaultConfig()
	if _, err := s.getJSON(ctx, keyAssistant, &cfg); err != nil {
		return cfg, err
	}
	cfg.Normalize()
	key, err := s.secret(ctx, keyAssistantAPIKey)
	if err != nil {
		return cfg, fmt.Errorf("读取 AI 接听的 API Key：%w", err)
	}
	cfg.APIKey = key
	return cfg, nil
}

// SetAssistant saves the call assistant configuration. apiKey replaces the
// assistant's own key unless it is nil; an empty key removes it, so the
// classifier's is used again.
func (s *Settings) SetAssistant(ctx context.Context, cfg assistant.Config, apiKey *string) error {
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return err
	}
	if apiKey != nil {
		if err := s.setSecret(ctx, keyAssistantAPIKey, *apiKey); err != nil {
			return err
		}
	}
	return s.setJSON(ctx, keyAssistant, cfg)
}

// Notify returns the push configuration, with the rules and every
// channel's secrets.
func (s *Settings) Notify(ctx context.Context) (notify.Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.push != nil {
		return *s.push, nil
	}
	var cfg notify.Config
	cfg.Rules = notify.DefaultRules()
	found, err := s.getJSON(ctx, keyNotifyRules, &cfg.Rules)
	if err != nil {
		return cfg, err
	}
	if !found {
		if _, err := s.getJSON(ctx, keyTelegram, &cfg.Rules); err != nil {
			return cfg, err
		}
	}
	if cfg.Rules.SkipCategories == nil {
		cfg.Rules.SkipCategories = []string{}
	}
	if cfg.Rules.SkipCallCategories == nil {
		cfg.Rules.SkipCallCategories = []string{}
	}
	if _, err := s.getJSON(ctx, keyTelegram, &cfg.Telegram); err != nil {
		return cfg, err
	}
	if cfg.Telegram.Token, err = s.secret(ctx, keyTelegramToken); err != nil {
		return cfg, fmt.Errorf("读取 Telegram Bot Token：%w", err)
	}
	for _, kind := range notify.Kinds[1:] {
		key, urlKey, secretKey := webhookKeys(kind)
		var w notify.Webhook
		if _, err := s.getJSON(ctx, key, &w); err != nil {
			return cfg, err
		}
		if w.URL, err = s.secret(ctx, urlKey); err != nil {
			return cfg, fmt.Errorf("读取%s Webhook：%w", kind.Label(), err)
		}
		if w.Secret, err = s.secret(ctx, secretKey); err != nil {
			return cfg, fmt.Errorf("读取%s签名密钥：%w", kind.Label(), err)
		}
		switch kind {
		case notify.KindFeishu:
			cfg.Feishu = w
		case notify.KindDingTalk:
			cfg.DingTalk = w
		case notify.KindWeCom:
			cfg.WeCom = w
		}
	}
	s.push = &cfg
	return cfg, nil
}

// SetNotifyRules saves what is pushed.
func (s *Settings) SetNotifyRules(ctx context.Context, rules notify.Rules) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rules.SkipCategories == nil {
		rules.SkipCategories = []string{}
	}
	if rules.SkipCallCategories == nil {
		rules.SkipCallCategories = []string{}
	}
	if err := s.setJSON(ctx, keyNotifyRules, rules); err != nil {
		return err
	}
	s.push = nil
	return nil
}

// SetTelegram saves the Telegram channel. token replaces the stored one
// unless it is nil; an empty token removes it.
func (s *Settings) SetTelegram(ctx context.Context, cfg notify.TelegramConfig, token *string) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// The rules used to live here; keep them in their own key before this
	// overwrites them.
	if err := s.adoptLegacyRules(ctx); err != nil {
		return err
	}
	if token != nil {
		if err := s.setSecret(ctx, keyTelegramToken, *token); err != nil {
			return err
		}
	}
	if err := s.setJSON(ctx, keyTelegram, cfg); err != nil {
		return err
	}
	s.push = nil
	return nil
}

func (s *Settings) adoptLegacyRules(ctx context.Context) error {
	if raw, err := s.store.Setting(ctx, keyNotifyRules); err != nil || raw != "" {
		return err
	}
	rules := notify.DefaultRules()
	found, err := s.getJSON(ctx, keyTelegram, &rules)
	if err != nil || !found {
		return err
	}
	return s.setJSON(ctx, keyNotifyRules, rules)
}

// SetWebhook saves a group-bot channel. url and secret replace the stored
// values unless nil; empty removes them.
func (s *Settings) SetWebhook(ctx context.Context, kind notify.Kind, w notify.Webhook, url, secret *string) error {
	key, urlKey, secretKey := webhookKeys(kind)
	s.mu.Lock()
	defer s.mu.Unlock()
	if url != nil {
		if err := s.setSecret(ctx, urlKey, *url); err != nil {
			return err
		}
	}
	if secret != nil {
		if err := s.setSecret(ctx, secretKey, *secret); err != nil {
			return err
		}
	}
	if err := s.setJSON(ctx, key, struct {
		Enabled bool `json:"enabled"`
	}{w.Enabled}); err != nil {
		return err
	}
	s.push = nil
	return nil
}
