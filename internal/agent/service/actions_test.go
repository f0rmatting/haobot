package service

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/f0rmatting/haobot/internal/agent/modem"
)

// scripted answers each command with a fixed reply and records what it got.
func scripted(t *testing.T, replies map[string]string) (*modem.Modem, *[]string) {
	t.Helper()
	host, device := net.Pipe()
	var got []string
	go func() {
		reader := bufio.NewReader(device)
		for {
			cmd, err := reader.ReadString('\r')
			if err != nil {
				return
			}
			cmd = strings.TrimSuffix(cmd, "\r")
			got = append(got, cmd)
			reply, ok := replies[cmd]
			if !ok {
				reply = "OK"
			}
			if _, err := io.WriteString(device, "\r\n"+reply+"\r\n"); err != nil {
				return
			}
		}
	}()
	session := modem.NewSession(host)
	t.Cleanup(func() { session.Close() })
	return modem.New(session), &got
}

func TestGuardPINRefusesLastAttempt(t *testing.T) {
	m, _ := scripted(t, map[string]string{`AT+QPINC="SC"`: "+QPINC: \"SC\",1,10\r\nOK"})
	ctx := context.Background()
	if err := guardPIN(ctx, m, false); !errors.Is(err, ErrLastAttempt) {
		t.Fatalf("guardPIN with 1 attempt left = %v, want ErrLastAttempt", err)
	}
	if err := guardPIN(ctx, m, true); err != nil {
		t.Fatalf("forced guardPIN = %v", err)
	}
}

func TestFriendlyWrongPIN(t *testing.T) {
	m, _ := scripted(t, map[string]string{`AT+CPIN="0000"`: "+CME ERROR: incorrect password"})
	err := friendly(m.EnterPIN(context.Background(), "0000"))
	if err == nil || !strings.Contains(err.Error(), "密码错误") {
		t.Fatalf("friendly error = %v", err)
	}
	if strings.Contains(err.Error(), "0000") {
		t.Fatalf("error leaks the PIN: %v", err)
	}
}
