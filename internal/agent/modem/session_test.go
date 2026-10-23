package modem

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeModem answers commands on the far end of a pipe using a canned script.
func fakeModem(t *testing.T, replies map[string]string) *Session {
	t.Helper()
	host, device := net.Pipe()
	go func() {
		reader := bufio.NewReader(device)
		for {
			cmd, err := reader.ReadString('\r')
			if err != nil {
				return
			}
			cmd = strings.TrimSuffix(cmd, "\r")
			reply, ok := replies[cmd]
			if !ok {
				reply = "ERROR"
			}
			if _, err := device.Write([]byte(cmd + "\r\r\n" + strings.ReplaceAll(reply, "\n", "\r\n") + "\r\n")); err != nil {
				return
			}
		}
	}()
	s := NewSession(host)
	t.Cleanup(func() { s.Close(); device.Close() })
	return s
}

func TestCommandRoutesURCsAndStripsEcho(t *testing.T) {
	s := fakeModem(t, map[string]string{
		"AT+CPIN?": "+CMTI: \"ME\",7\n+CPIN: READY\nOK",
	})
	lines, err := s.Command(context.Background(), "AT+CPIN?", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || lines[0] != "+CPIN: READY" {
		t.Fatalf("lines = %q", lines)
	}
	select {
	case urc := <-s.URCs():
		if urc != `+CMTI: "ME",7` {
			t.Fatalf("urc = %q", urc)
		}
	case <-time.After(time.Second):
		t.Fatal("URC was not routed")
	}
}

func TestBasicCommandResponse(t *testing.T) {
	s := fakeModem(t, map[string]string{"ATI": "Quectel\nEC20F\nRevision: X\n\nOK"})
	lines, err := s.Command(context.Background(), "ATI", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 3 || lines[0] != "Quectel" {
		t.Fatalf("lines = %q", lines)
	}
}

func TestCommandErrorAndRedaction(t *testing.T) {
	s := fakeModem(t, map[string]string{`AT+CPIN="1234"`: "+CME ERROR: incorrect password"})
	_, err := s.Command(context.Background(), `AT+CPIN="1234"`, time.Second)
	var cmdErr *CommandError
	if !errors.As(err, &cmdErr) {
		t.Fatalf("err = %v, want CommandError", err)
	}
	if strings.Contains(err.Error(), "1234") {
		t.Fatalf("PIN leaked into error: %v", err)
	}
}

func TestListSMS(t *testing.T) {
	s := fakeModem(t, map[string]string{
		"AT+CMGL=4": "+CMGL: 0,1,,24\n0791AB\n+CMGL: 3,0,,30\n0791CD\nOK",
	})
	entries, err := New(s).ListSMS(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []StoredPDU{{Index: 0, Stat: 1, PDU: "0791AB"}, {Index: 3, Stat: 0, PDU: "0791CD"}}
	if len(entries) != len(want) || entries[0] != want[0] || entries[1] != want[1] {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestSessionEndsWhenPortCloses(t *testing.T) {
	host, device := net.Pipe()
	s := NewSession(host)
	device.Close()
	select {
	case <-s.Done():
		if !errors.Is(s.Err(), ErrClosed) {
			t.Fatalf("err = %v", s.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("session did not end")
	}
	s.Close()
}

func TestSplitFields(t *testing.T) {
	got := splitFields(`0,0,"CHN-UNICOM, LTE",7`)
	if len(got) != 4 || got[2] != "CHN-UNICOM, LTE" {
		t.Fatalf("got %q", got)
	}
}

func TestParseServingCellLTE(t *testing.T) {
	cell := parseServingCell([]string{`+QENG: "servingcell","NOCONN","LTE","FDD",460,00,1A2D007,12,1650,3,5,5,1D4C,-95,-10,-65,12,-`})
	if !cell.Valid || cell.RAT != "LTE" || cell.Band != 3 || cell.RSRP != -95 || cell.RSRQ != -10 || cell.SINR != 12 || cell.PCI != 12 {
		t.Fatalf("cell = %+v", cell)
	}
	gsm := parseServingCell([]string{`+QENG: "servingcell","NOCONN","GSM",460,00,5A6B,1234,30,87,900,-70`})
	if gsm.Valid || gsm.RAT != "GSM" || gsm.Raw == "" {
		t.Fatalf("gsm cell = %+v", gsm)
	}
}

// TestParseCallsSkipsDataConnections uses lines captured from a real EC20,
// which lists its data bearers (mode 1) next to voice calls.
func TestParseCallsSkipsDataConnections(t *testing.T) {
	calls := parseCalls([]string{
		`+CLCC: 1,1,0,1,0,"",128`,
		`+CLCC: 2,1,0,1,0,"",128`,
		`+CLCC: 4,1,4,0,0,"19300000000",128`,
		`+CLCC: 3,1,0,1,0,"",128`,
	})
	if len(calls) != 1 {
		t.Fatalf("calls = %+v, want only the voice call", calls)
	}
	if c := calls[0]; c.ID != 4 || !c.Incoming || c.State != CallIncoming || c.Number != "19300000000" {
		t.Fatalf("call = %+v", c)
	}
	out := parseCalls([]string{`+CLCC: 3,0,0,0,0,"19300000000",129`})
	if len(out) != 1 || out[0].Incoming || out[0].State != CallActive {
		t.Fatalf("outgoing = %+v", out)
	}
}

func TestLateReplyIsNotTakenForTheNext(t *testing.T) {
	host, device := net.Pipe()
	s := NewSession(host)
	t.Cleanup(func() { s.Close(); device.Close() })
	reader := bufio.NewReader(device)
	write := func(text string) { device.Write([]byte(strings.ReplaceAll(text, "\n", "\r\n"))) }
	go func() {
		for {
			cmd, err := reader.ReadString('\r')
			if err != nil {
				return
			}
			switch strings.TrimSuffix(cmd, "\r") {
			case "AT+CMGL=4":
				// Answered only after the command was given up on.
				time.Sleep(150 * time.Millisecond)
				write("\n+CMGL: 1,1,,20\nLATE\n\nOK\n")
			case "AT+CMEE?":
				write("\n+CMEE: 1\n\nOK\n")
			case "AT+CSQ":
				write("\n+CSQ: 20,99\n\nOK\n")
			}
		}
	}()

	_, err := s.Command(context.Background(), "AT+CMGL=4", 50*time.Millisecond)
	if !errors.Is(err, ErrTimeout) || !s.Stale() {
		t.Fatalf("err = %v, stale = %v; want a timeout", err, s.Stale())
	}
	time.Sleep(50 * time.Millisecond) // the late reply is on its way
	lines, err := s.Command(context.Background(), "AT+CSQ", time.Second)
	if err != nil || len(lines) != 1 || lines[0] != "+CSQ: 20,99" {
		t.Fatalf("AT+CSQ = %q, %v; the late AT+CMGL reply leaked in", lines, err)
	}
	if s.Stale() {
		t.Fatal("still stale after the modem answered")
	}
}
