package main

import "testing"

func TestListenAddr(t *testing.T) {
	for _, tc := range []struct {
		listen  string
		port    int
		portSet bool
		want    string
	}{
		{"127.0.0.1", 9081, false, "127.0.0.1:9081"},
		{"0.0.0.0", 7000, true, "0.0.0.0:7000"},
		{"0.0.0.0:8080", 9081, false, "0.0.0.0:8080"}, // the older form keeps its port
		{"0.0.0.0:8080", 7000, true, "0.0.0.0:7000"},
		{"::1", 9081, false, "[::1]:9081"},
		{"", 9081, false, ":9081"},
	} {
		got, err := listenAddr(tc.listen, tc.port, tc.portSet)
		if err != nil || got != tc.want {
			t.Errorf("listenAddr(%q, %d, %v) = %q, %v; want %q", tc.listen, tc.port, tc.portSet, got, err, tc.want)
		}
	}
	if _, err := listenAddr("127.0.0.1", 70000, true); err == nil {
		t.Error("port 70000 accepted")
	}
}
