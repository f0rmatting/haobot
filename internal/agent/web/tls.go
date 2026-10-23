package web

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// Self-signed certificate files in the data directory.
const (
	certFile = "tls.crt"
	keyFile  = "tls.key"
	// certLifetime stays under the 825 days Apple platforms accept.
	certLifetime = 800 * 24 * time.Hour
	// certRenew is how long before expiry a new certificate is made.
	certRenew = 30 * 24 * time.Hour
)

// SelfSigned returns the certificate kept in dir, making a new one when
// there is none, it is about to expire, or it does not name one of this
// machine's current addresses (the NAS got a new IP). The bool reports
// whether a new one was made.
func SelfSigned(dir string) (tls.Certificate, bool, error) {
	certPath, keyPath := filepath.Join(dir, certFile), filepath.Join(dir, keyFile)
	names, ips := localNames()
	if cert, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil && covers(cert, names, ips) {
		return cert, false, nil
	}
	certPEM, keyPEM, err := newCert(names, ips)
	if err != nil {
		return tls.Certificate{}, false, err
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return tls.Certificate{}, false, err
	}
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return tls.Certificate{}, false, err
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	return cert, true, err
}

// Fingerprint is the SHA-256 of the certificate, to compare with what the
// browser shows before trusting it.
func Fingerprint(cert tls.Certificate) string {
	if len(cert.Certificate) == 0 {
		return ""
	}
	sum := sha256.Sum256(cert.Certificate[0])
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

// localNames lists the names and addresses the browser may use to reach
// this machine.
func localNames() ([]string, []net.IP) {
	names := []string{"localhost"}
	if host, err := os.Hostname(); err == nil && host != "" {
		host = strings.TrimSuffix(host, ".local")
		names = append(names, host, host+".local")
	}
	ips := []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		// IPv6 addresses are left out: temporary ones rotate, and a new
		// certificate each time would bring the browser warning back.
		if !ok || ipnet.IP.IsLoopback() || ipnet.IP.To4() == nil {
			continue
		}
		ips = append(ips, ipnet.IP)
	}
	return names, ips
}

// covers reports whether cert is still valid for a while and names every
// one of names and ips.
func covers(cert tls.Certificate, names []string, ips []net.IP) bool {
	if len(cert.Certificate) == 0 {
		return false
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil || time.Until(leaf.NotAfter) < certRenew {
		return false
	}
	for _, n := range names {
		if !slices.Contains(leaf.DNSNames, n) {
			return false
		}
	}
	for _, ip := range ips {
		if !slices.ContainsFunc(leaf.IPAddresses, ip.Equal) {
			return false
		}
	}
	return true
}

func newCert(names []string, ips []net.IP) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "号小助 haobot", Organization: []string{"haobot"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(certLifetime),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              names,
		IPAddresses:           ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), nil
}

// sniffTimeout bounds the wait for a new connection's first byte.
const sniffTimeout = 10 * time.Second

// Split serves HTTPS and plain HTTP on one port. A TLS handshake starts with
// byte 0x16, which no HTTP request does: those connections come out of
// secure already wrapped in TLS, the others out of plain.
func Split(l net.Listener, cfg *tls.Config) (secure, plain net.Listener) {
	s := &splitter{Listener: l, secure: make(chan net.Conn), plain: make(chan net.Conn), done: make(chan struct{})}
	go s.run(cfg)
	return &half{s, s.secure}, &half{s, s.plain}
}

type splitter struct {
	net.Listener
	secure, plain chan net.Conn
	done          chan struct{}
	once          sync.Once
	err           error
}

func (s *splitter) run(cfg *tls.Config) {
	for {
		c, err := s.Listener.Accept()
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			s.close(err)
			return
		}
		go s.sniff(c, cfg)
	}
}

func (s *splitter) sniff(c net.Conn, cfg *tls.Config) {
	c.SetReadDeadline(time.Now().Add(sniffTimeout))
	r := bufio.NewReader(c)
	first, err := r.Peek(1)
	c.SetReadDeadline(time.Time{})
	if err != nil {
		c.Close()
		return
	}
	pc := &peekedConn{Conn: c, r: r}
	out, conn := s.plain, net.Conn(pc)
	if first[0] == 0x16 {
		out, conn = s.secure, tls.Server(pc, cfg)
	}
	select {
	case out <- conn:
	case <-s.done:
		c.Close()
	}
}

func (s *splitter) close(err error) {
	s.once.Do(func() {
		s.err = err
		close(s.done)
		s.Listener.Close()
	})
}

// half is one side of a splitter; closing either closes the port.
type half struct {
	s  *splitter
	ch chan net.Conn
}

func (h *half) Accept() (net.Conn, error) {
	select {
	case c := <-h.ch:
		return c, nil
	case <-h.s.done:
		if h.s.err != nil {
			return nil, h.s.err
		}
		return nil, net.ErrClosed
	}
}

func (h *half) Close() error {
	h.s.close(nil)
	return nil
}

func (h *half) Addr() net.Addr { return h.s.Listener.Addr() }

// peekedConn replays the byte read to tell TLS from HTTP.
type peekedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *peekedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// RedirectHTTPS sends plain HTTP requests to the same address over HTTPS.
func RedirectHTTPS(fallbackHost string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if host == "" {
			host = fallbackHost
		}
		// Temporary, so turning TLS off later does not leave browsers stuck
		// on a cached redirect.
		http.Redirect(w, r, "https://"+host+r.URL.RequestURI(), http.StatusTemporaryRedirect)
	})
}

// ErrNoCert is returned when only one of the certificate and key is given.
var ErrNoCert = fmt.Errorf("-tls-cert 和 -tls-key 需要一起指定")
