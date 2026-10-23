package classify

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/f0rmatting/haobot/internal/agent/store"
)

func TestWorkerLogsEveryDecision(t *testing.T) {
	var down atomic.Bool
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if down.Load() {
			http.Error(w, `{"error":{"message":"quota exceeded"}}`, http.StatusTooManyRequests)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": `{"category":"快递物流","confidence":0.88}`}}}})
	}))
	defer api.Close()

	st, err := store.Open(filepath.Join(t.TempDir(), "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx, now := context.Background(), time.Now()
	for i, body := range []string{"您的快递到了，取件码 5-1-2001", "验证码 123456，5 分钟有效"} {
		st.SaveSegment(ctx, store.Segment{PDUHash: string(rune('a' + i)), PDU: "p", Sender: "1069", SentAt: now, ReceivedAt: now, Total: 1, Seq: 1, Body: body})
	}
	st.AssemblePending(ctx, now, time.Hour, nil)

	cfg := DefaultConfig()
	cfg.Enabled, cfg.BaseURL, cfg.Model = true, api.URL+"/v1", "m1"
	w := NewWorker(st, func(context.Context) (Config, error) { return cfg, nil }, NewClient(), slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil)

	if !w.ModelUp() {
		t.Fatal("model down before any request")
	}
	down.Store(true)
	if _, err := w.batch(ctx); err == nil {
		t.Fatal("batch succeeded while the model is down")
	}
	if w.ModelUp() {
		t.Fatal("ModelUp after a failed request; new SMS would wait for nothing")
	}
	down.Store(false)
	if n, err := w.batch(ctx); err != nil || n != 2 {
		t.Fatalf("batch = %d, %v", n, err)
	}
	if !w.ModelUp() {
		t.Fatal("model still down after it answered")
	}

	logs, total, err := st.RecentClassifyLogs(ctx, "", false, 10, 0)
	if err != nil || total != 3 {
		t.Fatalf("logs = %d, %v", total, err)
	}
	var failed, llm *store.ClassifyLog
	sources := map[string]int{}
	for i := range logs {
		l := &logs[i]
		sources[l.Source]++
		switch {
		case l.Error != "":
			failed = l
		case strings.Contains(l.Prompt, "[取件码]"):
			llm = l
		}
	}
	// Every text message goes to the model, verification codes included.
	if sources[SourceLLM] != 3 {
		t.Fatalf("sources = %v, want only the model", sources)
	}
	if failed == nil || !strings.Contains(failed.Error, "quota exceeded") || failed.Category != "" || failed.Prompt == "" {
		t.Fatalf("failed attempt = %+v", failed)
	}
	if llm == nil || llm.Category != Logistics || llm.Model != "m1" || llm.Confidence != 0.88 || strings.Contains(llm.Prompt, "5-1-2001") || llm.Reply == "" {
		t.Fatalf("model decision = %+v", llm)
	}
	for _, l := range logs {
		if strings.Contains(l.Prompt, "123456") {
			t.Fatalf("verification code sent to the model: %q", l.Prompt)
		}
	}
	if mine, _ := st.MessageClassifyLogs(ctx, llm.MessageID); len(mine) != 2 {
		t.Fatalf("message history = %d entries, want 2 (failed then decided)", len(mine))
	}
}

func TestWorkerWaitsForAModel(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "n.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx, now := context.Background(), time.Now()
	st.SaveSegment(ctx, store.Segment{PDUHash: "a", PDU: "p", Sender: "1069", SentAt: now, ReceivedAt: now, Total: 1, Seq: 1, Body: "验证码 123456"})
	st.SaveSegment(ctx, store.Segment{PDUHash: "b", PDU: "p", Sender: "1064", SentAt: now, ReceivedAt: now, Total: 1, Seq: 1, Body: "30EB1F", Binary: true})
	st.AssemblePending(ctx, now, time.Hour, nil)
	cfg := DefaultConfig() // model not enabled
	w := NewWorker(st, func(context.Context) (Config, error) { return cfg, nil }, NewClient(), slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil)
	if _, err := w.batch(ctx); err != nil {
		t.Fatalf("batch without a model = %v, want a quiet wait", err)
	}
	pending, _ := st.Unclassified(ctx, 10)
	if len(pending) != 1 || pending[0].Binary {
		t.Fatalf("pending = %+v, want the text message waiting and the binary one classified", pending)
	}
	if w.Status().LastError == "" {
		t.Fatal("status does not say classification waits for a model")
	}
	if _, total, _ := st.RecentClassifyLogs(ctx, "", true, 10, 0); total != 0 {
		t.Fatalf("waiting logged %d failures", total)
	}
}

func TestMMSIsMarketingWithoutAModel(t *testing.T) {
	w := NewWorker(nil, nil, NewClient(), slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil)
	cfg := DefaultConfig() // model not enabled
	d, err := w.Classify(context.Background(), cfg, store.Message{Kind: store.KindMMS})
	if err != nil || d.Category != Marketing || d.Source != SourceRule || d.Call != nil {
		t.Fatalf("MMS = %+v, %v; want marketing by rule", d, err)
	}
}
