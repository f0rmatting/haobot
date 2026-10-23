package settings

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/f0rmatting/haobot/internal/agent/assistant"
	"github.com/f0rmatting/haobot/internal/agent/classify"
	"github.com/f0rmatting/haobot/internal/agent/notify"
	"github.com/f0rmatting/haobot/internal/agent/secret"
	"github.com/f0rmatting/haobot/internal/agent/store"
)

func open(t *testing.T) (*Settings, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, err := secret.Load(filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	return New(st, box), st
}

func TestSIMPINSealedAndGatedByAutoUnlock(t *testing.T) {
	s, st := open(t)
	ctx := context.Background()
	pin := "4321"
	if err := s.SetSIM(ctx, "dev1", false, &pin); err != nil {
		t.Fatal(err)
	}
	if raw, _ := st.Setting(ctx, keySIMPIN+".dev1"); raw == "" || strings.Contains(raw, pin) {
		t.Fatalf("stored PIN %q is not sealed", raw)
	}
	if got, _ := s.SIMPIN(ctx, "dev1"); got != "" {
		t.Fatalf("PIN returned while auto unlock is off: %q", got)
	}
	if err := s.SetSIM(ctx, "dev1", true, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.SIMPIN(ctx, "dev1"); got != pin {
		t.Fatalf("SIMPIN = %q, want %q", got, pin)
	}
	sim, _ := s.SIM(ctx, "dev1")
	if !sim.HasPIN || !sim.AutoUnlock {
		t.Fatalf("SIM = %+v", sim)
	}
}

func TestLLMKeepsKeyWhenNotReplaced(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	cfg := classify.DefaultConfig()
	cfg.Enabled, cfg.BaseURL, cfg.Model = true, "https://api.example.com/v1", "m1"
	key := "sk-1"
	if err := s.SetLLM(ctx, cfg, &key); err != nil {
		t.Fatal(err)
	}
	cfg.Model = "m2"
	if err := s.SetLLM(ctx, cfg, nil); err != nil {
		t.Fatal(err)
	}
	got, err := s.LLM(ctx)
	if err != nil || got.Model != "m2" || got.APIKey != "sk-1" {
		t.Fatalf("LLM = %+v, %v", got, err)
	}
}

func TestLegacyGlobalPINStillApplies(t *testing.T) {
	s, st := open(t)
	ctx := context.Background()
	sealed, _ := s.box.Seal("1111")
	st.SetSetting(ctx, keySIM, `{"autoUnlock":true}`)
	st.SetSetting(ctx, keySIMPIN, sealed)
	if got, _ := s.SIMPIN(ctx, "any"); got != "1111" {
		t.Fatalf("legacy PIN = %q", got)
	}
	// Changing only the switch keeps the PIN, now stored for the device.
	if err := s.SetSIM(ctx, "dev2", true, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.SIMPIN(ctx, "dev2"); got != "1111" {
		t.Fatalf("device PIN after first save = %q", got)
	}
	other := "2222"
	s.SetSIM(ctx, "dev3", true, &other)
	if got, _ := s.SIMPIN(ctx, "dev3"); got != "2222" {
		t.Fatalf("dev3 PIN = %q", got)
	}
	if got, _ := s.SIMPIN(ctx, "dev2"); got != "1111" {
		t.Fatalf("dev2 PIN changed to %q", got)
	}
}

func TestAssistantOwnKey(t *testing.T) {
	s, st := open(t)
	ctx := context.Background()
	cfg, err := s.Assistant(ctx)
	if err != nil || cfg.Model != assistant.DefaultModel || cfg.APIKey != "" {
		t.Fatalf("default = %+v, %v", cfg, err)
	}
	cfg.Model, cfg.URL = "qwen3.5-omni-plus-realtime", "wss://ws-x.cn-beijing.maas.aliyuncs.com/api-ws/v1/realtime"
	key := "sk-beijing-1234"
	if err := s.SetAssistant(ctx, cfg, &key); err != nil {
		t.Fatal(err)
	}
	raw, _ := st.Setting(ctx, "assistant")
	sealed, _ := st.Setting(ctx, "assistant.api_key")
	if strings.Contains(raw, key) || strings.Contains(sealed, key) {
		t.Fatal("key stored in the clear")
	}
	// Saving without a key keeps it.
	if err := s.SetAssistant(ctx, cfg, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Assistant(ctx)
	if got.APIKey != key || got.Model != cfg.Model || got.URL != cfg.URL {
		t.Fatalf("reloaded = %+v", got)
	}
	cfg.URL = "wss://{WorkspaceId}.cn-beijing.maas.aliyuncs.com/api-ws/v1/realtime"
	if err := s.SetAssistant(ctx, cfg, nil); err == nil {
		t.Fatal("accepted a URL with the {WorkspaceId} placeholder")
	}
}

// Before there were several channels, the push rules were saved with the
// Telegram settings.
func TestNotifyRulesReadFromLegacyTelegram(t *testing.T) {
	s, st := open(t)
	ctx := context.Background()
	st.SetSetting(ctx, keyTelegram, `{"enabled":true,"chatId":"42","sms":true,"maskCodes":true,"skipCategories":["营销推广"],"ringing":true,"missed":false,"summaries":true}`)
	cfg, err := s.Notify(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Rules.MaskCodes || !cfg.Rules.Ringing || cfg.Rules.Missed || len(cfg.Rules.SkipCategories) != 1 || cfg.Telegram.ChatID != "42" {
		t.Fatalf("config = %+v", cfg)
	}
	// Saving Telegram keeps the rules, now in their own key.
	tg := cfg.Telegram
	tg.Enabled, tg.ChatID = false, "43"
	if err := s.SetTelegram(ctx, tg, nil); err != nil {
		t.Fatal(err)
	}
	cfg, _ = s.Notify(ctx)
	if !cfg.Rules.MaskCodes || cfg.Rules.Missed || cfg.Telegram.ChatID != "43" {
		t.Fatalf("after saving Telegram: %+v", cfg)
	}
	if raw, _ := st.Setting(ctx, keyNotifyRules); raw == "" {
		t.Fatal("rules not moved to their own key")
	}
}

func TestWebhookSealed(t *testing.T) {
	s, st := open(t)
	ctx := context.Background()
	u, secret := "https://oapi.dingtalk.com/robot/send?access_token=tok-123", "SECabc"
	if err := s.SetWebhook(ctx, notify.KindDingTalk, notify.Webhook{Enabled: true}, &u, &secret); err != nil {
		t.Fatal(err)
	}
	_, urlKey, _ := webhookKeys(notify.KindDingTalk)
	if raw, _ := st.Setting(ctx, urlKey); raw == "" || strings.Contains(raw, "tok-123") {
		t.Fatalf("webhook URL not sealed: %q", raw)
	}
	cfg, _ := s.Notify(ctx)
	if !cfg.DingTalk.Ready() || cfg.DingTalk.URL != u || cfg.DingTalk.Secret != secret || cfg.Feishu.Ready() {
		t.Fatalf("config = %+v", cfg)
	}
}
