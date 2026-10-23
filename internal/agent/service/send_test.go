package service

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/f0rmatting/haobot/internal/agent/store"
)

// sendingModem is a ready modem with an empty inbox that accepts AT+CMGS.
type sendingModem struct {
	mu   sync.Mutex
	sent []string
	fail bool
}

func (f *sendingModem) serve(device net.Conn) {
	reader := bufio.NewReader(device)
	for {
		cmd, err := reader.ReadString('\r')
		if err != nil {
			return
		}
		cmd = strings.TrimSuffix(cmd, "\r")
		reply := "OK"
		switch {
		case cmd == "AT+CGSN":
			reply = "866123456789012\r\nOK"
		case cmd == "ATI":
			reply = "Quectel\r\nEC20F\r\nRevision: X\r\nOK"
		case cmd == "AT+CPIN?":
			reply = "+CPIN: READY\r\nOK"
		case strings.HasPrefix(cmd, "AT+CMGS="):
			io.WriteString(device, "\r\n> ")
			payload, err := reader.ReadString('\x1a')
			if err != nil {
				return
			}
			f.mu.Lock()
			f.sent = append(f.sent, payload)
			fail := f.fail
			f.mu.Unlock()
			reply = "+CMGS: 7\r\nOK"
			if fail {
				reply = "+CMS ERROR: 500"
			}
		}
		if _, err := io.WriteString(device, "\r\n"+reply+"\r\n"); err != nil {
			return
		}
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSendThroughRunningService(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "send.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	fake := &sendingModem{}
	host, device := net.Pipe()
	go fake.serve(device)
	svc := New(Options{Storage: "ME", ScanStorages: []string{"ME"}, PollInterval: time.Minute}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.serve(ctx, host)
	waitFor(t, "SMS ready", func() bool { return svc.Snapshot().SMSReady })

	if id := svc.DeviceID(); id != "866123456789012" {
		t.Fatalf("device id = %q", id)
	}
	if dev, _ := st.GetDevice(ctx, "866123456789012"); dev.Name != "EC20F-9012" {
		t.Fatalf("default device name = %q", dev.Name)
	}
	msg, err := svc.Send(ctx, "138 0013 8000", "你好，收到")
	if err != nil {
		t.Fatal(err)
	}
	if msg.Status != store.StatusSending || msg.Peer != "13800138000" {
		t.Fatalf("queued message = %+v", msg)
	}
	waitFor(t, "sent status", func() bool {
		m, _ := st.GetMessage(ctx, msg.ID)
		return m.Status == store.StatusSent
	})

	fake.mu.Lock()
	fake.fail = true
	fake.mu.Unlock()
	failed, _ := svc.Send(ctx, "13800138000", "再试一次")
	waitFor(t, "failed status", func() bool {
		m, _ := st.GetMessage(ctx, failed.ID)
		return m.Status == store.StatusFailed && m.Error != ""
	})
	fake.mu.Lock()
	fake.fail = false
	fake.mu.Unlock()
	m, _ := st.GetMessage(ctx, failed.ID)
	if _, err := svc.Resend(ctx, m); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "resent", func() bool {
		m, _ := st.GetMessage(ctx, failed.ID)
		return m.Status == store.StatusSent
	})
	if _, err := svc.Send(ctx, "not a number", "x"); err == nil {
		t.Fatal("invalid number accepted")
	}
}
