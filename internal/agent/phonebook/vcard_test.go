package phonebook

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestVCardRoundTrip(t *testing.T) {
	cards := []Card{
		{Name: "妈妈", Numbers: []CardNumber{{Number: "138 0013 8000"}}, Note: "家里, 座机;另记\n第二行", Photo: bytes.Repeat([]byte{0xff, 0xd8, 1}, 100)},
		{Name: strings.Repeat("很长的名字", 10), Numbers: []CardNumber{{Number: "95588"}}},
	}
	var b bytes.Buffer
	if err := WriteVCards(&b, cards); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(b.String(), "\r\n") {
		if len(line) > 75 {
			t.Errorf("line of %d bytes", len(line))
		}
	}
	if got := ReadVCards(b.Bytes()); !reflect.DeepEqual(got, cards) {
		t.Errorf("round trip:\n%+v\n%+v", got, cards)
	}
}

func TestReadVCards(t *testing.T) {
	// An old Android export: vCard 2.1, quoted-printable, bare types.
	android := "BEGIN:VCARD\r\nVERSION:2.1\r\nN;CHARSET=UTF-8;ENCODING=QUOTED-PRINTABLE:=E5=BC=A0;=E4=B8=89;;;\r\n" +
		"TEL;CELL:13800138000\r\nTEL;WORK;VOICE:010-12345678\r\nEND:VCARD\r\n" +
		"BEGIN:VCARD\r\nVERSION:2.1\r\nFN:没有号码\r\nEND:VCARD\r\n"
	got := ReadVCards([]byte(android))
	want := []Card{{Name: "张三", Numbers: []CardNumber{{"13800138000", "手机"}, {"010-12345678", "工作"}}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("android = %+v", got)
	}
	// iPhone: grouped lines, typed TEL, ORG only.
	iphone := "BEGIN:VCARD\nVERSION:3.0\nN:;;;;\nORG:物业;\nitem1.TEL;type=pref;type=HOME:+86 21 1234 5678\nEND:VCARD\n"
	if got := ReadVCards([]byte(iphone)); len(got) != 1 || got[0].Name != "物业" || got[0].Numbers[0].Label != "家庭" {
		t.Errorf("iphone = %+v", got)
	}
}
