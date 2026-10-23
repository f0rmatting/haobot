// Package pdu decodes SMS-DELIVER PDUs as returned by AT+CMGL in PDU mode.
package pdu

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/warthog618/sms"
	"github.com/warthog618/sms/encoding/pdumode"
	"github.com/warthog618/sms/encoding/tpdu"
)

// ErrNotDeliver marks PDUs that are not incoming messages (status reports,
// stored outgoing messages); they are skipped rather than treated as broken.
var ErrNotDeliver = errors.New("not an SMS-DELIVER PDU")

// Version identifies the decoding rules. Bump it whenever Decode's output for
// an existing PDU changes, so stored messages get re-decoded on startup.
const Version = "3"

// Segment is one received PDU. Single-part messages have Total == 1.
type Segment struct {
	Sender string
	SentAt time.Time
	// Text is the message text, or the user data as upper-case hex when
	// Binary is set.
	Text   string
	Binary bool
	// Port is the UDH application destination port, 0 when absent.
	// WAP Push (MMS notifications among others) uses WAPPushPort.
	Port  int
	Ref   int
	Total int
	Seq   int
}

// WAPPushPort is the WDP destination port of WAP Push messages.
const WAPPushPort = 2948

// Decode parses a hex PDU including the leading SMSC address.
func Decode(hexPDU string) (Segment, error) {
	frame, err := pdumode.UnmarshalHexString(hexPDU)
	if err != nil {
		return Segment{}, fmt.Errorf("decode PDU frame: %w", err)
	}
	t, err := sms.Unmarshal(frame.TPDU, sms.AsMT)
	if err != nil {
		return Segment{}, fmt.Errorf("decode TPDU: %w", err)
	}
	if t.SmsType() != tpdu.SmsDeliver {
		return Segment{}, ErrNotDeliver
	}
	text, err := sms.Decode([]*tpdu.TPDU{t})
	// Anyone can send broken UCS-2 (a lone surrogate, an odd length). The
	// library then fails, or drops the next character with the bad one;
	// decoding it here keeps everything else and replaces only what is
	// broken, so the message is read rather than left in the storage.
	if alphabet, _ := t.Alphabet(); alphabet == tpdu.AlphaUCS2 {
		text, err = lenientUCS2(t.UD), nil
	}
	if err != nil {
		return Segment{}, fmt.Errorf("decode user data: %w", err)
	}
	seg := Segment{
		Sender: printable(t.OA.Number()),
		SentAt: t.SCTS.Time,
		Text:   string(text),
		Total:  1,
		Seq:    1,
	}
	seg.Port = destinationPort(t.UDH)
	// 8-bit data (DCS class 0x04 and friends) is usually machine payload, not
	// text. Keep it as hex unless it happens to be valid UTF-8; WAP Push is
	// always binary so MMS notifications can be parsed later.
	alphabet, _ := t.Alphabet()
	if alphabet == tpdu.Alpha8Bit && (!utf8.Valid(text) || seg.Port == WAPPushPort) {
		seg.Text, seg.Binary = strings.ToUpper(hex.EncodeToString(text)), true
	} else if !utf8.Valid(text) {
		seg.Text = strings.ToValidUTF8(seg.Text, "�")
	}
	// A part numbered outside its message would count towards completing
	// it; such a header is ignored and the part kept as a message of its own.
	if total, seq, ref, ok := t.ConcatInfo(); ok && total > 1 && seq >= 1 && seq <= total {
		seg.Total, seg.Seq, seg.Ref = total, seq, ref
	}
	return seg, nil
}

// lenientUCS2 decodes UTF-16BE, replacing what is not valid with U+FFFD.
func lenientUCS2(ud []byte) []byte {
	units := make([]uint16, len(ud)/2)
	for i := range units {
		units[i] = uint16(ud[2*i])<<8 | uint16(ud[2*i+1])
	}
	return []byte(string(utf16.Decode(units)))
}

// printable drops control characters: an alphanumeric sender is free text
// and could carry line breaks into logs and notifications.
func printable(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

// destinationPort reads the application port addressing IE (3GPP TS 23.040
// 9.2.3.24.3/4): 0x05 carries 16-bit ports, 0x04 8-bit ones.
func destinationPort(udh tpdu.UserDataHeader) int {
	if ie, ok := udh.IE(0x05); ok && len(ie.Data) >= 2 {
		return int(ie.Data[0])<<8 | int(ie.Data[1])
	}
	if ie, ok := udh.IE(0x04); ok && len(ie.Data) >= 1 {
		return int(ie.Data[0])
	}
	return 0
}
