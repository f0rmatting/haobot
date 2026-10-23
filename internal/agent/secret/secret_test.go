package secret

import (
	"path/filepath"
	"testing"
)

func TestSealOpenAcrossLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.key")
	first, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := first.Seal("1234")
	if err != nil {
		t.Fatal(err)
	}
	if sealed == "1234" {
		t.Fatal("value stored in clear")
	}
	second, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := second.Open(sealed); err != nil || got != "1234" {
		t.Fatalf("Open = %q, %v", got, err)
	}
	if got, _ := second.Seal(""); got != "" {
		t.Fatalf("empty value sealed to %q", got)
	}
}

func TestOpenWithOtherKeyFails(t *testing.T) {
	a, _ := Load(filepath.Join(t.TempDir(), "a.key"))
	b, _ := Load(filepath.Join(t.TempDir(), "b.key"))
	sealed, _ := a.Seal("secret")
	if _, err := b.Open(sealed); err == nil {
		t.Fatal("opened with the wrong key")
	}
}
