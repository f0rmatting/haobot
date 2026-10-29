package mms

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

var jpeg = []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00}

// part encodes one multipart entry: its headers are the content type
// followed by extra.
func part(contentType, extra, data []byte) []byte {
	headers := append(append([]byte{}, contentType...), extra...)
	out := []byte{byte(len(headers)), byte(len(data))}
	out = append(out, headers...)
	return append(out, data...)
}

func retrievePDU(body []byte, count int) []byte {
	var pdu []byte
	pdu = append(pdu, 0x8C, mmsRetrieveConf)
	pdu = append(pdu, 0x98)
	pdu = append(pdu, cstr("R42")...)
	pdu = append(pdu, 0x8D, 0x92)
	pdu = append(pdu, 0x85, 0x04, 0x6A, 0x5B, 0x8B, 0x00) // Date 1784384256
	from := append([]byte{0x80}, cstr("10655/TYPE=PLMN")...)
	pdu = append(pdu, 0x89, byte(len(from)))
	pdu = append(pdu, from...)
	subject := append([]byte{0x80 | 106}, cstr("周末优惠")...)
	pdu = append(pdu, 0x96, byte(len(subject)))
	pdu = append(pdu, subject...)
	// Content-Type: application/vnd.wap.multipart.related; type=application/smil
	ct := []byte{0x80 | 0x33, 0x89}
	ct = append(ct, cstr("application/smil")...)
	pdu = append(pdu, 0x84, byte(len(ct)))
	pdu = append(pdu, ct...)
	pdu = append(pdu, byte(count))
	return append(pdu, body...)
}

func TestParseRetrieveMultipart(t *testing.T) {
	smil := cstr("application/smil")
	textCT := []byte{0x03, 0x80 | 0x03, 0x81, 0x80 | 106} // text/plain; charset=utf-8
	imageCT := append([]byte{0x00, 0x80 | 0x1E, 0x85}, cstr("pic.jpg")...)
	imageCT[0] = byte(len(imageCT) - 1)
	location := append([]byte{0x8E}, cstr("text_0.txt")...)
	body := part(smil, nil, []byte("<smil/>"))
	body = append(body, part(textCT, location, []byte("满100减20"))...)
	body = append(body, part(imageCT, nil, jpeg)...)

	m, err := ParseRetrieve(retrievePDU(body, 3))
	if err != nil {
		t.Fatal(err)
	}
	if m.TransactionID != "R42" || m.From != "10655" || m.Subject != "周末优惠" || m.Date.Unix() != 0x6A5B8B00 {
		t.Fatalf("headers = %+v", m)
	}
	if len(m.Parts) != 3 {
		t.Fatalf("parts = %+v", m.Parts)
	}
	want := []Part{
		{ContentType: "application/smil", Data: []byte("<smil/>")},
		{ContentType: "text/plain", Name: "text_0.txt", Data: []byte("满100减20")},
		{ContentType: "image/jpeg", Name: "pic.jpg", Data: jpeg},
	}
	for i, p := range m.Parts {
		if p.ContentType != want[i].ContentType || p.Name != want[i].Name || !bytes.Equal(p.Data, want[i].Data) {
			t.Errorf("part %d = %+v (%q), want %+v", i, p, p.Data, want[i])
		}
	}
}

func TestParseRetrieveGBKTextWithoutCharset(t *testing.T) {
	gbk, err := simplifiedchinese.GBK.NewEncoder().String("您好，欢迎")
	if err != nil {
		t.Fatal(err)
	}
	body := part([]byte{0x80 | 0x03}, nil, []byte(gbk))
	m, err := ParseRetrieve(retrievePDU(body, 1))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Parts) != 1 || string(m.Parts[0].Data) != "您好，欢迎" {
		t.Fatalf("parts = %+v", m.Parts)
	}
}

func TestParseRetrieveSinglePart(t *testing.T) {
	pdu := []byte{0x8C, mmsRetrieveConf, 0x98}
	pdu = append(pdu, cstr("X")...)
	pdu = append(pdu, 0x84, 0x80|0x1E)
	pdu = append(pdu, jpeg...)
	m, err := ParseRetrieve(pdu)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Parts) != 1 || m.Parts[0].ContentType != "image/jpeg" || !bytes.Equal(m.Parts[0].Data, jpeg) {
		t.Fatalf("parts = %+v", m.Parts)
	}
}

func TestParseRetrieveStatusError(t *testing.T) {
	pdu := []byte{0x8C, mmsRetrieveConf, 0x98}
	pdu = append(pdu, cstr("X")...)
	pdu = append(pdu, 0x99, 0xE2) // permanent: message not found
	pdu = append(pdu, 0x9A)
	pdu = append(pdu, cstr("expired")...)
	_, err := ParseRetrieve(pdu)
	if !errors.Is(err, ErrRetrieveFailed) || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseRetrieveRejectsNotification(t *testing.T) {
	subject := append([]byte{0x80 | 106}, cstr("x")...)
	if _, err := ParseRetrieve(notificationPDU(subject)); err == nil {
		t.Fatal("parsed a notification as a retrieved message")
	}
}

func TestParseRetrieveTruncated(t *testing.T) {
	body := part([]byte{0x80 | 0x1E}, nil, jpeg)
	pdu := retrievePDU(body, 1)
	for n := 0; n < len(pdu)-1; n++ {
		ParseRetrieve(pdu[:n]) // must not panic
	}
}

func TestAcknowledge(t *testing.T) {
	want := []byte{0x8C, 0x85, 0x98, 'T', '1', 0x00, 0x8D, 0x92}
	if got := Acknowledge("T1"); !bytes.Equal(got, want) {
		t.Fatalf("got % X", got)
	}
}

func TestDetectCarrier(t *testing.T) {
	for _, tc := range []struct{ imsi, operator, want string }{
		{"460011234567890", "", "中国联通"},
		{"460001234567890", "", "中国移动"},
		{"460111234567890", "", "中国电信"},
		{"", "CHINA MOBILE", "中国移动"},
		{"", "CHN-UNICOM", "中国联通"},
		{"", "CHN-CT", "中国电信"},
		{"", "46003", "中国电信"},
	} {
		c, err := DetectCarrier(tc.imsi, tc.operator)
		if err != nil || c.Name != tc.want {
			t.Errorf("DetectCarrier(%q, %q) = %v, %v; want %s", tc.imsi, tc.operator, c.Name, err, tc.want)
		}
	}
	for _, imsi := range []string{"460151234567890", "310260000000000"} {
		if _, err := DetectCarrier(imsi, ""); !errors.Is(err, ErrUnknownCarrier) {
			t.Errorf("DetectCarrier(%q) err = %v", imsi, err)
		}
	}
}

func TestGetRequestThroughProxy(t *testing.T) {
	connect, raw, err := chinaUnicom.GetRequest("http://mmsc.myuni.com.cn/abc?x=1")
	if err != nil {
		t.Fatal(err)
	}
	if connect != "http://10.0.0.172:80/" {
		t.Fatalf("connect = %q", connect)
	}
	text := string(raw)
	if !strings.HasPrefix(text, "GET http://mmsc.myuni.com.cn/abc?x=1 HTTP/1.1\r\nHost: mmsc.myuni.com.cn\r\n") || !strings.HasSuffix(text, "\r\n\r\n") {
		t.Fatalf("request = %q", text)
	}
}

func TestPostRequestCarriesBody(t *testing.T) {
	_, raw, err := chinaMobile.PostRequest([]byte{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(raw, []byte("POST http://mmsc.monternet.com/ HTTP/1.1\r\n")) ||
		!bytes.Contains(raw, []byte("Content-Length: 3\r\n")) || !bytes.HasSuffix(raw, []byte("\r\n\r\n\x01\x02\x03")) {
		t.Fatalf("request = %q", raw)
	}
}

func TestRequestRejectsOddURLs(t *testing.T) {
	for _, target := range []string{"https://mmsc.example/x", "ftp://x/y", "http://x/a b", "http://x/\r\nX: y", "/relative"} {
		if _, _, err := chinaMobile.GetRequest(target); err == nil {
			t.Errorf("GetRequest(%q) accepted", target)
		}
	}
}
