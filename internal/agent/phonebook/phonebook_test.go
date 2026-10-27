package phonebook

import (
	"context"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/f0rmatting/haobot/internal/agent/store"
)

func TestKey(t *testing.T) {
	for in, want := range map[string]string{
		"+86 138-0013-8000": "13800138000",
		"95588":             "95588",
		"+8695588":          "95588",
		"010-53153053":      "01053153053",
		"010 83461806":      "01083461806",
		"+86 21-28913888":   "02128913888",
		"008675589888888":   "075589888888",
		"4001-666-016":      "4001666016",
		"+864001666016":     "4001666016",
		"+862195533":        "02195533",
		"+86 951 1234567":   "09511234567",
		"+1(412)567-6602":   "+14125676602",
		"Apple":             "APPLE",
	} {
		if got := Key(in); got != want {
			t.Errorf("Key(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLookupYellow(t *testing.T) {
	if len(Yellow().Orgs) < 100 {
		t.Fatalf("yellow pages has %d organisations", len(Yellow().Orgs))
	}
	e, ok := builtinBook.LookupYellow("95588")
	if !ok || e.Name != "中国工商银行" || e.Source != SourceYellowPages {
		t.Fatalf("95588 = %+v, %v", e, ok)
	}
	if e2, ok := builtinBook.LookupYellow("955880"); !ok || e2.Name != e.Name {
		t.Errorf("extension 955880 = %+v, %v", e2, ok)
	}
	// Service numbers called from a city carry its area code.
	for _, n := range []string{"02195533", "021-95533", "+86 21 95533", "075595533", "0219553301"} {
		if e3, ok := builtinBook.LookupYellow(n); !ok || e3.Name != "中国建设银行" {
			t.Errorf("%s = %+v, %v", n, e3, ok)
		}
	}
	// The yellow pages' own area-coded number wins over the bare one.
	if e4, ok := builtinBook.LookupYellow("010-96123"); !ok || e4.Name == "" {
		t.Errorf("010-96123 = %+v, %v", e4, ok)
	}
	// A Shanghai landline that happens to start with 9553 is not the bank.
	for _, n := range []string{"02195533123", "021955331234", "0951123456"} {
		if e5, ok := builtinBook.LookupYellow(n); ok {
			t.Errorf("landline %s matched %+v", n, e5)
		}
	}
	if _, ok := builtinBook.LookupYellow("13800138000"); ok {
		t.Error("a mobile number matched the yellow pages")
	}
	if _, ok := builtinBook.LookupYellow("9"); ok {
		t.Error("a one-digit number matched")
	}
}

func TestBook(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	b := &Book{Store: st}
	if _, err := st.SaveContact(ctx, store.Contact{Name: "妈妈", Number: "138 0013 8000", Key: Key("138 0013 8000")}); err != nil {
		t.Fatal(err)
	}
	// The user's name for a yellow pages number wins.
	if _, err := st.SaveContact(ctx, store.Contact{Name: "工行客户经理", Number: "95588", Key: Key("95588")}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveContact(ctx, store.Contact{Name: "重复", Number: "+8613800138000", Key: Key("+8613800138000")}); err != store.ErrContactExists {
		t.Fatalf("duplicate number: err = %v", err)
	}
	if b.Name("+8613800138000") != "" {
		t.Error("contacts were used before Reload")
	}
	if err := b.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if e, _ := b.Lookup("+8613800138000"); e.Name != "妈妈" || e.Source != SourceContact {
		t.Errorf("mobile = %+v", e)
	}
	if got := b.Name("95588"); got != "工行客户经理" {
		t.Errorf("95588 = %q", got)
	}
	if got := b.Name("02195588"); got != "工行客户经理" {
		t.Errorf("02195588 = %q", got)
	}
	var nilBook *Book
	if got := nilBook.Name("95588"); got != "中国工商银行" {
		t.Errorf("nil book 95588 = %q", got)
	}
}

func TestLogosAndSignature(t *testing.T) {
	e, ok := builtinBook.LookupYellow("95555")
	if !ok || e.Logo == "" {
		t.Fatalf("95555 = %+v", e)
	}
	if _, err := fs.Stat(Logos, e.Logo); err != nil {
		t.Fatalf("logo %s: %v", e.Logo, err)
	}
	for _, body := range []string{"【招商银行】您的验证码是 123456", "您尾号 1234 的卡消费 50 元。【招商银行】 ", "【工商银行】余额变动"} {
		if s, ok := builtinBook.LookupSignature("1069012345", body); !ok || s.Logo == "" {
			t.Errorf("%q = %+v, %v", body, s, ok)
		}
	}
	if s, _ := builtinBook.LookupSignature("95588", "【工商银行】余额变动"); s.Name != "中国工商银行" {
		t.Errorf("工商银行 = %q", s.Name)
	}
	for _, c := range [][2]string{
		{"13800138000", "【招商银行】您的账户异常，请点击链接"}, // a personal number can type any signature
		{"+8613800138000", "【招商银行】您的账户异常"},
		{"1069012345", "招商银行提醒您"},
		{"1069012345", "【某某小店】欢迎光临"},
		{"1069012345", "通知【招商银行】请查收"},
	} {
		if s, ok := builtinBook.LookupSignature(c[0], c[1]); ok {
			t.Errorf("%s %q matched %+v", c[0], c[1], s)
		}
	}
}

// builtinBook knows only the yellow pages as they ship.
var builtinBook *Book

func TestYellowChanges(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	edits := []store.YellowOrg{
		// 招商银行 renamed, with a number of its own instead of 95555.
		{Builtin: "招商银行", Name: "招行", Category: "金融银行", Numbers: []store.YellowNumber{{Number: "1069210395555", Label: "营销"}}},
		{Builtin: "中国工商银行", Name: "中国工商银行", Category: "金融银行", Hidden: true},
		{Name: "小区物业", Category: "本地生活", Numbers: []store.YellowNumber{{Number: "021-12345678"}}},
	}
	for _, o := range edits {
		if _, err := st.SaveYellowOrg(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	b := &Book{Store: st}
	if err := b.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if e, ok := b.LookupYellow("1069210395555"); !ok || e.Name != "招行" || e.Label != "营销" || e.Logo == "" || e.Org.ID == 0 {
		t.Errorf("1069210395555 = %+v", e)
	}
	if e, ok := b.LookupYellow("95555"); ok {
		t.Errorf("95555 still = %+v", e)
	}
	if e, ok := b.LookupSignature("1069012345", "【招行】您好"); !ok || e.Name != "招行" {
		t.Errorf("【招行】 = %+v, %v", e, ok)
	}
	if e, ok := b.LookupYellow("95588"); ok {
		t.Errorf("hidden 95588 = %+v", e)
	}
	if e, ok := b.LookupYellow("02112345678"); !ok || e.Name != "小区物业" {
		t.Errorf("own org = %+v", e)
	}
	p := b.Pages()
	if len(p.Hidden) != 1 || len(p.Orgs) != len(Yellow().Orgs) {
		t.Errorf("%d orgs, %d hidden", len(p.Orgs), len(p.Hidden))
	}
	// The built-in pages are untouched.
	if e, _ := builtinBook.LookupYellow("95555"); e.Name != "招商银行" {
		t.Errorf("built-in 95555 = %+v", e)
	}
}
