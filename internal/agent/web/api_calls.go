package web

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/f0rmatting/haobot/internal/agent/modem"
	"github.com/f0rmatting/haobot/internal/agent/service"
	"github.com/f0rmatting/haobot/internal/agent/store"
)

type callJSON struct {
	ID         int        `json:"id"`
	Incoming   bool       `json:"incoming"`
	State      string     `json:"state"` // active, held, dialing, alerting, incoming, waiting
	Number     string     `json:"number"`
	StartedAt  time.Time  `json:"startedAt"`
	AnsweredAt *time.Time `json:"answeredAt"`
	RecordID   int64      `json:"recordId"`
	caller
}

var callStates = map[modem.CallState]string{
	modem.CallActive: "active", modem.CallHeld: "held", modem.CallDialing: "dialing",
	modem.CallAlerting: "alerting", modem.CallIncoming: "incoming", modem.CallWaiting: "waiting",
}

type audioJSON struct {
	Active      bool   `json:"active"`
	Listeners   int    `json:"listeners"`
	Error       string `json:"error,omitempty"`
	RxPerSecond int    `json:"rxPerSecond"`
	MaxGapMs    int    `json:"maxGapMs"`
	MicFrames   int64  `json:"micFrames"`
	FillFrames  int64  `json:"fillFrames"`
}

func (s *Server) toCallsJSON(snap service.Snapshot) ([]callJSON, audioJSON) {
	calls := make([]callJSON, 0, len(snap.Calls))
	for _, c := range snap.Calls {
		calls = append(calls, callJSON{c.ID, c.Incoming, callStates[c.State], c.Number, c.StartedAt, timePtr(c.AnsweredAt), c.RecordID, s.caller(c.Number)})
	}
	a := snap.Audio
	return calls, audioJSON{a.Active, a.Listeners, a.Error, a.RxPerSecond, a.MaxGapMs, a.MicFrames, a.FillFrames}
}

type callRecordJSON struct {
	ID         int64      `json:"id"`
	DeviceID   string     `json:"deviceId"`
	Peer       string     `json:"peer"`
	Number     string     `json:"number"`
	Direction  string     `json:"direction"`
	Status     string     `json:"status"`
	StartedAt  time.Time  `json:"startedAt"`
	AnsweredAt *time.Time `json:"answeredAt"`
	EndedAt    *time.Time `json:"endedAt"`
	// DurationSec is the talk time of an answered call.
	DurationSec int  `json:"durationSec"`
	Seen        bool `json:"seen"`
	// HandledBy is "assistant" for calls the AI assistant answered;
	// Summary and Transcript are its notes.
	HandledBy    string          `json:"handledBy"`
	HasRecording bool            `json:"hasRecording"`
	Summary      json.RawMessage `json:"summary"`
	Transcript   json.RawMessage `json:"transcript"`
	caller
}

func (s *Server) toCallRecordJSON(c store.CallRecord) callRecordJSON {
	out := callRecordJSON{
		ID: c.ID, DeviceID: c.DeviceID, Peer: c.Peer, Number: c.Number, Direction: c.Direction, Status: c.Status,
		StartedAt: c.StartedAt, AnsweredAt: timePtr(c.AnsweredAt), EndedAt: timePtr(c.EndedAt), Seen: c.Seen,
		HandledBy: c.HandledBy, HasRecording: c.Recording != "", Summary: rawJSON(c.Summary), Transcript: rawJSON(c.Transcript),
		caller: s.caller(c.Peer),
	}
	if !c.AnsweredAt.IsZero() && !c.EndedAt.IsZero() {
		out.DurationSec = int(c.EndedAt.Sub(c.AnsweredAt).Seconds())
	}
	return out
}

func (s *Server) listCalls(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, size := pageParams(r)
	calls, total, err := s.Store.ListCalls(r.Context(), q.Get("device"), callKind(q), size, (page-1)*size)
	if err != nil {
		s.serverError(w, err)
		return
	}
	items := make([]callRecordJSON, 0, len(calls))
	for _, c := range calls {
		items = append(items, s.toCallRecordJSON(c))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "page": page, "pageSize": size})
}

func (s *Server) markCallsSeen(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.MarkCallsSeen(r.Context()); err != nil {
		s.serverError(w, err)
		return
	}
	s.Devices.Hub().Publish(service.TopicCalls)
	ok(w)
}

func (s *Server) deleteCalls(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []int64 `json:"ids"`
	}
	if !decode(w, r, &req) {
		return
	}
	files, err := s.Store.CallRecordings(r.Context(), req.IDs)
	if err != nil {
		s.serverError(w, err)
		return
	}
	n, err := s.Store.DeleteCalls(r.Context(), req.IDs)
	if err != nil {
		s.serverError(w, err)
		return
	}
	for _, f := range files {
		os.Remove(s.recordingPath(f))
	}
	s.Devices.Hub().Publish(service.TopicCalls)
	writeJSON(w, http.StatusOK, map[string]int{"affected": n})
}

// recordingPath resolves a stored recording path inside the recordings
// directory; Clean on a rooted path cannot climb out of it.
func (s *Server) recordingPath(rel string) string {
	return filepath.Join(s.RecordingDir, filepath.Clean("/"+rel))
}

// callRecording serves a call's WAV recording, with range requests so the
// player can seek.
func (s *Server) callRecording(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的通话记录")
		return
	}
	c, err := s.Store.GetCall(r.Context(), id)
	if err != nil || c.Recording == "" || s.RecordingDir == "" {
		writeError(w, http.StatusNotFound, "这通电话没有录音")
		return
	}
	f, err := os.Open(s.recordingPath(c.Recording))
	if err != nil {
		writeError(w, http.StatusNotFound, "录音文件不存在")
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		s.serverError(w, err)
		return
	}
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	name := fmt.Sprintf("通话录音-%s-%s.wav", c.Number, c.StartedAt.Format("20060102-150405"))
	w.Header().Set("Content-Disposition", "inline; filename*=UTF-8''"+url.PathEscape(name))
	http.ServeContent(w, r, "", info.ModTime(), f)
}

func (s *Server) dial(w http.ResponseWriter, r *http.Request) {
	svc, found := s.device(w, r)
	if !found {
		return
	}
	var req struct {
		To string `json:"to"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := svc.Dial(r.Context(), req.To); err != nil {
		s.opError(w, err)
		return
	}
	ok(w)
}

func (s *Server) callAction(action func(*service.Service, context.Context) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc, found := s.device(w, r)
		if !found {
			return
		}
		if err := action(svc, r.Context()); err != nil {
			s.opError(w, err)
			return
		}
		ok(w)
	}
}

func (s *Server) dtmf(w http.ResponseWriter, r *http.Request) {
	svc, found := s.device(w, r)
	if !found {
		return
	}
	var req struct {
		Key string `json:"key"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := svc.SendDTMF(r.Context(), req.Key); err != nil {
		s.opError(w, err)
		return
	}
	ok(w)
}

type forwardingJSON struct {
	Reason  int    `json:"reason"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Number  string `json:"number"`
}

// getForwarding asks the network; it can take several seconds.
func (s *Server) getForwarding(w http.ResponseWriter, r *http.Request) {
	svc, found := s.device(w, r)
	if !found {
		return
	}
	list, err := svc.Forwarding(r.Context())
	if err != nil {
		s.opError(w, err)
		return
	}
	out := make([]forwardingJSON, 0, len(list))
	for _, f := range list {
		out = append(out, forwardingJSON{f.Reason, modem.ForwardReasons[f.Reason], f.Enabled, f.Number})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) putForwarding(w http.ResponseWriter, r *http.Request) {
	svc, found := s.device(w, r)
	if !found {
		return
	}
	var req forwardingJSON
	if !decode(w, r, &req) {
		return
	}
	if _, known := modem.ForwardReasons[req.Reason]; !known {
		writeError(w, http.StatusBadRequest, "未知的转移类型")
		return
	}
	if err := svc.SetForwarding(r.Context(), req.Reason, req.Enabled, req.Number); err != nil {
		s.opError(w, err)
		return
	}
	ok(w)
}

// callAudio streams call audio over a WebSocket: binary messages of 8 kHz,
// 16-bit little-endian mono PCM in both directions. The browser does the
// resampling.
func (s *Server) callAudio(w http.ResponseWriter, r *http.Request) {
	svc, found := s.device(w, r)
	if !found {
		return
	}
	// Accept checks that the page and the socket share an origin.
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	stream := svc.AttachAudio()
	defer stream.Close()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		defer cancel()
		for {
			kind, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			if kind == websocket.MessageBinary && len(data) > 0 && len(data) <= 8000 {
				stream.Write(data)
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			conn.Close(websocket.StatusNormalClosure, "")
			return
		case frame := <-stream.Frames():
			wctx, done := context.WithTimeout(ctx, 2*time.Second)
			err := conn.Write(wctx, websocket.MessageBinary, frame)
			done()
			if err != nil {
				return
			}
		}
	}
}

func (s *Server) routeCalls(authed func(string, http.HandlerFunc)) {
	authed("GET /api/calls", s.listCalls)
	authed("POST /api/calls/seen", s.markCallsSeen)
	authed("GET /api/calls/callbacks", s.callbacks)
	authed("POST /api/calls/{id}/callback-done", s.callbackDone)
	authed("POST /api/calls/delete", s.deleteCalls)
	authed("GET /api/calls/{id}/recording", s.callRecording)
	authed("POST /api/devices/{id}/call/dial", s.dial)
	authed("POST /api/devices/{id}/call/answer", s.callAction((*service.Service).Answer))
	authed("POST /api/devices/{id}/call/hangup", s.callAction((*service.Service).Hangup))
	authed("POST /api/devices/{id}/call/dtmf", s.dtmf)
	authed("GET /api/devices/{id}/call/audio", s.callAudio)
	authed("GET /api/devices/{id}/forwarding", s.getForwarding)
	authed("PUT /api/devices/{id}/forwarding", s.putForwarding)
}

// callKind reads the call log filter: ?kind=missed|assistant, or the older
// ?missed=1.
func callKind(q url.Values) string {
	if q.Get("missed") == "1" {
		return store.CallsMissed
	}
	switch k := q.Get("kind"); k {
	case store.CallsMissed, store.CallsAssistant:
		return k
	}
	return ""
}

// callbackJSON is a call that may need a call back, with why.
type callbackJSON struct {
	callRecordJSON
	Reason   string `json:"reason"`   // what the call was about
	Callback string `json:"callback"` // the assistant's note on calling back
	Urgency  string `json:"urgency"`
}

// needsCallback reads the assistant's callback note; "不需要" and the like
// mean no.
func needsCallback(note string) bool {
	note = strings.TrimSpace(note)
	if note == "" {
		return false
	}
	if note == "无" || note == "否" {
		return false
	}
	for _, no := range []string{"不需要", "无需", "不用"} {
		if strings.HasPrefix(note, no) {
			return false
		}
	}
	return true
}

// callbacks lists the calls of the last 3 days still waiting for a call
// back: missed calls, and calls the assistant says need one.
func (s *Server) callbacks(w http.ResponseWriter, r *http.Request) {
	calls, err := s.Store.CallbackCandidates(r.Context(), time.Now().Add(-72*time.Hour))
	if err != nil {
		s.serverError(w, err)
		return
	}
	items := []callbackJSON{}
	for _, c := range calls {
		item := callbackJSON{callRecordJSON: s.toCallRecordJSON(c)}
		if c.HandledBy == store.HandledByAssistant {
			var sum struct{ Brief, Purpose, Callback, Urgency string }
			if json.Unmarshal([]byte(c.Summary), &sum) != nil || !needsCallback(sum.Callback) {
				continue
			}
			item.Reason, item.Callback, item.Urgency = cmp.Or(sum.Brief, sum.Purpose), sum.Callback, sum.Urgency
		} else {
			item.Reason = "未接来电"
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) callbackDone(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "通话编号无效")
		return
	}
	if err := s.Store.MarkCallbackDone(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "通话记录不存在")
		return
	} else if err != nil {
		s.serverError(w, err)
		return
	}
	s.Devices.Hub().Publish(service.TopicCalls)
	ok(w)
}
