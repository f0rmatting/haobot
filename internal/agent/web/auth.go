package web

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/f0rmatting/haobot/internal/agent/store"
)

func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	ok, err := s.authenticated(w, r)
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": ok, "username": Username})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if wait := s.limiter.blockedFor(ip); wait > 0 {
		writeError(w, http.StatusTooManyRequests, "尝试次数过多，请 "+strconv.Itoa(int(wait.Minutes())+1)+" 分钟后再试")
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &req) {
		return
	}
	if !s.checkPassword(r, req.Password) {
		s.limiter.fail(ip)
		s.Log.Warn("web login failed", "remote", ip)
		writeError(w, http.StatusUnauthorized, "密码错误")
		return
	}
	s.limiter.reset(ip)
	if err := s.startSession(w, r); err != nil {
		s.serverError(w, err)
		return
	}
	ok(w)
}

// checkPassword compares against the stored hash; a storage error counts as
// a mismatch.
func (s *Server) checkPassword(r *http.Request, password string) bool {
	hash, err := s.Store.PasswordHash(r.Context(), Username)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.Log.Error("read password hash", "error", err)
		return false
	}
	return hash != "" && bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request) error {
	token, err := newToken()
	if err != nil {
		return err
	}
	if err := s.Store.CreateSession(r.Context(), hashToken(token), Username, time.Now().Add(sessionTTL)); err != nil {
		return err
	}
	s.setSessionCookie(w, token)
	return nil
}

func (s *Server) setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   int(sessionTTL / time.Second),
		HttpOnly: true,
		Secure:   s.SecureCookie,
		SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if token := sessionToken(r); token != "" {
		if err := s.Store.DeleteSession(r.Context(), hashToken(token)); err != nil {
			s.serverError(w, err)
			return
		}
	}
	s.clearCookie(w, sessionCookie)
	ok(w)
}

func (s *Server) clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.SecureCookie, SameSite: http.SameSiteStrictMode})
}

// putPassword changes the admin password. Every session is signed out; the
// caller gets a fresh one so the UI keeps working.
func (s *Server) putPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if !decode(w, r, &req) {
		return
	}
	ip := clientIP(r)
	if wait := s.limiter.blockedFor(ip); wait > 0 {
		writeError(w, http.StatusTooManyRequests, "尝试次数过多，请稍后再试")
		return
	}
	if !s.checkPassword(r, req.Current) {
		s.limiter.fail(ip)
		writeError(w, http.StatusUnprocessableEntity, "当前密码不正确")
		return
	}
	if err := SetPassword(r.Context(), s.Store, req.New); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	s.Devices.Note(r.Context(), "info", "后台登录密码已修改，其他登录会话已退出")
	if err := s.startSession(w, r); err != nil {
		s.serverError(w, err)
		return
	}
	ok(w)
}

// SetPassword hashes and stores the web password.
func SetPassword(ctx context.Context, st *store.Store, password string) error {
	if len(password) < 8 {
		return errors.New("密码至少 8 位")
	}
	if len(password) > 72 {
		return errors.New("密码最多 72 个字符")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return st.SetPasswordHash(ctx, Username, string(hash))
}

// RandomPassword returns a password suitable for the first start.
func RandomPassword() (string, error) {
	const alphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b), nil
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// hashToken means a leaked database does not expose live session cookies.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// loginLimiter locks an address out for a while after repeated failures.
type loginLimiter struct {
	mu       sync.Mutex
	failures map[string]*loginFailures
}

type loginFailures struct {
	count int
	last  time.Time
	until time.Time
}

const (
	maxLoginFailures = 5
	loginLockout     = 5 * time.Minute
)

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{failures: make(map[string]*loginFailures)}
}

func (l *loginLimiter) blockedFor(ip string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if f, ok := l.failures[ip]; ok {
		return time.Until(f.until)
	}
	return 0
}

func (l *loginLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	// Forget addresses that are not locked and have been quiet for a while.
	for key, f := range l.failures {
		if now.After(f.until) && now.Sub(f.last) > loginLockout {
			delete(l.failures, key)
		}
	}
	f, ok := l.failures[ip]
	if !ok || (f.count >= maxLoginFailures && now.After(f.until)) {
		f = &loginFailures{}
		l.failures[ip] = f
	}
	f.count++
	f.last = now
	if f.count >= maxLoginFailures {
		f.until = now.Add(loginLockout)
	}
}

func (l *loginLimiter) reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, ip)
}
