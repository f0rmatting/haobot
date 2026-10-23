package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/f0rmatting/haobot/internal/agent/classify"
	"github.com/f0rmatting/haobot/internal/agent/service"
	"github.com/f0rmatting/haobot/internal/agent/store"
)

type classifierJSON struct {
	classify.Config
	HasAPIKey  bool                  `json:"hasApiKey"`
	APIKeyHint string                `json:"apiKeyHint"`
	Status     classify.WorkerStatus `json:"status"`
	Defaults   []classify.Category   `json:"defaultCategories"`
}

// keyHint shows the last characters of a key so users can tell keys apart.
func keyHint(key string) string {
	if len(key) <= 8 {
		return strings.Repeat("•", len(key))
	}
	return "••••" + key[len(key)-4:]
}

func (s *Server) getClassifier(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.Settings.LLM(r.Context())
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, classifierJSON{
		Config: cfg, HasAPIKey: cfg.APIKey != "", APIKeyHint: keyHint(cfg.APIKey),
		Status: s.Worker.Status(), Defaults: classify.DefaultCategories(),
	})
}

// classifierRequest is a configuration from the form. APIKey nil keeps the
// stored key; "" removes it.
type classifierRequest struct {
	classify.Config
	APIKey *string `json:"apiKey"`
}

func (req *classifierRequest) normalise() {
	req.BaseURL = strings.TrimSpace(req.BaseURL)
	req.Model = strings.TrimSpace(req.Model)
	for i := range req.Categories {
		req.Categories[i].Name = strings.TrimSpace(req.Categories[i].Name)
		req.Categories[i].Description = strings.TrimSpace(req.Categories[i].Description)
	}
	if req.APIKey != nil {
		key := strings.TrimSpace(*req.APIKey)
		req.APIKey = &key
	}
}

// errRetypeKey refuses to send a saved secret to a server other than the
// one it was saved for: whoever is signed in could otherwise have it sent to
// a host of their own and read it there.
var errRetypeKey = errors.New("接口地址换了：为了防止已保存的密钥被发到别的服务器，请重新填写密钥")

// hostOf is the lower-case host name of a URL, "" when there is none.
func hostOf(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// keyError reports a failure to apply a form's secret.
func (s *Server) keyError(w http.ResponseWriter, err error) {
	if errors.Is(err, errRetypeKey) {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	s.serverError(w, err)
}

// withKey returns the request's configuration with the key it would use.
func (s *Server) withKey(ctx context.Context, req classifierRequest) (classify.Config, error) {
	cfg := req.Config
	if req.APIKey != nil {
		cfg.APIKey = *req.APIKey
		return cfg, nil
	}
	saved, err := s.Settings.LLM(ctx)
	if err != nil {
		return cfg, err
	}
	if saved.APIKey != "" && (cfg.Provider != saved.Provider || hostOf(cfg.BaseURL) != hostOf(saved.BaseURL)) {
		return cfg, errRetypeKey
	}
	cfg.APIKey = saved.APIKey
	return cfg, nil
}

func (s *Server) putClassifier(w http.ResponseWriter, r *http.Request) {
	var req classifierRequest
	if !decode(w, r, &req) {
		return
	}
	req.normalise()
	// The worker would send the saved key wherever the saved address points.
	if _, err := s.withKey(r.Context(), req); err != nil {
		s.keyError(w, err)
		return
	}
	if err := s.Settings.SetLLM(r.Context(), req.Config, req.APIKey); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	s.Devices.Note(r.Context(), "info", "短信分类设置已更新")
	s.Worker.Wake()
	s.getClassifier(w, r)
}

func (s *Server) testClassifier(w http.ResponseWriter, r *http.Request) {
	var req classifierRequest
	if !decode(w, r, &req) {
		return
	}
	req.normalise()
	cfg, err := s.withKey(r.Context(), req)
	if err != nil {
		s.keyError(w, err)
		return
	}
	cfg.Enabled = true
	if err := cfg.Validate(); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	start := time.Now()
	reply, err := s.Client.Test(r.Context(), cfg)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reply": reply, "latencyMs": time.Since(start).Milliseconds()})
}

// previewClassifier shows the redacted text and, optionally, the category
// the model picks for it, so users can check what leaves the machine.
func (s *Server) previewClassifier(w http.ResponseWriter, r *http.Request) {
	var req struct {
		classifierRequest
		Sender    string `json:"sender"`
		Text      string `json:"text"`
		CallModel bool   `json:"callModel"`
	}
	if !decode(w, r, &req) {
		return
	}
	req.normalise()
	redacted, findings := classify.Redact(req.Text)
	out := map[string]any{
		"sender":   classify.RedactSender(req.Sender),
		"text":     redacted,
		"findings": findings,
		"code":     classify.ExtractCode(req.Text),
	}
	if req.CallModel {
		cfg, err := s.withKey(r.Context(), req.classifierRequest)
		if err != nil {
			s.keyError(w, err)
			return
		}
		cfg.Enabled = true
		if err := cfg.Validate(); err != nil {
			out["modelError"] = err.Error()
		} else {
			result, call, err := s.Client.Classify(r.Context(), cfg, classify.RedactSender(req.Sender), redacted)
			out["call"] = toClassifyLogJSON(classify.DecisionLog(0, classify.Decision{Source: classify.SourceLLM, Call: &call}))
			if err != nil {
				out["modelError"] = err.Error()
			} else {
				out["model"] = result
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// reclassifyAll queues every message not categorised by hand.
func (s *Server) reclassifyAll(w http.ResponseWriter, r *http.Request) {
	n, err := s.Store.ResetCategories(r.Context(), nil)
	if err != nil {
		s.serverError(w, err)
		return
	}
	s.Devices.Note(r.Context(), "info", "已将 "+strconv.Itoa(n)+" 条短信加入重新分类队列")
	s.Worker.Wake()
	s.Devices.Hub().Publish(service.TopicMessages)
	writeJSON(w, http.StatusOK, map[string]int{"affected": n})
}

type classifyLogJSON struct {
	ID         int64     `json:"id"`
	MessageID  int64     `json:"messageId"`
	At         time.Time `json:"at"`
	Source     string    `json:"source"`
	Category   string    `json:"category"`
	Rule       string    `json:"rule,omitempty"`
	Provider   string    `json:"provider,omitempty"`
	Model      string    `json:"model,omitempty"`
	Endpoint   string    `json:"endpoint,omitempty"`
	System     string    `json:"system,omitempty"`
	Prompt     string    `json:"prompt,omitempty"`
	Reply      string    `json:"reply,omitempty"`
	Confidence float64   `json:"confidence"`
	LatencyMs  int64     `json:"latencyMs"`
	Error      string    `json:"error,omitempty"`
	Sender     string    `json:"sender,omitempty"`
	Body       string    `json:"body,omitempty"`
}

func toClassifyLogJSON(l store.ClassifyLog) classifyLogJSON {
	return classifyLogJSON{
		ID: l.ID, MessageID: l.MessageID, At: l.At, Source: l.Source, Category: l.Category, Rule: l.Rule,
		Provider: l.Provider, Model: l.Model, Endpoint: l.Endpoint, System: l.System, Prompt: l.Prompt, Reply: l.Reply,
		Confidence: l.Confidence, LatencyMs: l.LatencyMs, Error: l.Error, Sender: l.Sender, Body: l.Body,
	}
}

// messageClassification returns every classification attempt of a message.
func (s *Server) messageClassification(w http.ResponseWriter, r *http.Request) {
	id, valid := s.messageID(w, r)
	if !valid {
		return
	}
	logs, err := s.Store.MessageClassifyLogs(r.Context(), id)
	if err != nil {
		s.serverError(w, err)
		return
	}
	out := make([]classifyLogJSON, 0, len(logs))
	for _, l := range logs {
		out = append(out, toClassifyLogJSON(l))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

// classifyLogs pages through recent classification attempts.
func (s *Server) classifyLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, size := pageParams(r)
	logs, total, err := s.Store.RecentClassifyLogs(r.Context(), q.Get("source"), q.Get("failed") == "1", size, (page-1)*size)
	if err != nil {
		s.serverError(w, err)
		return
	}
	out := make([]classifyLogJSON, 0, len(logs))
	for _, l := range logs {
		out = append(out, toClassifyLogJSON(l))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "total": total, "page": page, "pageSize": size})
}
