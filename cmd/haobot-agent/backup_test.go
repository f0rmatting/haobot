package main

import "testing"

func TestRestorable(t *testing.T) {
	for name, want := range map[string]bool{
		"haobot.db":                     true,
		"secret.key":                    true,
		"recordings/8673830/call-1.wav": true,
		"recordings/a.wav":              true,
		"recordings/":                   false,
		"recordings/../secret.key":      false,
		"recordings/x/../../etc/passwd": false,
		"../haobot.db":                  false,
		"/etc/passwd":                   false,
		"tls.key":                       false,
		"haobot.db-wal":                 false,
	} {
		if got := restorable(name); got != want {
			t.Errorf("restorable(%q) = %v, want %v", name, got, want)
		}
	}
}
