package mms

import (
	"errors"
	"testing"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// push wraps an MMS PDU in a connectionless WSP Push with the MMS content
// type and the mms.ua application id.
func push(mmsPDU []byte) []byte {
	headers := []byte{0x80 | contentTypeMMS, 0xAF, 0x84}
	out := []byte{0x01, wspPushPDU, byte(len(headers))}
	out = append(out, headers...)
	return append(out, mmsPDU...)
}

func cstr(s string) []byte { return append([]byte(s), 0) }

func notificationPDU(subject []byte) []byte {
	from := append([]byte{0x80}, cstr("+8613800000000/TYPE=PLMN")...)
	var pdu []byte
	pdu = append(pdu, 0x8C, mmsNotification)
	pdu = append(pdu, 0x98)
	pdu = append(pdu, cstr("T123")...)
	pdu = append(pdu, 0x8D, 0x92) // MMS 1.2
	pdu = append(pdu, 0x89, byte(len(from)))
	pdu = append(pdu, from...)
	pdu = append(pdu, 0x96, byte(len(subject)))
	pdu = append(pdu, subject...)
	pdu = append(pdu, 0x8A, 0x80)                               // personal
	pdu = append(pdu, 0x8E, 0x02, 0x1F, 0x40)                   // 8000 bytes
	pdu = append(pdu, 0x88, 0x05, 0x81, 0x03, 0x03, 0xF4, 0x80) // relative 259200 s
	pdu = append(pdu, 0x83)
	return append(pdu, cstr("http://mmsc.example/abc")...)
}

func TestParseNotificationUTF8(t *testing.T) {
	received := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	subject := append([]byte{0x80 | 106}, cstr("你好")...)
	n, err := ParseWAPPush(push(notificationPDU(subject)), received)
	if err != nil {
		t.Fatal(err)
	}
	want := Notification{
		TransactionID: "T123",
		From:          "+8613800000000",
		Subject:       "你好",
		Size:          8000,
		Expiry:        received.Add(72 * time.Hour),
		URL:           "http://mmsc.example/abc",
	}
	if n != want {
		t.Fatalf("got %+v\nwant %+v", n, want)
	}
}

func TestParseNotificationGB2312Subject(t *testing.T) {
	gb, err := simplifiedchinese.GB18030.NewEncoder().String("春节快乐")
	if err != nil {
		t.Fatal(err)
	}
	// GB2312 is MIBenum 2025, a long integer (2 bytes).
	subject := append([]byte{0x02, 0x07, 0xE9}, cstr(gb)...)
	n, err := ParseWAPPush(push(notificationPDU(subject)), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if n.Subject != "春节快乐" {
		t.Fatalf("subject = %q", n.Subject)
	}
}

func TestParseRejectsServiceIndication(t *testing.T) {
	si := []byte{0x01, wspPushPDU, 0x01, 0xAE, 0x02, 0x05, 0x6A} // text/vnd.wap.sic
	if _, err := ParseWAPPush(si, time.Now()); !errors.Is(err, ErrNotMMS) {
		t.Fatalf("err = %v, want ErrNotMMS", err)
	}
}

func TestParseTruncated(t *testing.T) {
	full := push(notificationPDU(append([]byte{0x80 | 106}, cstr("x")...)))
	if _, err := ParseWAPPush(full[:len(full)-10], time.Now()); err == nil {
		t.Fatal("expected an error for a truncated notification")
	}
}
