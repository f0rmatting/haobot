package modem

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/warthog618/sms"
	"github.com/warthog618/sms/encoding/tpdu"
)

const (
	sendTimeout = 60 * time.Second
	// MaxParts bounds how long a message may get; carriers often drop very
	// long concatenated messages anyway.
	MaxParts = 10
)

var numberPattern = regexp.MustCompile(`^\+?[0-9]{3,20}$`)

// SubmitPDU is one encoded SMS-SUBMIT, ready for AT+CMGS in PDU mode.
type SubmitPDU struct {
	Hex    string // SMSC length octet 00 (use the SIM's SMSC) + TPDU
	Length int    // TPDU length in octets, the AT+CMGS argument
}

// CleanNumber strips separators users type and validates the rest.
func CleanNumber(number string) (string, error) {
	n := strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' || r == '(' || r == ')' {
			return -1
		}
		return r
	}, strings.TrimSpace(number))
	if !numberPattern.MatchString(n) {
		return "", errors.New("号码格式不正确，只能包含数字，国际号码以 + 开头")
	}
	return n, nil
}

// EncodeSubmit splits text into concatenated SMS-SUBMIT PDUs. Text that fits
// the GSM alphabet uses 7-bit encoding; anything else (Chinese) uses UCS-2.
func EncodeSubmit(number, text string) ([]SubmitPDU, error) {
	number, err := CleanNumber(number)
	if err != nil {
		return nil, err
	}
	if text == "" {
		return nil, errors.New("短信内容不能为空")
	}
	pdus, err := sms.Encode([]byte(text), sms.To(number))
	if err != nil {
		return nil, fmt.Errorf("编码短信失败：%w", err)
	}
	if len(pdus) > MaxParts {
		return nil, fmt.Errorf("短信太长：需要 %d 条，最多 %d 条", len(pdus), MaxParts)
	}
	out := make([]SubmitPDU, 0, len(pdus))
	for i := range pdus {
		// sms.To marks every number international; a national number such
		// as 13812345678 must go out as "unknown", or it reads as +13...
		if !strings.HasPrefix(number, "+") {
			pdus[i].DA.SetTypeOfNumber(tpdu.TonUnknown)
		}
		raw, err := pdus[i].MarshalBinary()
		if err != nil {
			return nil, fmt.Errorf("编码短信失败：%w", err)
		}
		out = append(out, SubmitPDU{Hex: "00" + strings.ToUpper(hex.EncodeToString(raw)), Length: len(raw)})
	}
	return out, nil
}

// CountParts reports how many SMS a text takes, or 0 if it cannot be sent.
func CountParts(text string) int {
	pdus, err := EncodeSubmit("10000", text)
	if err != nil {
		return 0
	}
	return len(pdus)
}

// SendSMS sends text to number and returns the message references of the
// parts. Parts already sent stay sent if a later part fails.
func (m *Modem) SendSMS(ctx context.Context, number, text string) ([]int, error) {
	pdus, err := EncodeSubmit(number, text)
	if err != nil {
		return nil, err
	}
	refs := make([]int, 0, len(pdus))
	for i, p := range pdus {
		lines, err := m.s.Submit(ctx, "AT+CMGS="+strconv.Itoa(p.Length), p.Hex, sendTimeout)
		if err != nil {
			if len(pdus) > 1 {
				return refs, fmt.Errorf("第 %d/%d 段发送失败：%w", i+1, len(pdus), err)
			}
			return refs, err
		}
		ref := -1
		if value, ok := field(lines, "+CMGS:"); ok {
			ref, _ = strconv.Atoi(strings.TrimSpace(value))
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

// IMEI reads the module's IMEI, which identifies the device.
func (m *Modem) IMEI(ctx context.Context) (string, error) {
	lines, err := m.s.Command(ctx, "AT+CGSN", shortTimeout)
	if err != nil {
		return "", err
	}
	for _, line := range lines {
		value := strings.TrimSpace(strings.TrimPrefix(line, "+CGSN:"))
		value = strings.Trim(value, `" `)
		if len(value) >= 14 && strings.Trim(value, "0123456789") == "" {
			return value, nil
		}
	}
	return "", errors.New("AT+CGSN: no IMEI in response")
}
