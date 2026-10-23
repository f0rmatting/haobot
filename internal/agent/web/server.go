// Package web serves the admin UI: a JSON API, a live event stream, and the
// single-page app built from ui/ and embedded from dist/.
package web

import (
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/f0rmatting/haobot/internal/agent/assistant"
	"github.com/f0rmatting/haobot/internal/agent/classify"
	"github.com/f0rmatting/haobot/internal/agent/notify"
	"github.com/f0rmatting/haobot/internal/agent/service"
	"github.com/f0rmatting/haobot/internal/agent/settings"
	"github.com/f0rmatting/haobot/internal/agent/store"
)

//go:embed all:dist
var dist embed.FS

const (
	sessionCookie = "haobot_session"
	// Logins are kept as long as browsers allow: Chrome caps cookie lifetime
	// at 400 days. The session is extended (at most once per
	// sessionRenewEvery) whenever the UI is used, so only 400 days without
	// a visit, logging out or changing the password ends it.
	sessionTTL        = 400 * 24 * time.Hour
	sessionRenewEvery = 24 * time.Hour
	maxBody           = 64 << 10
	// Username is the single web account.
	Username = "admin"
)

// Deps are the parts of the agent the web UI drives.
type Deps struct {
	Store    *store.Store
	Devices  *service.Manager
	Settings *settings.Settings
	Worker   *classify.Worker
	Client   *classify.Client
	// Assistant answers calls; its Test checks the realtime endpoint.
	Assistant *assistant.Agent
	// Notifier pushes to Telegram and group bots; the settings page tests it.
	Notifier *notify.Notifier
	// RecordingDir holds call recordings.
	RecordingDir string
	Log          *slog.Logger
	// SecureCookie marks the session cookie Secure (HTTPS reverse proxy).
	SecureCookie bool
}

type Server struct {
	Deps
	limiter *loginLimiter
	app     fs.FS
}

func New(d Deps) (*Server, error) {
	app, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, err
	}
	return &Server{Deps: d, limiter: newLoginLimiter(), app: app}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	api := func(pattern string, h http.HandlerFunc) {
		method, _, _ := strings.Cut(pattern, " ")
		if method != http.MethodGet {
			h = s.sameOrigin(h)
		}
		mux.HandleFunc(pattern, h)
	}
	authed := func(pattern string, h http.HandlerFunc) { api(pattern, s.requireAuth(h)) }

	api("GET /api/session", s.session)
	api("POST /api/login", s.login)
	authed("POST /api/logout", s.logout)
	authed("GET /api/stream", s.stream)

	authed("GET /api/status", s.status)
	authed("GET /api/events", s.events)
	authed("GET /api/activity", s.activity)
	authed("PATCH /api/devices/{id}", s.patchDevice)
	authed("DELETE /api/devices/{id}", s.forgetDevice)
	authed("POST /api/devices/{id}/adopt", s.adoptOrphans)
	authed("POST /api/devices/{id}/selftest", s.selfTest)
	authed("PUT /api/devices/{id}/recovery", s.setRecovery)
	authed("POST /api/devices/{id}/refresh", s.deviceRefresh)
	authed("POST /api/devices/{id}/reboot", s.deviceReboot)
	authed("POST /api/devices/{id}/radio", s.deviceRadio)
	authed("POST /api/devices/{id}/scan-mode", s.deviceScanMode)
	authed("POST /api/devices/{id}/console", s.deviceConsole)
	authed("POST /api/devices/{id}/sim/pin", s.simPIN)
	authed("POST /api/devices/{id}/sim/puk", s.simPUK)
	authed("POST /api/devices/{id}/sim/lock", s.simLock)
	authed("POST /api/devices/{id}/sim/change-pin", s.simChangePIN)
	authed("GET /api/devices/{id}/sim-settings", s.getSIMSettings)
	authed("PUT /api/devices/{id}/sim-settings", s.putSIMSettings)

	authed("GET /api/conversations", s.listConversations)
	authed("GET /api/conversations/messages", s.conversationMessages)
	authed("POST /api/conversations/read", s.readConversation)
	authed("POST /api/conversations/delete", s.deleteConversation)
	authed("POST /api/messages/send", s.sendMessage)
	authed("POST /api/messages/{id}/resend", s.resendMessage)

	authed("GET /api/messages", s.listMessages)
	authed("GET /api/messages/{id}", s.getMessage)
	authed("GET /api/messages/{id}/redacted", s.redactedMessage)
	authed("GET /api/messages/{id}/classification", s.messageClassification)
	authed("GET /api/classify/logs", s.classifyLogs)
	authed("PATCH /api/messages/{id}", s.patchMessage)
	authed("DELETE /api/messages/{id}", s.deleteMessage)
	authed("POST /api/messages/bulk", s.bulkMessages)

	authed("PUT /api/settings/password", s.putPassword)
	authed("GET /api/settings/classifier", s.getClassifier)
	authed("PUT /api/settings/classifier", s.putClassifier)
	authed("POST /api/settings/classifier/test", s.testClassifier)
	authed("POST /api/settings/classifier/preview", s.previewClassifier)
	authed("POST /api/settings/classifier/reclassify", s.reclassifyAll)

	s.routeCalls(authed)
	s.routeAssistant(authed)
	s.routeNotify(authed)

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "接口不存在")
	})
	mux.HandleFunc("/", s.serveApp)
	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; media-src 'self'; font-src 'self'; connect-src 'self'; manifest-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Permissions-Policy", "microphone=(self), camera=(), geolocation=()")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// serveApp serves the built UI. Unknown paths get index.html so client-side
// routes survive a reload.
func (s *Server) serveApp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name != "" && name != "index.html" {
		if info, err := fs.Stat(s.app, name); err == nil && !info.IsDir() {
			if strings.HasPrefix(name, "assets/") {
				// Vite puts a content hash in every asset name.
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			http.ServeFileFS(w, r, s.app, name)
			return
		}
	}
	index, err := fs.ReadFile(s.app, "index.html")
	if err != nil {
		http.Error(w, "UI not built", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(index)
}

// sameOrigin rejects cross-site requests (CSRF). Browsers set Sec-Fetch-Site
// themselves; older ones fall back to comparing Origin with Host. Requiring a
// JSON body adds a second layer: a cross-site form cannot send one without a
// CORS preflight, which this server never approves.
func (s *Server) sameOrigin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
			if site != "same-origin" && site != "none" {
				writeError(w, http.StatusForbidden, "cross-site request rejected")
				return
			}
		} else if origin := r.Header.Get("Origin"); origin != "" {
			parsed, err := url.Parse(origin)
			if err != nil || parsed.Host != r.Host {
				writeError(w, http.StatusForbidden, "cross-site request rejected")
				return
			}
		}
		if r.ContentLength != 0 && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			writeError(w, http.StatusUnsupportedMediaType, "需要 JSON 请求体")
			return
		}
		next(w, r)
	}
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ok, err := s.authenticated(w, r)
		if err != nil {
			s.serverError(w, err)
			return
		}
		if !ok {
			writeError(w, http.StatusUnauthorized, "请先登录")
			return
		}
		next(w, r)
	}
}

// authenticated checks the session cookie and, for an active session,
// extends it and refreshes the cookie.
func (s *Server) authenticated(w http.ResponseWriter, r *http.Request) (bool, error) {
	token := sessionToken(r)
	if token == "" {
		return false, nil
	}
	now := time.Now()
	_, err := s.Store.SessionUser(r.Context(), hashToken(token), now)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	extended, err := s.Store.ExtendSession(r.Context(), hashToken(token), now, sessionTTL, sessionRenewEvery)
	if err != nil {
		return false, err
	}
	if extended {
		s.setSessionCookie(w, token)
	}
	return true, nil
}

// sessionToken returns the session cookie's value.
func sessionToken(r *http.Request) string {
	if c, err := r.Cookie(sessionCookie); err == nil {
		return c.Value
	}
	return ""
}

type apiError struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, apiError{Error: msg})
}

func (s *Server) serverError(w http.ResponseWriter, err error) {
	s.Log.Error("web request failed", "error", err)
	writeError(w, http.StatusInternalServerError, "服务器内部错误")
}

// opError reports a failed device operation with a status that tells the UI
// what to do next.
func (s *Server) opError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrOffline), errors.Is(err, service.ErrNotReady):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, service.ErrLastAttempt):
		writeJSON(w, http.StatusConflict, apiError{Error: err.Error(), Code: "last_attempt"})
	default:
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	body := http.MaxBytesReader(w, r.Body, maxBody)
	if err := json.NewDecoder(body).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return false
	}
	return true
}

func ok(w http.ResponseWriter) { writeJSON(w, http.StatusOK, map[string]bool{"ok": true}) }
