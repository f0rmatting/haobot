package service

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/f0rmatting/haobot/internal/agent/store"
	"github.com/f0rmatting/haobot/internal/agent/transport"
)

func managerWith(t *testing.T, ids ...string) (*Manager, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx, now := context.Background(), time.Now()
	st.SaveSegment(ctx, store.Segment{PDUHash: "old", PDU: "p", Sender: "10086", SentAt: now, ReceivedAt: now, Total: 1, Seq: 1, Body: "升级前"})
	st.AssemblePending(ctx, now, time.Hour, nil)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := NewManager(Options{}, nil, transport.KindSerial, st, log)
	for _, id := range ids {
		svc := New(Options{Hub: m.hub, Transport: transport.Config{Kind: transport.KindSerial, Device: id}}, st, log)
		svc.update(func(s *Snapshot) { s.Device = store.Device{ID: id} })
		m.services[svc.Endpoint()] = svc
	}
	return m, st
}

func TestLegacyMessagesGoToTheOnlyModem(t *testing.T) {
	m, st := managerWith(t, "imei-1")
	m.adoptLegacy(context.Background())
	if n, _ := st.Orphans(context.Background()); n != 0 {
		t.Fatalf("orphans left: %d", n)
	}
}

func TestLegacyMessagesWaitWithSeveralModems(t *testing.T) {
	m, st := managerWith(t, "imei-1", "imei-2")
	m.adoptLegacy(context.Background())
	if n, _ := st.Orphans(context.Background()); n != 1 {
		t.Fatalf("orphans = %d, want 1 left for the user to assign", n)
	}
}
