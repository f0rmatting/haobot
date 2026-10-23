package service

import (
	"bufio"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/warthog618/sms"
	"github.com/warthog618/sms/encoding/tpdu"

	"github.com/f0rmatting/haobot/internal/agent/modem"
	"github.com/f0rmatting/haobot/internal/agent/store"
)

// fakeStorage emulates CPMS/CMGL/CMGD over a pipe with per-storage contents.
type fakeStorage struct {
	mu       sync.Mutex
	selected string
	boxes    map[string]map[int]string // storage -> index -> PDU
	deleted  []string                  // "ME:3"
}

func (f *fakeStorage) serve(device net.Conn) {
	reader := bufio.NewReader(device)
	cpms := regexp.MustCompile(`^AT\+CPMS="(\w+)"`)
	for {
		cmd, err := reader.ReadString('\r')
		if err != nil {
			return
		}
		cmd = strings.TrimSuffix(cmd, "\r")
		f.mu.Lock()
		reply := "OK"
		switch {
		case cpms.MatchString(cmd):
			f.selected = cpms.FindStringSubmatch(cmd)[1]
		case cmd == "AT+CMGL=4":
			var b strings.Builder
			for i := 0; i < 10; i++ {
				if p, ok := f.boxes[f.selected][i]; ok {
					fmt.Fprintf(&b, "+CMGL: %d,0,,%d\r\n%s\r\n", i, len(p)/2-1, p)
				}
			}
			reply = b.String() + "OK"
		case strings.HasPrefix(cmd, "AT+CMGD="):
			var i int
			fmt.Sscanf(cmd, "AT+CMGD=%d", &i)
			delete(f.boxes[f.selected], i)
			f.deleted = append(f.deleted, fmt.Sprintf("%s:%d", f.selected, i))
		}
		f.mu.Unlock()
		if _, err := io.WriteString(device, "\r\n"+reply+"\r\n"); err != nil {
			return
		}
	}
}

func marshal(t *testing.T, d *tpdu.TPDU) string {
	t.Helper()
	d.SCTS = tpdu.Timestamp{Time: time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)}
	raw, err := d.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	return "00" + strings.ToUpper(hex.EncodeToString(raw))
}

func textPDU(t *testing.T, from, text string) string {
	t.Helper()
	segments, err := sms.Encode([]byte(text), sms.AsDeliver)
	if err != nil || len(segments) != 1 {
		t.Fatalf("encode: %v", err)
	}
	// Short codes such as 10010 arrive with an unknown type of number,
	// not as international numbers.
	segments[0].OA = tpdu.Address{TOA: 0x81, Addr: from}
	return marshal(t, &segments[0])
}

func binaryPDU(t *testing.T, from string, data []byte, port int) string {
	t.Helper()
	d, err := tpdu.NewDeliver()
	if err != nil {
		t.Fatal(err)
	}
	d.OA = tpdu.NewAddress(tpdu.FromNumber(from))
	d.SetDCS(0x04)
	if port != 0 {
		d.SetUDH(tpdu.UserDataHeader{{ID: 0x05, Data: []byte{byte(port >> 8), byte(port), 0x23, 0xF0}}})
	}
	d.SetUD(data)
	return marshal(t, d)
}

func mmsPush() []byte {
	cstr := func(s string) []byte { return append([]byte(s), 0) }
	push := []byte{0x01, 0x06, 0x03, 0xBE, 0xAF, 0x84}
	push = append(push, 0x8C, 0x82, 0x98)
	push = append(push, cstr("T1")...)
	subject := append([]byte{0xEA}, cstr("照片")...) // charset UTF-8, then text
	push = append(push, 0x96, byte(len(subject)))
	push = append(push, subject...)
	push = append(push, 0x8E, 0x02, 0x10, 0x00, 0x83)
	return append(push, cstr("http://mmsc.example/x")...)
}

func TestSyncStoresTextAndMMSDropsBinaryAndClearsModem(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "svc.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	fake := &fakeStorage{boxes: map[string]map[int]string{
		"ME": {
			0: textPDU(t, "10010", "您的话费余额为 12.34 元"),
			1: binaryPDU(t, "1064899100000", []byte{0x30, 0xEB, 0x1F, 0xFF}, 0),
			2: binaryPDU(t, "+8613800000000", mmsPush(), 2948),
		},
		"SM": {4: textPDU(t, "95588", "SIM 卡里的短信")},
	}}
	host, device := net.Pipe()
	go fake.serve(device)
	session := modem.NewSession(host)
	defer session.Close()

	svc := New(Options{
		Storage:         "ME",
		ScanStorages:    []string{"ME", "SM"},
		DeleteAfterSave: true,
		PollInterval:    time.Minute,
	}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := svc.sync(context.Background(), modem.New(session), func() bool { return false }); err != nil {
		t.Fatal(err)
	}

	messages, total, err := st.ListMessages(context.Background(), store.Filter{}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Fatalf("stored %d messages, want 3 (two texts and one MMS): %+v", total, messages)
	}
	kinds := map[string]store.Message{}
	for _, m := range messages {
		kinds[m.Sender] = m
	}
	if m := kinds["10010"]; m.Body != "您的话费余额为 12.34 元" || m.Binary {
		t.Fatalf("text message %+v", m)
	}
	if m := kinds["95588"]; m.Body != "SIM 卡里的短信" {
		t.Fatalf("SIM message %+v", m)
	}
	if m := kinds["+8613800000000"]; m.Kind != store.KindMMS || m.Body != "照片" || m.MMSURL != "http://mmsc.example/x" || m.MMSSize != 4096 {
		t.Fatalf("MMS message %+v", m)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if got := strings.Join(fake.deleted, " "); got != "ME:0 ME:1 ME:2 SM:4" {
		t.Fatalf("deleted %q", got)
	}
	if fake.selected != "ME" {
		t.Fatalf("read storage left at %q, want ME", fake.selected)
	}
}
