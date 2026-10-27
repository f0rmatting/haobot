package web

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/f0rmatting/haobot/internal/agent/classify"
	"github.com/f0rmatting/haobot/internal/agent/notify"
	"github.com/f0rmatting/haobot/internal/agent/secret"
	"github.com/f0rmatting/haobot/internal/agent/service"
	"github.com/f0rmatting/haobot/internal/agent/settings"
	"github.com/f0rmatting/haobot/internal/agent/store"
)

type testEnv struct {
	ts     *httptest.Server
	client *http.Client
	store  *store.Store
	recDir string
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "web.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	if err := SetPassword(ctx, st, "correct-horse"); err != nil {
		t.Fatal(err)
	}
	for i, body := range []string{"【银行】验证码 654321，5 分钟内有效", "<b>双十一</b> 满减，退订回T"} {
		if _, err := st.SaveSegment(ctx, store.Segment{
			PDUHash: string(rune('a' + i)), PDU: "p", Sender: "10690", SentAt: time.Now(), ReceivedAt: time.Now(),
			Total: 1, Seq: 1, Body: body, DeviceID: "866000000000001",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.AssemblePending(ctx, time.Now(), time.Hour, nil); err != nil {
		t.Fatal(err)
	}
	box, err := secret.Load(filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	conf := settings.New(st, box)
	devices := service.NewManager(service.Options{PollInterval: time.Minute}, nil, "serial", st, log)
	client := classify.NewClient()
	worker := classify.NewWorker(st, conf.LLM, client, log, nil, nil)
	recDir := filepath.Join(dir, "recordings")
	srv, err := New(Deps{Store: st, Devices: devices, Settings: conf, Worker: worker, Client: client, Log: log, RecordingDir: recDir, Notifier: &notify.Notifier{Config: conf.Notify, Store: st, Log: log}})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	return &testEnv{ts: ts, client: &http.Client{Jar: jar}, store: st, recDir: recDir}
}

// call sends a same-origin JSON request and decodes the JSON answer.
func (e *testEnv) call(t *testing.T, method, path string, body any, out any) int {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = strings.NewReader(string(raw))
	}
	req, _ := http.NewRequest(method, e.ts.URL+path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("%s %s: decode: %v", method, path, err)
		}
	}
	return resp.StatusCode
}

func (e *testEnv) login(t *testing.T) {
	t.Helper()
	if code := e.call(t, "POST", "/api/login", map[string]string{"password": "correct-horse"}, nil); code != http.StatusOK {
		t.Fatalf("login status %d", code)
	}
}

func TestLoginFlow(t *testing.T) {
	e := newTestEnv(t)
	if code := e.call(t, "GET", "/api/messages", nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("anonymous list status %d", code)
	}

	// Cross-site requests are rejected before the password is checked.
	req, _ := http.NewRequest("POST", e.ts.URL+"/api/login", strings.NewReader(`{"password":"correct-horse"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site login status %d", resp.StatusCode)
	}

	// A form post cannot carry JSON, so it is refused too.
	req, _ = http.NewRequest("POST", e.ts.URL+"/api/login", strings.NewReader("password=correct-horse"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err = e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("form login status %d", resp.StatusCode)
	}

	if code := e.call(t, "POST", "/api/login", map[string]string{"password": "wrong"}, nil); code != http.StatusUnauthorized {
		t.Fatalf("wrong password status %d", code)
	}
	e.login(t)
	var session struct{ Authenticated bool }
	e.call(t, "GET", "/api/session", nil, &session)
	if !session.Authenticated {
		t.Fatal("not authenticated after login")
	}
	e.call(t, "POST", "/api/logout", nil, nil)
	if code := e.call(t, "GET", "/api/status", nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("status after logout %d", code)
	}
}

func TestLoginLockout(t *testing.T) {
	e := newTestEnv(t)
	for i := 0; i < maxLoginFailures; i++ {
		e.call(t, "POST", "/api/login", map[string]string{"password": "wrong"}, nil)
	}
	if code := e.call(t, "POST", "/api/login", map[string]string{"password": "correct-horse"}, nil); code != http.StatusTooManyRequests {
		t.Fatalf("status after lockout %d", code)
	}
}

func TestMessagesListDeleteAndCode(t *testing.T) {
	e := newTestEnv(t)
	e.login(t)
	var list struct {
		Items []messageJSON
		Total int
	}
	e.call(t, "GET", "/api/messages", nil, &list)
	if list.Total != 2 || len(list.Items) != 2 {
		t.Fatalf("list = %+v", list)
	}
	var code *messageJSON
	for i := range list.Items {
		if list.Items[i].Code != "" {
			code = &list.Items[i]
		}
	}
	if code == nil || code.Code != "654321" {
		t.Fatalf("verification code not extracted: %+v", list.Items)
	}
	if status := e.call(t, "PATCH", "/api/messages/"+itoa(code.ID), map[string]string{"category": "其他"}, nil); status != http.StatusOK {
		t.Fatalf("set category status %d", status)
	}
	var history struct{ Items []classifyLogJSON }
	e.call(t, "GET", "/api/messages/"+itoa(code.ID)+"/classification", nil, &history)
	if len(history.Items) != 1 || history.Items[0].Source != "manual" || history.Items[0].Category != "其他" {
		t.Fatalf("classification history = %+v", history)
	}
	var recent struct {
		Items []classifyLogJSON
		Total int
	}
	e.call(t, "GET", "/api/classify/logs?source=manual", nil, &recent)
	if recent.Total != 1 || recent.Items[0].Body == "" {
		t.Fatalf("recent logs = %+v", recent)
	}
	if status := e.call(t, "DELETE", "/api/messages/"+itoa(code.ID), nil, nil); status != http.StatusOK {
		t.Fatalf("delete status %d", status)
	}
	if status := e.call(t, "GET", "/api/messages/"+itoa(code.ID), nil, nil); status != http.StatusNotFound {
		t.Fatalf("deleted message status %d", status)
	}
	var bulk struct{ Affected int }
	e.call(t, "POST", "/api/messages/bulk", map[string]any{"action": "read"}, &bulk)
	e.call(t, "GET", "/api/messages?unread=1", nil, &list)
	if list.Total != 0 {
		t.Fatalf("unread after mark all read: %d", list.Total)
	}
}

func TestSecretsAreNeverReturned(t *testing.T) {
	e := newTestEnv(t)
	e.login(t)
	cfg := map[string]any{
		"enabled": true, "provider": "openai", "baseUrl": "https://api.example.com/v1", "model": "m",
		"rulesFirst": true, "categories": classify.DefaultCategories(), "apiKey": "sk-very-secret-key-1234",
	}
	var saved map[string]any
	if code := e.call(t, "PUT", "/api/settings/classifier", cfg, &saved); code != http.StatusOK {
		t.Fatalf("save classifier status %d: %v", code, saved)
	}
	raw, _ := json.Marshal(saved)
	if strings.Contains(string(raw), "very-secret") {
		t.Fatalf("API key returned to the browser: %s", raw)
	}
	if saved["hasApiKey"] != true || saved["apiKeyHint"] != "••••1234" {
		t.Fatalf("key hint missing: %s", raw)
	}

	var sim map[string]any
	if _, err := e.store.SeeDevice(context.Background(), store.Device{ID: "866000000000001", Name: "EC20"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	e.call(t, "PUT", "/api/devices/866000000000001/sim-settings", map[string]any{"autoUnlock": true, "pin": "4321"}, &sim)
	raw, _ = json.Marshal(sim)
	if strings.Contains(string(raw), "4321") || sim["hasPin"] != true {
		t.Fatalf("SIM settings response: %s", raw)
	}
}

func TestDeviceOperationsWhileOffline(t *testing.T) {
	e := newTestEnv(t)
	e.login(t)
	if code := e.call(t, "POST", "/api/devices/nope/reboot", map[string]any{}, nil); code != http.StatusNotFound {
		t.Fatalf("reboot of unknown device status %d", code)
	}
	e.store.SeeDevice(context.Background(), store.Device{ID: "866000000000001", Name: "EC20"}, time.Now())
	var resp apiError
	if code := e.call(t, "POST", "/api/devices/866000000000001/reboot", map[string]any{}, &resp); code != http.StatusServiceUnavailable {
		t.Fatalf("reboot while offline status %d (%+v)", code, resp)
	}
	name := "家里"
	if code := e.call(t, "PATCH", "/api/devices/866000000000001", map[string]any{"name": name}, nil); code != http.StatusOK {
		t.Fatalf("rename status %d", code)
	}
	var status struct {
		Devices []struct {
			ID, Name string
			Online   bool
			Unread   int
		}
	}
	e.call(t, "GET", "/api/status", nil, &status)
	if len(status.Devices) != 1 || status.Devices[0].Name != name || status.Devices[0].Online || status.Devices[0].Unread != 2 {
		t.Fatalf("devices = %+v", status.Devices)
	}
	if code := e.call(t, "POST", "/api/messages/send", map[string]any{"deviceId": "866000000000001", "to": "13800000000", "text": "hi"}, nil); code != http.StatusServiceUnavailable {
		t.Fatalf("send while offline status %d", code)
	}
	if code := e.call(t, "POST", "/api/devices/866000000000001/selftest", map[string]any{"to": "13800000000"}, nil); code != http.StatusServiceUnavailable {
		t.Fatalf("self-test while offline status %d", code)
	}
	if code := e.call(t, "PUT", "/api/devices/866000000000001/recovery", map[string]any{"enabled": false}, nil); code != http.StatusOK {
		t.Fatalf("turn off recovery status %d", code)
	}
	var health struct {
		Devices []struct {
			Health struct {
				Level, Summary string
			}
		}
	}
	e.call(t, "GET", "/api/status", nil, &health)
	if h := health.Devices[0].Health; h.Level != "bad" || h.Summary != "离线" {
		t.Fatalf("offline health = %+v", h)
	}
}

func TestConversationsAPI(t *testing.T) {
	e := newTestEnv(t)
	e.login(t)
	var list struct {
		Items []conversationJSON
		Total int
	}
	e.call(t, "GET", "/api/conversations", nil, &list)
	if list.Total != 1 || list.Items[0].Peer != "10690" || list.Items[0].Unread != 2 || list.Items[0].Total != 2 {
		t.Fatalf("conversations = %+v", list)
	}
	var thread struct {
		Items []messageJSON
		Total int
	}
	e.call(t, "GET", "/api/conversations/messages?device=866000000000001&peer=10690", nil, &thread)
	if thread.Total != 2 || thread.Items[0].Direction != "in" {
		t.Fatalf("thread = %+v", thread)
	}
	var read struct{ Affected int }
	e.call(t, "POST", "/api/conversations/read", map[string]string{"deviceId": "866000000000001", "peer": "10690"}, &read)
	if read.Affected != 2 {
		t.Fatalf("marked %d read", read.Affected)
	}
	e.call(t, "POST", "/api/conversations/delete", map[string]string{"deviceId": "866000000000001", "peer": "10690"}, nil)
	e.call(t, "GET", "/api/conversations", nil, &list)
	if list.Total != 0 {
		t.Fatalf("conversation still listed: %+v", list)
	}
}

func TestPasswordChangeKeepsCallerSignedIn(t *testing.T) {
	e := newTestEnv(t)
	e.login(t)
	if code := e.call(t, "PUT", "/api/settings/password", map[string]string{"current": "nope", "new": "another-horse"}, nil); code != http.StatusUnprocessableEntity {
		t.Fatalf("wrong current password status %d", code)
	}
	if code := e.call(t, "PUT", "/api/settings/password", map[string]string{"current": "correct-horse", "new": "another-horse"}, nil); code != http.StatusOK {
		t.Fatalf("change password status %d", code)
	}
	if code := e.call(t, "GET", "/api/status", nil, nil); code != http.StatusOK {
		t.Fatalf("status after password change %d", code)
	}
}

func TestAppFallbackServesIndex(t *testing.T) {
	e := newTestEnv(t)
	resp, err := e.client.Get(e.ts.URL + "/messages/42")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("client route status %d type %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestCallsAPI(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	e.store.SeeDevice(ctx, store.Device{ID: "866000000000001", Name: "EC20"}, time.Now())
	c, _ := e.store.StartCall(ctx, store.CallRecord{DeviceID: "866000000000001", Number: "13800000000", Direction: store.DirectionIn, Status: store.CallRinging, StartedAt: time.Now()})
	e.store.EndCall(ctx, c.ID, store.CallMissed, time.Now())

	// Audio needs a session like everything else.
	resp, err := e.client.Get(e.ts.URL + "/api/devices/866000000000001/call/audio")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous audio status %d", resp.StatusCode)
	}

	e.login(t)
	var status struct{ MissedCalls int }
	e.call(t, "GET", "/api/status", nil, &status)
	if status.MissedCalls != 1 {
		t.Fatalf("missed calls = %d", status.MissedCalls)
	}
	var list struct {
		Items []callRecordJSON
		Total int
	}
	e.call(t, "GET", "/api/calls?missed=1", nil, &list)
	if list.Total != 1 || list.Items[0].Peer != "13800000000" || list.Items[0].Seen {
		t.Fatalf("missed list = %+v", list)
	}
	e.call(t, "POST", "/api/calls/seen", map[string]any{}, nil)
	e.call(t, "GET", "/api/status", nil, &status)
	if status.MissedCalls != 0 {
		t.Fatalf("missed calls after seen = %d", status.MissedCalls)
	}
	if code := e.call(t, "POST", "/api/devices/866000000000001/call/dial", map[string]string{"to": "13800000000"}, nil); code != http.StatusServiceUnavailable {
		t.Fatalf("dial while offline status %d", code)
	}
}

func TestCallRecordingServedAndDeleted(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	c, _ := e.store.StartCall(ctx, store.CallRecord{DeviceID: "d1", Number: "13800000000", Direction: store.DirectionIn, Status: store.CallRinging, StartedAt: time.Now()})
	wav := append([]byte("RIFF....WAVE"), make([]byte, 2000)...)
	os.MkdirAll(filepath.Join(e.recDir, "d1"), 0o700)
	os.WriteFile(filepath.Join(e.recDir, "d1", "call.wav"), wav, 0o600)
	e.store.SetCallRecording(ctx, c.ID, "d1/call.wav")
	// A path that tries to climb out stays inside the directory.
	other, _ := e.store.StartCall(ctx, store.CallRecord{DeviceID: "d1", Number: "1", Direction: store.DirectionIn, Status: store.CallRinging, StartedAt: time.Now()})
	e.store.SetCallRecording(ctx, other.ID, "../web.db")

	url := e.ts.URL + "/api/calls/" + itoa(c.ID) + "/recording"
	if resp, _ := e.client.Get(url); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous recording status %d", resp.StatusCode)
	}
	e.login(t)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Range", "bytes=0-11")
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent || string(body) != "RIFF....WAVE" || resp.Header.Get("Content-Type") != "audio/wav" {
		t.Fatalf("range request: %d %q %s", resp.StatusCode, body, resp.Header.Get("Content-Type"))
	}
	if resp, _ := e.client.Get(e.ts.URL + "/api/calls/" + itoa(other.ID) + "/recording"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("escaping path status %d", resp.StatusCode)
	}

	var list struct{ Items []callRecordJSON }
	e.call(t, "GET", "/api/calls", nil, &list)
	found := false
	for _, it := range list.Items {
		found = found || (it.ID == c.ID && it.HasRecording)
	}
	if !found {
		t.Fatal("hasRecording not reported")
	}
	e.call(t, "POST", "/api/calls/delete", map[string]any{"ids": []int64{c.ID}}, nil)
	if _, err := os.Stat(filepath.Join(e.recDir, "d1", "call.wav")); !os.IsNotExist(err) {
		t.Fatal("recording file not deleted with the call")
	}
}

func TestNotifySettings(t *testing.T) {
	e := newTestEnv(t)
	e.login(t)
	hook := "https://open.feishu.cn/open-apis/bot/v2/hook/0f1e2d3c-very-secret-7788"
	var got map[string]any
	if code := e.call(t, "PUT", "/api/settings/notify/feishu", map[string]any{"enabled": true, "url": hook, "secret": "sign-secret-99"}, &got); code != http.StatusOK {
		t.Fatalf("save feishu status %d: %v", code, got)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "very-secret") || strings.Contains(string(raw), "sign-secret") {
		t.Fatalf("webhook secrets returned to the browser: %s", raw)
	}
	feishu := got["feishu"].(map[string]any)
	if feishu["enabled"] != true || feishu["hasUrl"] != true || feishu["hasSecret"] != true || feishu["urlHint"] != "open.feishu.cn · ••••7788" {
		t.Fatalf("feishu = %v", feishu)
	}
	// Saving without url and secret keeps them.
	e.call(t, "PUT", "/api/settings/notify/feishu", map[string]any{"enabled": false}, &got)
	if f := got["feishu"].(map[string]any); f["hasUrl"] != true || f["enabled"] != false {
		t.Fatalf("feishu after toggle = %v", f)
	}
	if code := e.call(t, "PUT", "/api/settings/notify/feishu", map[string]any{"enabled": true, "url": "http://x"}, nil); code != http.StatusUnprocessableEntity {
		t.Fatalf("bad url status %d", code)
	}
	if code := e.call(t, "PUT", "/api/settings/notify/nope", map[string]any{}, nil); code != http.StatusNotFound {
		t.Fatalf("unknown channel status %d", code)
	}
	rules := map[string]any{"sms": true, "maskCodes": true, "skipCategories": []string{"营销推广"}, "ringing": true, "missed": true, "summaries": false}
	e.call(t, "PUT", "/api/settings/notify/rules", rules, &got)
	r := got["rules"].(map[string]any)
	if r["maskCodes"] != true || r["summaries"] != false || len(r["skipCategories"].([]any)) != 1 {
		t.Fatalf("rules = %v", r)
	}
}

func TestNeedsCallback(t *testing.T) {
	for note, want := range map[string]bool{"不需要": false, "无需回电": false, "无": false, "": false, "需要，今天下午回电": true, "无论多晚都请回电": true, "尽快回电": true} {
		if got := needsCallback(note); got != want {
			t.Errorf("needsCallback(%q) = %v", note, got)
		}
	}
}

// A saved key is only sent where it was saved for: pointing the form at
// another server needs the key typed again.
func TestSavedKeyStaysWithItsServer(t *testing.T) {
	e := newTestEnv(t)
	e.login(t)
	cfg := map[string]any{
		"enabled": true, "provider": "openai", "baseUrl": "https://api.example.com/v1", "model": "m",
		"categories": classify.DefaultCategories(), "apiKey": "sk-very-secret-key-1234",
	}
	if code := e.call(t, "PUT", "/api/settings/classifier", cfg, nil); code != http.StatusOK {
		t.Fatalf("save classifier status %d", code)
	}
	delete(cfg, "apiKey")
	cfg["baseUrl"] = "https://attacker.example.net/v1"
	var got map[string]any
	if code := e.call(t, "POST", "/api/settings/classifier/test", cfg, &got); code != http.StatusUnprocessableEntity {
		t.Fatalf("test against another host with the saved key: status %d %v", code, got)
	}
	if code := e.call(t, "PUT", "/api/settings/classifier", cfg, nil); code != http.StatusUnprocessableEntity {
		t.Fatalf("saving another host with the saved key: status %d", code)
	}
	// Same host, other settings: the saved key is used.
	cfg["baseUrl"], cfg["model"] = "https://api.example.com/v1", "m2"
	if code := e.call(t, "PUT", "/api/settings/classifier", cfg, nil); code != http.StatusOK {
		t.Fatalf("saving with the saved key: status %d", code)
	}

	tg := map[string]any{"enabled": true, "chatId": "42", "token": "123456789:AAEvery-secret-token-abcdefghijklmnopq"}
	if code := e.call(t, "PUT", "/api/settings/notify/telegram", tg, nil); code != http.StatusOK {
		t.Fatalf("save telegram status %d", code)
	}
	delete(tg, "token")
	tg["apiBase"] = "https://attacker.example.net"
	if code := e.call(t, "PUT", "/api/settings/notify/telegram", tg, nil); code != http.StatusUnprocessableEntity {
		t.Fatalf("telegram token to another API host: status %d", code)
	}
}

func TestContactsNameConversations(t *testing.T) {
	e := newTestEnv(t)
	e.login(t)
	var c contactJSON
	if code := e.call(t, "POST", "/api/contacts", map[string]string{"name": "银行通知", "number": "1069-0"}, &c); code != http.StatusOK || c.ID == 0 {
		t.Fatalf("add contact: %d %+v", code, c)
	}
	if code := e.call(t, "POST", "/api/contacts", map[string]string{"name": "重复", "number": "10690"}, nil); code != http.StatusConflict {
		t.Fatalf("duplicate number: %d", code)
	}
	if code := e.call(t, "POST", "/api/contacts", map[string]string{"name": " ", "number": "10086"}, nil); code != http.StatusBadRequest {
		t.Fatalf("empty name: %d", code)
	}
	var list struct{ Items []conversationJSON }
	e.call(t, "GET", "/api/conversations", nil, &list)
	if got := list.Items[0]; got.Name != "银行通知" || got.NameSource != "contact" || got.Last.Name != "银行通知" {
		t.Fatalf("conversation = %+v", got)
	}
	if code := e.call(t, "PUT", "/api/contacts/"+itoa(c.ID), map[string]string{"name": "工行", "number": "10690", "note": "n"}, &c); code != http.StatusOK || c.Name != "工行" {
		t.Fatalf("update: %d %+v", code, c)
	}
	if code := e.call(t, "DELETE", "/api/contacts/"+itoa(c.ID), nil, nil); code != http.StatusOK {
		t.Fatalf("delete: %d", code)
	}
	var after struct{ Items []conversationJSON }
	e.call(t, "GET", "/api/conversations", nil, &after)
	if after.Items[0].Name != "" {
		t.Fatalf("name kept after delete: %+v", after.Items[0])
	}
	var yp struct {
		Source string
		Orgs   []struct{ Name string }
	}
	if e.call(t, "GET", "/api/yellowpages", nil, &yp); len(yp.Orgs) < 100 || !strings.Contains(yp.Source, "vCards") {
		t.Fatalf("yellow pages: %d orgs, source %q", len(yp.Orgs), yp.Source)
	}
}

func TestContactAvatarAndLogos(t *testing.T) {
	e := newTestEnv(t)
	e.login(t)
	// A 1x1 PNG.
	png := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII="
	var c contactJSON
	if code := e.call(t, "POST", "/api/contacts", map[string]any{"name": "妈妈", "number": "13800138000", "avatar": png}, &c); code != http.StatusOK || c.Avatar == "" {
		t.Fatalf("add with avatar: %d %+v", code, c)
	}
	resp, err := e.client.Get(e.ts.URL + c.Avatar)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("avatar: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	// Leaving avatar out keeps it; "" removes it.
	var kept contactJSON
	e.call(t, "PUT", "/api/contacts/"+itoa(c.ID), map[string]any{"name": "老妈", "number": "13800138000"}, &kept)
	if kept.Avatar == "" {
		t.Fatalf("avatar dropped on edit: %+v", kept)
	}
	var removed contactJSON
	e.call(t, "PUT", "/api/contacts/"+itoa(c.ID), map[string]any{"name": "老妈", "number": "13800138000", "avatar": ""}, &removed)
	if removed.Avatar != "" {
		t.Fatalf("avatar kept after removal: %+v", removed)
	}
	if code := e.call(t, "PUT", "/api/contacts/"+itoa(c.ID), map[string]any{"name": "x", "number": "13800138000", "avatar": "data:image/png;base64,PHN2Zz48L3N2Zz4="}, nil); code != http.StatusBadRequest {
		t.Fatalf("svg avatar: %d", code)
	}

	// The test messages come from 10690 and are signed 【银行】, which is
	// no organisation; a signed one gets its logo.
	ctx := context.Background()
	e.store.SaveSegment(ctx, store.Segment{PDUHash: "z", PDU: "p", Sender: "1069088", SentAt: time.Now(), ReceivedAt: time.Now(), Total: 1, Seq: 1, Body: "【招商银行】您的验证码是 112233", DeviceID: "866000000000001"})
	e.store.AssemblePending(ctx, time.Now(), time.Hour, nil)
	var list struct{ Items []conversationJSON }
	e.call(t, "GET", "/api/conversations", nil, &list)
	var logo string
	for _, conv := range list.Items {
		if conv.Peer == "1069088" {
			logo = conv.Avatar
		} else if conv.Avatar != "" {
			t.Errorf("unsigned conversation has avatar %q", conv.Avatar)
		}
	}
	if !strings.HasPrefix(logo, "/api/yellowpages/logos/") {
		t.Fatalf("signed conversation avatar = %q", logo)
	}
	resp, err = e.client.Get(e.ts.URL + logo)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("logo: %d", resp.StatusCode)
	}
	if code := e.call(t, "GET", "/api/yellowpages/logos/..%2fyellowpages.json", nil, nil); code != http.StatusNotFound {
		t.Fatalf("logo path escape: %d", code)
	}
}

// get fetches path and returns its body.
func (e *testEnv) get(t *testing.T, path string) (int, string) {
	t.Helper()
	resp, err := e.client.Get(e.ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestContactsImportExport(t *testing.T) {
	e := newTestEnv(t)
	e.login(t)
	e.call(t, "POST", "/api/contacts", map[string]any{"name": "妈妈", "number": "13800138000"}, nil)
	vcf := "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:张三\r\nTEL;TYPE=CELL:139 0000 0000\r\nTEL;TYPE=WORK:010-12345678\r\nEND:VCARD\r\n" +
		"BEGIN:VCARD\r\nVERSION:3.0\r\nFN:母亲\r\nTEL:+86 138 0013 8000\r\nNOTE:家里\r\nEND:VCARD\r\n"
	var res importResult
	if code := e.call(t, "POST", "/api/contacts/import", map[string]any{"vcf": vcf}, &res); code != http.StatusOK || res != (importResult{Added: 2, Skipped: 1}) {
		t.Fatalf("import: %d %+v", code, res)
	}
	e.call(t, "POST", "/api/contacts/import", map[string]any{"vcf": vcf, "overwrite": true}, &res)
	if res != (importResult{Updated: 3}) {
		t.Fatalf("overwrite: %+v", res)
	}
	if code := e.call(t, "POST", "/api/contacts/import", map[string]any{"vcf": "hello"}, nil); code != http.StatusBadRequest {
		t.Fatalf("not a vcard: %d", code)
	}
	code, body := e.get(t, "/api/contacts/export")
	if code != http.StatusOK || !strings.Contains(body, "FN:张三 手机") || !strings.Contains(body, "NOTE:家里") || !strings.Contains(body, "FN:母亲") {
		t.Fatalf("export: %d %s", code, body)
	}
}

func TestYellowPagesEdits(t *testing.T) {
	e := newTestEnv(t)
	e.login(t)
	type pages struct{ Orgs, Hidden []orgJSON }
	find := func(p pages, name string) *orgJSON {
		for i := range p.Orgs {
			if p.Orgs[i].Name == name {
				return &p.Orgs[i]
			}
		}
		return nil
	}
	// Edit a built-in organisation, hide one, add one of our own.
	numbers := []map[string]string{{"number": "95555"}, {"number": "1069210395555", "label": "营销"}}
	if code := e.call(t, "POST", "/api/yellowpages/orgs", map[string]any{"builtin": "招商银行", "name": "招商银行", "category": "金融银行", "numbers": numbers}, nil); code != http.StatusOK {
		t.Fatalf("edit: %d", code)
	}
	e.call(t, "POST", "/api/yellowpages/hide", map[string]string{"builtin": "中国工商银行"}, nil)
	var own struct{ ID int64 }
	e.call(t, "POST", "/api/yellowpages/orgs", map[string]any{"name": "小区物业", "numbers": []map[string]string{{"number": "021-12345678"}}}, &own)
	if code := e.call(t, "POST", "/api/yellowpages/orgs", map[string]any{"builtin": "没有这家", "name": "x"}, nil); code != http.StatusNotFound {
		t.Fatalf("unknown built-in: %d", code)
	}
	var p pages
	e.call(t, "GET", "/api/yellowpages", nil, &p)
	if o := find(p, "招商银行"); o == nil || !o.Edited || len(o.Numbers) != 2 || o.Logo == "" {
		t.Fatalf("edited = %+v", o)
	}
	if find(p, "中国工商银行") != nil || len(p.Hidden) != 1 {
		t.Fatalf("hidden: %+v", p.Hidden)
	}
	if o := find(p, "小区物业"); o == nil || o.Category != "其它" || o.Builtin != "" {
		t.Fatalf("own = %+v", o)
	}

	// Export, put everything back, import: the changes return.
	code, file := e.get(t, "/api/yellowpages/export")
	if code != http.StatusOK || !strings.Contains(file, yellowFormat) {
		t.Fatalf("export: %d", code)
	}
	e.call(t, "DELETE", "/api/yellowpages/orgs/"+itoa(find(p, "招商银行").ID), nil, nil)
	e.call(t, "DELETE", "/api/yellowpages/orgs/"+itoa(own.ID), nil, nil)
	e.call(t, "POST", "/api/yellowpages/unhide", nil, nil)
	e.call(t, "GET", "/api/yellowpages", nil, &p)
	if o := find(p, "招商银行"); o.Edited || find(p, "小区物业") != nil || len(p.Hidden) != 0 {
		t.Fatalf("not reset: %+v", o)
	}
	var raw map[string]any
	json.Unmarshal([]byte(file), &raw)
	var res importResult
	if code := e.call(t, "POST", "/api/yellowpages/import", raw, &res); code != http.StatusOK || res.Added != 3 {
		t.Fatalf("import: %d %+v", code, res)
	}
	e.call(t, "GET", "/api/yellowpages", nil, &p)
	if o := find(p, "招商银行"); !o.Edited || find(p, "小区物业") == nil || len(p.Hidden) != 1 {
		t.Fatalf("after import: %+v %+v", o, p.Hidden)
	}
	// Importing again changes nothing.
	e.call(t, "POST", "/api/yellowpages/import", raw, &res)
	if res.Added != 0 || res.Updated != 3 {
		t.Fatalf("again: %+v", res)
	}
}
