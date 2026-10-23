package modem

import (
	"bufio"
	"context"
	"encoding/hex"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/warthog618/sms/encoding/tpdu"
)

func decodeSubmit(t *testing.T, p SubmitPDU) tpdu.TPDU {
	t.Helper()
	raw, err := hex.DecodeString(p.Hex)
	if err != nil || raw[0] != 0 || len(raw)-1 != p.Length {
		t.Fatalf("bad PDU %s (length %d)", p.Hex, p.Length)
	}
	d := tpdu.TPDU{Direction: tpdu.MO}
	if err := d.UnmarshalBinary(raw[1:]); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestEncodeSubmitNationalAndInternational(t *testing.T) {
	pdus, err := EncodeSubmit("138 1234 5678", "你好")
	if err != nil || len(pdus) != 1 {
		t.Fatalf("EncodeSubmit = %v, %v", pdus, err)
	}
	d := decodeSubmit(t, pdus[0])
	if d.DA.Addr != "13812345678" || d.DA.TypeOfNumber() != tpdu.TonUnknown {
		t.Fatalf("national DA = %+v", d.DA)
	}
	pdus, _ = EncodeSubmit("+8613812345678", "hi")
	if d := decodeSubmit(t, pdus[0]); d.DA.TypeOfNumber() != tpdu.TonInternational || d.DA.Addr != "8613812345678" {
		t.Fatalf("international DA = %+v", d.DA)
	}
}

func TestEncodeSubmitSplitsLongChinese(t *testing.T) {
	text := strings.Repeat("中", 140) // 70 UCS-2 characters fit one SMS
	if n := CountParts(text); n != 3 {
		t.Fatalf("CountParts = %d, want 3", n)
	}
	if n := CountParts(strings.Repeat("a", 160)); n != 1 {
		t.Fatalf("160 GSM characters take %d parts, want 1", n)
	}
	if _, err := EncodeSubmit("13812345678", strings.Repeat("中", 1000)); err == nil {
		t.Fatal("over-long message accepted")
	}
	if _, err := EncodeSubmit("abc", "x"); err == nil {
		t.Fatal("invalid number accepted")
	}
}

// fakeSubmitModem answers AT+CMGS with the "> " prompt, then reads the PDU up
// to Ctrl-Z and confirms it.
func fakeSubmitModem(t *testing.T, device net.Conn, got chan<- string) {
	reader := bufio.NewReader(device)
	for {
		cmd, err := reader.ReadString('\r')
		if err != nil {
			return
		}
		if !strings.HasPrefix(cmd, "AT+CMGS=") {
			io.WriteString(device, "\r\nOK\r\n")
			continue
		}
		io.WriteString(device, "\r\n> ")
		payload, err := reader.ReadString('\x1a')
		if err != nil {
			return
		}
		got <- strings.TrimSuffix(payload, "\x1a")
		io.WriteString(device, "\r\n+CMGS: 42\r\n\r\nOK\r\n")
	}
}

func TestSendSMSWaitsForPrompt(t *testing.T) {
	host, device := net.Pipe()
	got := make(chan string, 4)
	go fakeSubmitModem(t, device, got)
	s := NewSession(host)
	defer s.Close()
	refs, err := New(s).SendSMS(context.Background(), "13812345678", "测试回复")
	if err != nil || len(refs) != 1 || refs[0] != 42 {
		t.Fatalf("SendSMS = %v, %v", refs, err)
	}
	pdus, _ := EncodeSubmit("13812345678", "测试回复")
	if payload := <-got; payload != pdus[0].Hex {
		t.Fatalf("modem got %q, want %q", payload, pdus[0].Hex)
	}
}
