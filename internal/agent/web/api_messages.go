package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/f0rmatting/haobot/internal/agent/classify"
	"github.com/f0rmatting/haobot/internal/agent/service"
	"github.com/f0rmatting/haobot/internal/agent/store"
)

const (
	defaultPageSize = 50
	maxPageSize     = 200
	maxBulk         = 1000
)

type messageJSON struct {
	ID             int64      `json:"id"`
	Sender         string     `json:"sender"`
	Body           string     `json:"body"`
	SentAt         time.Time  `json:"sentAt"`
	ReceivedAt     time.Time  `json:"receivedAt"`
	Parts          int        `json:"parts"`
	Complete       bool       `json:"complete"`
	Binary         bool       `json:"binary"`
	Kind           string     `json:"kind"`
	MMSURL         string     `json:"mmsUrl,omitempty"`
	MMSSize        int64      `json:"mmsSize,omitempty"`
	MMSExpiry      *time.Time `json:"mmsExpiry,omitempty"`
	Category       string     `json:"category"`
	CategorySource string     `json:"categorySource"`
	Read           bool       `json:"read"`
	DeviceID       string     `json:"deviceId"`
	Peer           string     `json:"peer"`
	Direction      string     `json:"direction"`
	Status         string     `json:"status,omitempty"`
	Error          string     `json:"error,omitempty"`
	// Code is the verification code found in the body, for one-tap copying.
	Code string `json:"code,omitempty"`
	caller
}

func (s *Server) toMessageJSON(m store.Message) messageJSON {
	out := messageJSON{
		ID: m.ID, Sender: m.Sender, Body: m.Body, SentAt: m.SentAt, ReceivedAt: m.ReceivedAt,
		Parts: m.Parts, Complete: m.Complete, Binary: m.Binary, Kind: m.Kind,
		MMSURL: m.MMSURL, MMSSize: m.MMSSize, MMSExpiry: timePtr(m.MMSExpiry),
		Category: m.Category, CategorySource: m.CategorySource, Read: m.Read,
		DeviceID: m.DeviceID, Peer: m.Peer, Direction: m.Direction, Status: m.Status, Error: m.Error,
		caller: s.messageCaller(m),
	}
	if m.Direction == store.DirectionIn && !m.Binary && m.Kind == store.KindSMS && (m.Category == classify.Verification || classify.RuleVerification(m.Body)) {
		out.Code = classify.ExtractCode(m.Body)
	}
	return out
}

func (s *Server) listMessages(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	page = max(page, 1)
	size, _ := strconv.Atoi(q.Get("pageSize"))
	if size <= 0 || size > maxPageSize {
		size = defaultPageSize
	}
	filter := store.Filter{
		Query:    q.Get("q"),
		Category: q.Get("category"),
		Since:    since(q.Get("since")),
		Sender:   q.Get("sender"),
		Unread:   q.Get("unread") == "1" || q.Get("unread") == "true",
		DeviceID: q.Get("device"),
	}
	messages, total, err := s.Store.ListMessages(r.Context(), filter, size, (page-1)*size)
	if err != nil {
		s.serverError(w, err)
		return
	}
	items := make([]messageJSON, 0, len(messages))
	for _, m := range messages {
		items = append(items, s.toMessageJSON(m))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items, "total": total, "page": page, "pageSize": size,
	})
}

func (s *Server) messageID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "无效的短信编号")
		return 0, false
	}
	return id, true
}

func (s *Server) loadMessage(w http.ResponseWriter, r *http.Request) (store.Message, bool) {
	id, valid := s.messageID(w, r)
	if !valid {
		return store.Message{}, false
	}
	m, err := s.Store.GetMessage(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "短信不存在或已删除")
		return m, false
	}
	if err != nil {
		s.serverError(w, err)
		return m, false
	}
	return m, true
}

func (s *Server) getMessage(w http.ResponseWriter, r *http.Request) {
	m, found := s.loadMessage(w, r)
	if !found {
		return
	}
	writeJSON(w, http.StatusOK, s.toMessageJSON(m))
}

// redactedMessage shows exactly what the classifier would send to a model.
func (s *Server) redactedMessage(w http.ResponseWriter, r *http.Request) {
	m, found := s.loadMessage(w, r)
	if !found {
		return
	}
	text, findings := classify.Redact(m.Body)
	writeJSON(w, http.StatusOK, map[string]any{
		"sender": classify.RedactSender(m.Sender), "text": text, "findings": findings,
	})
}

func (s *Server) patchMessage(w http.ResponseWriter, r *http.Request) {
	id, valid := s.messageID(w, r)
	if !valid {
		return
	}
	var req struct {
		Read     *bool   `json:"read"`
		Category *string `json:"category"`
	}
	if !decode(w, r, &req) {
		return
	}
	ctx := r.Context()
	if req.Category != nil {
		category := strings.TrimSpace(*req.Category)
		cfg, err := s.Settings.LLM(ctx)
		if err != nil {
			s.serverError(w, err)
			return
		}
		if !cfg.Has(category) {
			writeError(w, http.StatusBadRequest, "未知的类别")
			return
		}
		if err := s.Store.SetCategory(ctx, id, category, classify.SourceManual, time.Now()); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusNotFound, "短信不存在或已删除")
				return
			}
			s.serverError(w, err)
			return
		}
		s.Store.AddClassifyLog(ctx, store.ClassifyLog{MessageID: id, At: time.Now(), Source: classify.SourceManual, Category: category, Rule: "在管理后台手动修改"})
	}
	if req.Read != nil {
		if err := s.Store.MarkRead(ctx, []int64{id}, *req.Read); err != nil {
			s.serverError(w, err)
			return
		}
	}
	s.Devices.Hub().Publish(service.TopicMessages)
	s.getMessage(w, r)
}

func (s *Server) deleteMessage(w http.ResponseWriter, r *http.Request) {
	id, valid := s.messageID(w, r)
	if !valid {
		return
	}
	n, err := s.Store.DeleteMessages(r.Context(), []int64{id})
	if err != nil {
		s.serverError(w, err)
		return
	}
	if n == 0 {
		writeError(w, http.StatusNotFound, "短信不存在或已删除")
		return
	}
	s.Devices.Hub().Publish(service.TopicMessages)
	ok(w)
}

// bulkMessages applies one action to several messages. "read" with no ids
// marks everything read.
func (s *Server) bulkMessages(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action string  `json:"action"`
		IDs    []int64 `json:"ids"`
	}
	if !decode(w, r, &req) {
		return
	}
	if len(req.IDs) > maxBulk {
		writeError(w, http.StatusBadRequest, "一次最多处理 1000 条")
		return
	}
	ctx := r.Context()
	var n int
	var err error
	switch req.Action {
	case "delete":
		if len(req.IDs) == 0 {
			writeError(w, http.StatusBadRequest, "请选择要删除的短信")
			return
		}
		n, err = s.Store.DeleteMessages(ctx, req.IDs)
		if err == nil {
			s.Devices.Note(ctx, "info", "已删除 "+strconv.Itoa(n)+" 条短信")
		}
	case "read", "unread":
		err = s.Store.MarkRead(ctx, req.IDs, req.Action == "read")
		n = len(req.IDs)
	case "reclassify":
		if len(req.IDs) == 0 {
			writeError(w, http.StatusBadRequest, "请选择要重新分类的短信")
			return
		}
		n, err = s.Store.ResetCategories(ctx, req.IDs)
		s.Worker.Wake()
	default:
		writeError(w, http.StatusBadRequest, "未知操作")
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	s.Devices.Hub().Publish(service.TopicMessages)
	writeJSON(w, http.StatusOK, map[string]int{"affected": n})
}

// since reads a ?since= filter: "today" for local midnight, or a Unix
// time.
func since(v string) time.Time {
	if v == "today" {
		now := time.Now()
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
		return time.Unix(n, 0)
	}
	return time.Time{}
}

func pageParams(r *http.Request) (page, size int) {
	q := r.URL.Query()
	page, _ = strconv.Atoi(q.Get("page"))
	page = max(page, 1)
	size, _ = strconv.Atoi(q.Get("pageSize"))
	if size <= 0 || size > maxPageSize {
		size = defaultPageSize
	}
	return page, size
}

type conversationJSON struct {
	DeviceID string      `json:"deviceId"`
	Peer     string      `json:"peer"`
	Total    int         `json:"total"`
	Unread   int         `json:"unread"`
	LastAt   time.Time   `json:"lastAt"`
	Last     messageJSON `json:"last"`
	caller
}

// listConversations groups messages by device and number.
func (s *Server) listConversations(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, size := pageParams(r)
	filter := store.Filter{
		Query:    q.Get("q"),
		Category: q.Get("category"),
		Since:    since(q.Get("since")),
		Unread:   q.Get("unread") == "1",
		DeviceID: q.Get("device"),
	}
	convs, total, err := s.Store.ListConversations(r.Context(), filter, size, (page-1)*size)
	if err != nil {
		s.serverError(w, err)
		return
	}
	items := make([]conversationJSON, 0, len(convs))
	for _, c := range convs {
		items = append(items, conversationJSON{c.DeviceID, c.Peer, c.Total, c.Unread, c.LastAt, s.toMessageJSON(c.Last), s.messageCaller(c.Last)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "page": page, "pageSize": size})
}

// conversationMessages returns one conversation newest first; the UI shows
// pages in reverse.
func (s *Server) conversationMessages(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	device, peer := q.Get("device"), store.NormalizePeer(q.Get("peer"))
	if peer == "" {
		writeError(w, http.StatusBadRequest, "缺少号码")
		return
	}
	page, size := pageParams(r)
	messages, total, err := s.Store.ConversationMessages(r.Context(), device, peer, size, (page-1)*size)
	if err != nil {
		s.serverError(w, err)
		return
	}
	items := make([]messageJSON, 0, len(messages))
	for _, m := range messages {
		items = append(items, s.toMessageJSON(m))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "page": page, "pageSize": size})
}

type conversationRequest struct {
	DeviceID string `json:"deviceId"`
	Peer     string `json:"peer"`
}

func (s *Server) readConversation(w http.ResponseWriter, r *http.Request) {
	var req conversationRequest
	if !decode(w, r, &req) {
		return
	}
	n, err := s.Store.MarkConversationRead(r.Context(), req.DeviceID, store.NormalizePeer(req.Peer))
	if err != nil {
		s.serverError(w, err)
		return
	}
	if n > 0 {
		s.Devices.Hub().Publish(service.TopicMessages)
	}
	writeJSON(w, http.StatusOK, map[string]int{"affected": n})
}

func (s *Server) deleteConversation(w http.ResponseWriter, r *http.Request) {
	var req conversationRequest
	if !decode(w, r, &req) {
		return
	}
	peer := store.NormalizePeer(req.Peer)
	n, err := s.Store.DeleteConversation(r.Context(), req.DeviceID, peer)
	if err != nil {
		s.serverError(w, err)
		return
	}
	s.Devices.Note(r.Context(), "info", "已删除与 "+peer+" 的会话（"+strconv.Itoa(n)+" 条）")
	s.Devices.Hub().Publish(service.TopicMessages)
	writeJSON(w, http.StatusOK, map[string]int{"affected": n})
}

// sendMessage queues an SMS; the answer comes back with status "sending"
// and the outcome arrives over the event stream.
func (s *Server) sendMessage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeviceID string `json:"deviceId"`
		To       string `json:"to"`
		Text     string `json:"text"`
	}
	if !decode(w, r, &req) {
		return
	}
	svc := s.Devices.Device(req.DeviceID)
	if svc == nil {
		writeError(w, http.StatusServiceUnavailable, "设备未连接，无法发送")
		return
	}
	msg, err := svc.Send(r.Context(), req.To, req.Text)
	if err != nil {
		s.opError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.toMessageJSON(msg))
}

func (s *Server) resendMessage(w http.ResponseWriter, r *http.Request) {
	m, found := s.loadMessage(w, r)
	if !found {
		return
	}
	svc := s.Devices.Device(m.DeviceID)
	if svc == nil {
		writeError(w, http.StatusServiceUnavailable, "设备未连接，无法发送")
		return
	}
	msg, err := svc.Resend(r.Context(), m)
	if err != nil {
		s.opError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.toMessageJSON(msg))
}
