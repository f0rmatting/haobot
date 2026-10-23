package pdu

import (
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/warthog618/sms"
	"github.com/warthog618/sms/encoding/tpdu"
)

// deliverPDUs builds SMS-DELIVER PDUs (with an empty SMSC prefix, as a modem
// would return them) for msg from sender.
func deliverPDUs(t *testing.T, sender, msg string) []string {
	t.Helper()
	segments, err := sms.Encode([]byte(msg), sms.AsDeliver, sms.From(sender))
	if err != nil {
		t.Fatal(err)
	}
	scts := tpdu.Timestamp{Time: time.Date(2026, 10, 20, 9, 30, 0, 0, time.FixedZone("", 8*3600))}
	var out []string
	for _, segment := range segments {
		segment.SCTS = scts
		raw, err := segment.MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, "00"+strings.ToUpper(hex.EncodeToString(raw)))
	}
	return out
}

func TestDecodeSingleChinese(t *testing.T) {
	pdus := deliverPDUs(t, "+8610010", "您的验证码是 123456，5 分钟内有效。")
	if len(pdus) != 1 {
		t.Fatalf("got %d segments, want 1", len(pdus))
	}
	seg, err := Decode(pdus[0])
	if err != nil {
		t.Fatal(err)
	}
	if seg.Sender != "+8610010" || seg.Text != "您的验证码是 123456，5 分钟内有效。" || seg.Total != 1 {
		t.Fatalf("unexpected segment %+v", seg)
	}
	if want := time.Date(2026, 10, 20, 1, 30, 0, 0, time.UTC); !seg.SentAt.Equal(want) {
		t.Fatalf("SentAt = %v, want %v", seg.SentAt, want)
	}
}

func TestDecodeConcatenated(t *testing.T) {
	msg := strings.Repeat("长短信拼接测试。", 20)
	pdus := deliverPDUs(t, "10086", msg)
	if len(pdus) < 2 {
		t.Fatalf("got %d segments, want a multipart message", len(pdus))
	}
	var joined strings.Builder
	for i, raw := range pdus {
		seg, err := Decode(raw)
		if err != nil {
			t.Fatal(err)
		}
		if seg.Total != len(pdus) || seg.Seq != i+1 {
			t.Fatalf("segment %d: total=%d seq=%d", i, seg.Total, seg.Seq)
		}
		joined.WriteString(seg.Text)
	}
	if joined.String() != msg {
		t.Fatalf("reassembled text mismatch")
	}
}

func TestDecodeBinaryAsHex(t *testing.T) {
	deliver, err := tpdu.NewDeliver()
	if err != nil {
		t.Fatal(err)
	}
	deliver.OA = tpdu.NewAddress(tpdu.FromNumber("1064800000000"))
	deliver.SetDCS(0x04) // 8-bit data
	deliver.SetUD([]byte{0x30, 0xEB, 0x1F, 0x00, 0xFF, 0x62})
	raw, err := deliver.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	seg, err := Decode("00" + hex.EncodeToString(raw))
	if err != nil {
		t.Fatal(err)
	}
	if !seg.Binary || seg.Text != "30EB1F00FF62" {
		t.Fatalf("segment %+v, want binary hex", seg)
	}
}

func TestDecodeRejectsGarbage(t *testing.T) {
	if _, err := Decode("ZZ"); err == nil {
		t.Fatal("expected an error")
	}
}

// Anyone can send broken UCS-2; the message is still read.
func TestDecodeBrokenUCS2(t *testing.T) {
	pdu := deliverPDUs(t, "+8613800138000", "你好")[0]
	if !strings.HasSuffix(pdu, "4F60597D") {
		t.Fatalf("unexpected PDU %s", pdu)
	}
	pdu = strings.TrimSuffix(pdu, "4F60597D") + "D800597D" // lone high surrogate
	seg, err := Decode(pdu)
	if err != nil || seg.Text != "�好" {
		t.Fatalf("Decode = %q, %v", seg.Text, err)
	}
}
