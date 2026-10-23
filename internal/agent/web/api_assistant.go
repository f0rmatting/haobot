package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/f0rmatting/haobot/internal/agent/assistant"
	"github.com/f0rmatting/haobot/internal/agent/service"
)

type assistantJSON struct {
	AnswerAt *time.Time           `json:"answerAt"`
	Active   bool                 `json:"active"`
	RecordID int64                `json:"recordId"`
	Phase    string               `json:"phase"`
	Error    string               `json:"error"`
	Lines    []transcriptLineJSON `json:"lines"`
}

type transcriptLineJSON struct {
	Role  string    `json:"role"`
	Text  string    `json:"text"`
	Final bool      `json:"final"`
	At    time.Time `json:"at"`
}

func toAssistantJSON(a service.AssistantState) assistantJSON {
	out := assistantJSON{AnswerAt: timePtr(a.AnswerAt), Active: a.Active, RecordID: a.RecordID, Phase: a.Phase, Error: a.Error, Lines: []transcriptLineJSON{}}
	for _, l := range a.Lines {
		out.Lines = append(out.Lines, transcriptLineJSON{l.Role, l.Text, l.Final, l.At})
	}
	return out
}

type assistantSettingsJSON struct {
	assistant.Config
	// Endpoint is the realtime URL in use, derived or configured;
	// DerivedEndpoint is what an empty URL means.
	Endpoint        string   `json:"endpoint"`
	DerivedEndpoint string   `json:"derivedEndpoint"`
	WorkspaceID     string   `json:"workspaceId"` // from the classifier's Base URL, if any
	Models          []string `json:"models"`
	HasOwnKey       bool     `json:"hasOwnKey"`
	OwnKeyHint      string   `json:"ownKeyHint"`
	HasSharedKey    bool     `json:"hasSharedKey"` // the classifier has a key
	Problem         string   `json:"problem"`      // why calls cannot be answered with this setup
}

// assistantRequest is the form. APIKey nil keeps the stored key; "" removes
// it.
type assistantRequest struct {
	assistant.Config
	APIKey *string `json:"apiKey"`
}

func (s *Server) assistantSettings(r *http.Request, cfg assistant.Config) assistantSettingsJSON {
	out := assistantSettingsJSON{Config: cfg, Models: assistant.Models, HasOwnKey: cfg.APIKey != "", OwnKeyHint: keyHint(cfg.APIKey)}
	llm, err := s.Settings.LLM(r.Context())
	if err != nil {
		out.Problem = err.Error()
		return out
	}
	out.HasSharedKey = llm.APIKey != ""
	derived := cfg
	derived.URL = ""
	if d, err := derived.Endpoint(llm); err == nil {
		out.DerivedEndpoint = d
		if u, err := url.Parse(d); err == nil {
			if id, _, ok := strings.Cut(u.Host, "."); ok && strings.HasPrefix(id, "ws-") {
				out.WorkspaceID = id
			}
		}
	}
	if err := cfg.Validate(); err != nil {
		out.Problem = err.Error()
	} else if out.Endpoint, err = cfg.Endpoint(llm); err != nil {
		out.Problem = err.Error()
	} else if cfg.Key(llm) == "" {
		out.Problem = "还没有 API Key：在这里填写，或在“智能分类”里配置"
	}
	return out
}

// withAssistantKey applies the form's key over the stored one.
func (s *Server) withAssistantKey(r *http.Request, req assistantRequest) (assistant.Config, error) {
	cfg := req.Config
	cfg.Normalize()
	cfg.URL = strings.TrimSpace(cfg.URL)
	cfg.Model = strings.TrimSpace(cfg.Model)
	if req.APIKey != nil {
		cfg.APIKey = strings.TrimSpace(*req.APIKey)
		return cfg, nil
	}
	stored, err := s.Settings.Assistant(r.Context())
	if err != nil {
		return cfg, err
	}
	cfg.APIKey = stored.APIKey
	// The key used is the assistant's own or the classifier's; either may
	// go to the endpoint it was saved for, or to Bailian itself.
	llm, err := s.Settings.LLM(r.Context())
	if err != nil {
		return cfg, err
	}
	if cfg.Key(llm) == "" {
		return cfg, nil
	}
	target, err := cfg.Endpoint(llm)
	if err != nil {
		return cfg, nil // Validate reports it; nothing is sent
	}
	host := hostOf(target)
	was, _ := stored.Endpoint(llm)
	derived := cfg
	derived.URL = ""
	from, _ := derived.Endpoint(llm)
	if host != hostOf(was) && host != hostOf(from) && !bailianHost(host) {
		return cfg, errRetypeKey
	}
	return cfg, nil
}

// bailianHost reports Alibaba Cloud Model Studio (Bailian) hosts, where a
// Bailian key may always go.
func bailianHost(host string) bool {
	return strings.HasSuffix(host, ".aliyuncs.com") || strings.HasSuffix(host, ".alibabacloud.com")
}

func (s *Server) getAssistant(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.Settings.Assistant(r.Context())
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.assistantSettings(r, cfg))
}

func (s *Server) putAssistant(w http.ResponseWriter, r *http.Request) {
	var req assistantRequest
	if !decode(w, r, &req) {
		return
	}
	cfg, err := s.withAssistantKey(r, req)
	if err != nil {
		s.keyError(w, err)
		return
	}
	if err := cfg.Validate(); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	var key *string
	if req.APIKey != nil {
		key = &cfg.APIKey
	}
	if err := s.Settings.SetAssistant(r.Context(), cfg, key); err != nil {
		s.serverError(w, err)
		return
	}
	state := "已关闭"
	if cfg.Enabled {
		state = "已开启"
	}
	s.Devices.Note(r.Context(), "info", "AI 接听助手"+state+"（模型 "+cfg.Model+"）")
	writeJSON(w, http.StatusOK, s.assistantSettings(r, cfg))
}

func (s *Server) testAssistant(w http.ResponseWriter, r *http.Request) {
	var req assistantRequest
	if !decode(w, r, &req) {
		return
	}
	cfg, err := s.withAssistantKey(r, req)
	if err != nil {
		s.keyError(w, err)
		return
	}
	if err := cfg.Validate(); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if s.Assistant == nil {
		writeError(w, http.StatusServiceUnavailable, "AI 接听助手不可用")
		return
	}
	latency, format, err := s.Assistant.Test(r.Context(), cfg)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"latencyMs": latency.Milliseconds(), "format": format})
}

// rawJSON passes a stored JSON note through, or null.
func rawJSON(text string) json.RawMessage {
	if text == "" || !json.Valid([]byte(text)) {
		return nil
	}
	return json.RawMessage(text)
}

func (s *Server) routeAssistant(authed func(string, http.HandlerFunc)) {
	authed("GET /api/settings/assistant", s.getAssistant)
	authed("PUT /api/settings/assistant", s.putAssistant)
	authed("POST /api/settings/assistant/test", s.testAssistant)
	authed("POST /api/devices/{id}/call/assistant", s.callAction((*service.Service).AnswerWithAssistant))
	authed("POST /api/devices/{id}/call/assistant/skip", s.callAction((*service.Service).SkipAssistant))
	authed("POST /api/devices/{id}/call/takeover", s.callAction((*service.Service).TakeOver))
}
