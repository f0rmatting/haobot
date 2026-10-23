package web

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestSelfSignedIsKept(t *testing.T) {
	dir := t.TempDir()
	first, made, err := SelfSigned(dir)
	if err != nil || !made {
		t.Fatalf("first: made=%v err=%v", made, err)
	}
	again, made, err := SelfSigned(dir)
	if err != nil || made || Fingerprint(again) != Fingerprint(first) {
		t.Fatalf("second: made=%v err=%v; the certificate should be reused", made, err)
	}
	if fi, err := os.Stat(filepath.Join(dir, keyFile)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v %v", fi.Mode(), err)
	}
	leaf, _ := x509.ParseCertificate(first.Certificate[0])
	names, ips := localNames()
	if !covers(first, names, ips) || len(leaf.IPAddresses) < 2 {
		t.Fatalf("certificate names %v %v", leaf.DNSNames, leaf.IPAddresses)
	}
	// A new address (the machine got another IP) makes a new certificate.
	if covers(first, names, append(ips, net.ParseIP("10.99.88.77"))) {
		t.Fatal("covers an address it does not name")
	}
}

func TestSplitServesHTTPSAndRedirectsHTTP(t *testing.T) {
	cert, _, err := SelfSigned(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	secure, plain := Split(l, &tls.Config{Certificates: []tls.Certificate{cert}})
	addr := l.Addr().String()
	app := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "hello "+r.URL.Path) })}
	redirect := &http.Server{Handler: RedirectHTTPS(addr)}
	go app.Serve(secure)
	go redirect.Serve(plain)
	defer app.Close()
	defer redirect.Close()

	client := &http.Client{
		Transport:     &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Get("http://" + addr + "/messages?x=1")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTemporaryRedirect || resp.Header.Get("Location") != "https://"+addr+"/messages?x=1" {
		t.Fatalf("plain HTTP: %d → %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, err = client.Get("https://" + addr + "/api")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "hello /api" {
		t.Fatalf("HTTPS body %q", body)
	}
}
