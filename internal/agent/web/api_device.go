package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/f0rmatting/haobot/internal/agent/modem"
	"github.com/f0rmatting/haobot/internal/agent/service"
	"github.com/f0rmatting/haobot/internal/agent/store"
)

// statusJSON is the dashboard and device page payload.
type statusJSON struct {
	Devices []deviceJSON `json:"devices"`
	// Orphans counts messages from before devices were tracked that no
	// device has been assigned yet.
	Orphans int `json:"orphans"`
	// MissedCalls counts missed calls nobody has looked at.
	MissedCalls int       `json:"missedCalls"`
	Messages    statsJSON `json:"messages"`
	Classifier  struct {
		Enabled   bool   `json:"enabled"`
		Ready     bool   `json:"ready"`
		LastError string `json:"lastError"`
	} `json:"classifier"`
	ScanModes map[int]string `json:"scanModes"`
}

// deviceJSON is one modem: its stored record plus, while it is connected,
// the live state.
type deviceJSON struct {
	ID         string        `json:"id"`
	Name       string        `json:"name"`
	Model      string        `json:"model"`
	IMEI       string        `json:"imei"`
	Enabled    bool          `json:"enabled"`
	Online     bool          `json:"online"` // a service is running for it
	Connected  bool          `json:"connected"`
	Endpoint   string        `json:"endpoint"`
	LastError  string        `json:"lastError"`
	SMSReady   bool          `json:"smsReady"`
	LastSync   *time.Time    `json:"lastSync"`
	LastSeen   *time.Time    `json:"lastSeen"`
	PINBlocked bool          `json:"pinBlocked"`
	Unread     int           `json:"unread"`
	Modem      modemJSON     `json:"modem"`
	Identity   identityJSON  `json:"identity"`
	Health     healthJSON    `json:"health"`
	Calls      []callJSON    `json:"calls"`
	Audio      audioJSON     `json:"audio"`
	Assistant  assistantJSON `json:"assistant"`
}

type recoveryJSON struct {
	At     time.Time `json:"at"`
	Action string    `json:"action"`
	Reason string    `json:"reason"`
	Error  string    `json:"error,omitempty"`
}

type selfTestJSON struct {
	State      string     `json:"state"`
	To         string     `json:"to"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt"`
	DelaySec   int        `json:"delaySec"`
	Detail     string     `json:"detail"`
}

type healthJSON struct {
	// Level sums the rest up: ok, warn or bad.
	Level             string         `json:"level"`
	Summary           string         `json:"summary"`
	LastPing          *time.Time     `json:"lastPing"`
	PingLatencyMs     int64          `json:"pingLatencyMs"`
	PingFailures      int            `json:"pingFailures"`
	UnregisteredSince *time.Time     `json:"unregisteredSince"`
	Recoveries        []recoveryJSON `json:"recoveries"`
	AutoRecover       bool           `json:"autoRecover"`
	Suspended         bool           `json:"suspended"`
	SelfTest          *selfTestJSON  `json:"selfTest"`
	SelfTestTarget    string         `json:"selfTestTarget"`
}

// toHealthJSON judges a connected device's health. Self-test failures count
// for a day; recoveries for an hour.
func toHealthJSON(snap service.Snapshot, now time.Time) healthJSON {
	h := snap.Health
	out := healthJSON{
		LastPing: timePtr(h.LastPing), PingLatencyMs: h.PingLatency.Milliseconds(), PingFailures: h.PingFailures,
		UnregisteredSince: timePtr(h.UnregisteredSince), AutoRecover: h.AutoRecover, Suspended: h.Suspended,
		Recoveries: []recoveryJSON{},
	}
	for _, r := range h.Recoveries {
		out.Recoveries = append(out.Recoveries, recoveryJSON{r.At, r.Action, r.Reason, r.Err})
	}
	if t := h.SelfTest; t != nil {
		out.SelfTest = &selfTestJSON{t.State, t.To, t.StartedAt, timePtr(t.FinishedAt), int(t.Delay.Seconds() + 0.5), t.Detail}
	}
	recent := len(h.Recoveries) > 0 && now.Sub(h.Recoveries[0].At) < time.Hour
	failedTest := h.SelfTest != nil && h.SelfTest.State == "failed" && now.Sub(h.SelfTest.FinishedAt) < 24*time.Hour
	switch {
	case !snap.Connected:
		out.Level, out.Summary = "bad", "未连接"
	case h.Suspended:
		out.Level, out.Summary = "bad", "多次自动重启仍未恢复，已暂停自动处理"
	case h.PingFailures >= 2:
		out.Level, out.Summary = "bad", "模组响应异常，可能卡死"
	case failedTest:
		out.Level, out.Summary = "bad", "最近一次收发自检未通过"
	case h.PingFailures == 1:
		out.Level, out.Summary = "warn", "有一次心跳没有响应"
	case !h.UnregisteredSince.IsZero():
		out.Level, out.Summary = "warn", "未注册到网络"
	case recent && h.Recoveries[0].Err != "":
		out.Level, out.Summary = "warn", "最近一次自动"+h.Recoveries[0].Action+"失败，已改用重新连接恢复"
	case recent:
		out.Level, out.Summary = "warn", "最近 1 小时内自动恢复过："+h.Recoveries[0].Action
	case h.PingLatency > 2*time.Second:
		out.Level, out.Summary = "warn", "模组响应偏慢"
	default:
		out.Level, out.Summary = "ok", "运行正常"
	}
	return out
}

type modemJSON struct {
	Model         string     `json:"model"`
	Revision      string     `json:"revision"`
	SIM           string     `json:"sim"`
	PINRetries    int        `json:"pinRetries"`
	PUKRetries    int        `json:"pukRetries"`
	Operator      string     `json:"operator"`
	AccessTech    string     `json:"accessTech"`
	Band          string     `json:"band"`
	Registration  string     `json:"registration"`
	Registered    bool       `json:"registered"`
	CSQ           int        `json:"csq"`
	SignalDBM     int        `json:"signalDbm"`
	StorageUsed   int        `json:"storageUsed"`
	StorageTotal  int        `json:"storageTotal"`
	Functionality int        `json:"functionality"`
	PINLock       int        `json:"pinLock"`
	Attached      int        `json:"attached"`
	APN           string     `json:"apn"`
	IP            string     `json:"ip"`
	ScanMode      int        `json:"scanMode"`
	Cell          cellJSON   `json:"cell"`
	UpdatedAt     *time.Time `json:"updatedAt"`
}

type cellJSON struct {
	Valid  bool   `json:"valid"`
	State  string `json:"state"`
	RAT    string `json:"rat"`
	MCC    string `json:"mcc"`
	MNC    string `json:"mnc"`
	CellID string `json:"cellId"`
	PCI    int    `json:"pci"`
	EARFCN int    `json:"earfcn"`
	Band   int    `json:"band"`
	TAC    string `json:"tac"`
	RSRP   int    `json:"rsrp"`
	RSRQ   int    `json:"rsrq"`
	RSSI   int    `json:"rssi"`
	SINR   int    `json:"sinr"`
	Raw    string `json:"raw"`
}

type identityJSON struct {
	IMEI   string `json:"imei"`
	IMSI   string `json:"imsi"`
	ICCID  string `json:"iccid"`
	Number string `json:"number"`
	SMSC   string `json:"smsc"`
}

type statsJSON struct {
	Total      int            `json:"total"`
	Unread     int            `json:"unread"`
	Today      int            `json:"today"`
	Pending    int            `json:"pending"`
	Categories map[string]int `json:"categories"`
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func toModemJSON(m modem.Status) modemJSON {
	return modemJSON{
		Model: m.Model, Revision: m.Revision, SIM: m.SIM, PINRetries: m.PINRetries, PUKRetries: m.PUKRetries,
		Operator: m.Operator, AccessTech: m.AccessTech, Band: m.Band, Registration: m.Registration,
		Registered: m.Registered, CSQ: m.CSQ, SignalDBM: m.SignalDBM, StorageUsed: m.StorageUsed,
		StorageTotal: m.StorageTotal, Functionality: m.Functionality, PINLock: m.PINLock,
		Attached: m.Attached, APN: m.APN, IP: m.IP, ScanMode: m.ScanMode, UpdatedAt: timePtr(m.UpdatedAt),
		Cell: cellJSON{
			Valid: m.Cell.Valid, State: m.Cell.State, RAT: m.Cell.RAT, MCC: m.Cell.MCC, MNC: m.Cell.MNC,
			CellID: m.Cell.CellID, PCI: m.Cell.PCI, EARFCN: m.Cell.EARFCN, Band: m.Cell.Band, TAC: m.Cell.TAC,
			RSRP: m.Cell.RSRP, RSRQ: m.Cell.RSRQ, RSSI: m.Cell.RSSI, SINR: m.Cell.SINR, Raw: m.Cell.Raw,
		},
	}
}

func liveDevice(out *deviceJSON, snap service.Snapshot) {
	out.Online = true
	out.Health = toHealthJSON(snap, time.Now())
	out.Calls, out.Audio = toCallsJSON(snap)
	out.Assistant = toAssistantJSON(snap.Assistant)
	out.Connected, out.Endpoint, out.LastError, out.SMSReady = snap.Connected, snap.Endpoint, snap.LastError, snap.SMSReady
	out.LastSync, out.PINBlocked = timePtr(snap.LastSync), snap.PINBlocked
	out.Modem = toModemJSON(snap.Modem)
	out.Identity = identityJSON{
		IMEI: snap.Identity.IMEI, IMSI: snap.Identity.IMSI, ICCID: snap.Identity.ICCID,
		Number: snap.Identity.Number, SMSC: snap.Identity.SMSC,
	}
}

// devices merges stored devices with running services. Ports that have not
// answered yet appear with their endpoint as id.
func (s *Server) devices(r *http.Request, unread map[string]int) ([]deviceJSON, error) {
	stored, err := s.Store.Devices(r.Context())
	if err != nil {
		return nil, err
	}
	live := map[string]service.Snapshot{}
	var pending []service.Snapshot
	for _, svc := range s.Devices.Services() {
		snap := svc.Snapshot()
		if snap.Device.ID == "" {
			pending = append(pending, snap)
			continue
		}
		live[snap.Device.ID] = snap
	}
	out := make([]deviceJSON, 0, len(stored)+len(pending))
	for _, d := range stored {
		j := deviceJSON{
			Calls: []callJSON{},
			ID:    d.ID, Name: d.Name, Model: d.Model, IMEI: d.IMEI, Enabled: d.Enabled,
			Endpoint: d.Endpoint, LastSeen: timePtr(d.LastSeen), Unread: unread[d.ID],
			Modem: toModemJSON(modem.Status{PINRetries: -1, PUKRetries: -1, CSQ: 99, StorageUsed: -1, StorageTotal: -1, Functionality: -1, PINLock: -1, Attached: -1, ScanMode: -1}),
		}
		j.Health = healthJSON{Level: "bad", Summary: "离线", Recoveries: []recoveryJSON{}}
		if snap, ok := live[d.ID]; ok {
			liveDevice(&j, snap)
		}
		j.Health.SelfTestTarget, _ = s.Settings.SelfTestTarget(r.Context(), d.ID)
		if j.Health.SelfTestTarget == "" {
			j.Health.SelfTestTarget = j.Identity.Number
		}
		out = append(out, j)
	}
	for _, snap := range pending {
		j := deviceJSON{ID: "", Name: "正在连接…", Enabled: true}
		liveDevice(&j, snap)
		out = append(out, j)
	}
	return out, nil
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	stats, err := s.Store.MessageStats(r.Context(), time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()))
	if err != nil {
		s.serverError(w, err)
		return
	}
	devices, err := s.devices(r, stats.UnreadByDevice)
	if err != nil {
		s.serverError(w, err)
		return
	}
	orphans, err := s.Store.Orphans(r.Context())
	if err != nil {
		s.serverError(w, err)
		return
	}
	missed, err := s.Store.UnseenMissedCalls(r.Context())
	if err != nil {
		s.serverError(w, err)
		return
	}
	out := statusJSON{Devices: devices, Orphans: orphans, MissedCalls: missed, ScanModes: modem.ScanModes}
	out.Messages = statsJSON{stats.Total, stats.Unread, stats.Today, stats.Pending, stats.Categories}
	if cfg, err := s.Settings.LLM(r.Context()); err == nil {
		out.Classifier.Enabled, out.Classifier.Ready = cfg.Enabled, cfg.Ready()
	}
	out.Classifier.LastError = s.Worker.Status().LastError
	writeJSON(w, http.StatusOK, out)
}

// device finds the connected service for the {id} in the path. It answers
// the request itself when the device is unknown or offline.
func (s *Server) device(w http.ResponseWriter, r *http.Request) (*service.Service, bool) {
	id := r.PathValue("id")
	if _, err := s.Store.GetDevice(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "设备不存在")
		} else {
			s.serverError(w, err)
		}
		return nil, false
	}
	svc := s.Devices.Device(id)
	if svc == nil {
		writeError(w, http.StatusServiceUnavailable, "设备未连接")
		return nil, false
	}
	return svc, true
}

func (s *Server) patchDevice(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    *string `json:"name"`
		Enabled *bool   `json:"enabled"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" || len([]rune(name)) > 24 {
			writeError(w, http.StatusBadRequest, "名称不能为空，且不超过 24 个字")
			return
		}
		req.Name = &name
	}
	id := r.PathValue("id")
	if err := s.Store.UpdateDevice(r.Context(), id, req.Name, req.Enabled); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "设备不存在")
			return
		}
		s.serverError(w, err)
		return
	}
	if req.Enabled != nil {
		state := "启用"
		if !*req.Enabled {
			state = "停用（不再同步短信）"
		}
		s.Devices.Note(r.Context(), "info", "设备 "+id+" 已"+state)
	}
	if svc := s.Devices.Device(id); svc != nil {
		svc.Refresh(r.Context())
	}
	s.Devices.Hub().Publish(service.TopicStatus)
	ok(w)
}

// adoptOrphans assigns messages from before devices were tracked to a device.
func (s *Server) adoptOrphans(w http.ResponseWriter, r *http.Request) {
	id, found := s.knownDevice(w, r)
	if !found {
		return
	}
	n, err := s.Store.AdoptOrphans(r.Context(), id)
	if err != nil {
		s.serverError(w, err)
		return
	}
	s.Devices.Note(r.Context(), "info", "已将升级前的 "+strconv.Itoa(n)+" 条短信归属到设备 "+id)
	s.Devices.Hub().Publish(service.TopicMessages)
	writeJSON(w, http.StatusOK, map[string]int{"affected": n})
}

// selfTest starts a manual send-and-receive check; the result arrives in the
// device's health.
func (s *Server) selfTest(w http.ResponseWriter, r *http.Request) {
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
	to, err := modem.CleanNumber(req.To)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Devices.SelfTest(r.Context(), svc.DeviceID(), to); err != nil {
		s.opError(w, err)
		return
	}
	s.Settings.SetSelfTestTarget(r.Context(), svc.DeviceID(), to)
	ok(w)
}

// setRecovery turns automatic recovery on or off; turning it on also lifts
// a suspension.
func (s *Server) setRecovery(w http.ResponseWriter, r *http.Request) {
	id, found := s.knownDevice(w, r)
	if !found {
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := s.Settings.SetAutoRecover(r.Context(), id, req.Enabled); err != nil {
		s.serverError(w, err)
		return
	}
	if svc := s.Devices.Device(id); svc != nil {
		if req.Enabled {
			svc.ResumeRecovery()
		}
		svc.Note(r.Context(), "info", map[bool]string{true: "已开启自动恢复", false: "已关闭自动恢复（卡死时只告警）"}[req.Enabled])
		svc.Refresh(r.Context())
	}
	ok(w)
}

// forgetDevice removes an offline device from the list; messages stay.
func (s *Server) forgetDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.Devices.Device(id) != nil {
		writeError(w, http.StatusConflict, "设备在线，不能移除")
		return
	}
	if err := s.Store.ForgetDevice(r.Context(), id); err != nil {
		s.serverError(w, err)
		return
	}
	s.Devices.Hub().Publish(service.TopicStatus)
	ok(w)
}

type eventJSON struct {
	ID    int64     `json:"id"`
	At    time.Time `json:"at"`
	Level string    `json:"level"`
	Text  string    `json:"text"`
}

func toEventJSON(events []store.Event) []eventJSON {
	out := make([]eventJSON, 0, len(events))
	for _, e := range events {
		out = append(out, eventJSON{e.ID, e.At, e.Level, e.Text})
	}
	return out
}

// events returns the latest events, or with ?page= a filtered page of them
// and the count per level.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("page") == "" {
		limit, _ := strconv.Atoi(q.Get("limit"))
		if limit <= 0 || limit > 500 {
			limit = 200
		}
		events, err := s.Store.RecentEvents(r.Context(), limit)
		if err != nil {
			s.serverError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toEventJSON(events))
		return
	}
	page, size := pageParams(r)
	events, total, err := s.Store.ListEvents(r.Context(), store.EventFilter{Level: q.Get("level"), Query: q.Get("q")}, size, (page-1)*size)
	if err != nil {
		s.serverError(w, err)
		return
	}
	counts, err := s.Store.EventCounts(r.Context())
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": toEventJSON(events), "total": total, "page": page, "pageSize": size, "counts": counts})
}

// stream pushes change notifications as server-sent events; the UI refetches
// whatever changed.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	flusher, okFlush := w.(http.Flusher)
	if !okFlush {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("X-Accel-Buffering", "no")
	topics, cancel := s.Devices.Hub().Subscribe()
	defer cancel()
	fmt.Fprint(w, "retry: 3000\n\n")
	flusher.Flush()
	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()
	// Status changes arrive on every poll; coalesce bursts.
	pending := map[string]bool{}
	flush := time.NewTimer(time.Hour)
	flush.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case topic := <-topics:
			if len(pending) == 0 {
				flush.Reset(200 * time.Millisecond)
			}
			pending[topic] = true
		case <-flush.C:
			for topic := range pending {
				fmt.Fprintf(w, "event: %s\ndata: {}\n\n", topic)
			}
			clear(pending)
			flusher.Flush()
		case <-heartbeat.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func (s *Server) deviceRefresh(w http.ResponseWriter, r *http.Request) {
	svc, found := s.device(w, r)
	if !found {
		return
	}
	if err := svc.Refresh(r.Context()); err != nil {
		s.opError(w, err)
		return
	}
	ok(w)
}

func (s *Server) deviceReboot(w http.ResponseWriter, r *http.Request) {
	svc, found := s.device(w, r)
	if !found {
		return
	}
	if err := svc.Reboot(r.Context()); err != nil {
		s.opError(w, err)
		return
	}
	ok(w)
}

func (s *Server) deviceRadio(w http.ResponseWriter, r *http.Request) {
	svc, found := s.device(w, r)
	if !found {
		return
	}
	var req struct {
		On bool `json:"on"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := svc.SetRadio(r.Context(), req.On); err != nil {
		s.opError(w, err)
		return
	}
	ok(w)
}

func (s *Server) deviceScanMode(w http.ResponseWriter, r *http.Request) {
	svc, found := s.device(w, r)
	if !found {
		return
	}
	var req struct {
		Mode int `json:"mode"`
	}
	if !decode(w, r, &req) {
		return
	}
	if _, known := modem.ScanModes[req.Mode]; !known {
		writeError(w, http.StatusBadRequest, "未知的网络制式")
		return
	}
	if err := svc.SetScanMode(r.Context(), req.Mode); err != nil {
		s.opError(w, err)
		return
	}
	ok(w)
}

func (s *Server) deviceConsole(w http.ResponseWriter, r *http.Request) {
	svc, found := s.device(w, r)
	if !found {
		return
	}
	var req struct {
		Command string `json:"command"`
	}
	if !decode(w, r, &req) {
		return
	}
	lines, err := svc.Console(r.Context(), req.Command)
	if err != nil {
		s.opError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": lines})
}

type pinRequest struct {
	PIN    string `json:"pin"`
	OldPIN string `json:"oldPin"`
	NewPIN string `json:"newPin"`
	PUK    string `json:"puk"`
	// Enabled is the wanted PIN lock state for /api/sim/lock.
	Enabled bool `json:"enabled"`
	// Force allows using the last PIN attempt.
	Force bool `json:"force"`
	// Save stores the (new) PIN for automatic unlocking.
	Save bool `json:"save"`
}

func (s *Server) savePIN(r *http.Request, svc *service.Service, pin string) error {
	if err := s.Settings.SetSIM(r.Context(), svc.DeviceID(), true, &pin); err != nil {
		return err
	}
	svc.ResumeAutoUnlock()
	return nil
}

func (s *Server) simPIN(w http.ResponseWriter, r *http.Request) {
	svc, found := s.device(w, r)
	if !found {
		return
	}
	var req pinRequest
	if !decode(w, r, &req) {
		return
	}
	if err := svc.EnterPIN(r.Context(), strings.TrimSpace(req.PIN), req.Force); err != nil {
		s.opError(w, err)
		return
	}
	if req.Save {
		if err := s.savePIN(r, svc, strings.TrimSpace(req.PIN)); err != nil {
			s.serverError(w, err)
			return
		}
	}
	ok(w)
}

func (s *Server) simPUK(w http.ResponseWriter, r *http.Request) {
	svc, found := s.device(w, r)
	if !found {
		return
	}
	var req pinRequest
	if !decode(w, r, &req) {
		return
	}
	if err := svc.EnterPUK(r.Context(), strings.TrimSpace(req.PUK), strings.TrimSpace(req.NewPIN)); err != nil {
		s.opError(w, err)
		return
	}
	if req.Save {
		if err := s.savePIN(r, svc, strings.TrimSpace(req.NewPIN)); err != nil {
			s.serverError(w, err)
			return
		}
	}
	ok(w)
}

func (s *Server) simLock(w http.ResponseWriter, r *http.Request) {
	svc, found := s.device(w, r)
	if !found {
		return
	}
	var req pinRequest
	if !decode(w, r, &req) {
		return
	}
	if err := svc.SetPINLock(r.Context(), req.Enabled, strings.TrimSpace(req.PIN), req.Force); err != nil {
		s.opError(w, err)
		return
	}
	if req.Save && req.Enabled {
		if err := s.savePIN(r, svc, strings.TrimSpace(req.PIN)); err != nil {
			s.serverError(w, err)
			return
		}
	}
	ok(w)
}

func (s *Server) simChangePIN(w http.ResponseWriter, r *http.Request) {
	svc, found := s.device(w, r)
	if !found {
		return
	}
	var req pinRequest
	if !decode(w, r, &req) {
		return
	}
	newPIN := strings.TrimSpace(req.NewPIN)
	if err := svc.ChangePIN(r.Context(), strings.TrimSpace(req.OldPIN), newPIN, req.Force); err != nil {
		s.opError(w, err)
		return
	}
	// A stored PIN is now wrong; replace it so auto-unlock keeps working.
	sim, err := s.Settings.SIM(r.Context(), svc.DeviceID())
	if err != nil {
		s.serverError(w, err)
		return
	}
	if req.Save || sim.HasPIN {
		if err := s.Settings.SetSIM(r.Context(), svc.DeviceID(), sim.AutoUnlock || req.Save, &newPIN); err != nil {
			s.serverError(w, err)
			return
		}
		svc.ResumeAutoUnlock()
	}
	ok(w)
}

// knownDevice checks the {id} names a stored device, online or not.
func (s *Server) knownDevice(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if _, err := s.Store.GetDevice(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "设备不存在")
		} else {
			s.serverError(w, err)
		}
		return "", false
	}
	return id, true
}

func (s *Server) getSIMSettings(w http.ResponseWriter, r *http.Request) {
	id, found := s.knownDevice(w, r)
	if !found {
		return
	}
	sim, err := s.Settings.SIM(r.Context(), id)
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sim)
}

// putSIMSettings stores the PIN used for automatic unlocking. The PIN is
// never sent back to the browser.
func (s *Server) putSIMSettings(w http.ResponseWriter, r *http.Request) {
	id, found := s.knownDevice(w, r)
	if !found {
		return
	}
	var req struct {
		AutoUnlock bool    `json:"autoUnlock"`
		PIN        *string `json:"pin"` // nil keeps the stored PIN, "" removes it
	}
	if !decode(w, r, &req) {
		return
	}
	if req.PIN != nil {
		pin := strings.TrimSpace(*req.PIN)
		if pin != "" && (len(pin) < 4 || len(pin) > 8 || strings.Trim(pin, "0123456789") != "") {
			writeError(w, http.StatusBadRequest, "PIN 需要 4 到 8 位数字")
			return
		}
		req.PIN = &pin
	}
	if err := s.Settings.SetSIM(r.Context(), id, req.AutoUnlock, req.PIN); err != nil {
		s.serverError(w, err)
		return
	}
	if svc := s.Devices.Device(id); svc != nil {
		svc.ResumeAutoUnlock()
		svc.Note(r.Context(), "info", "SIM 自动解锁设置已更新")
	}
	sim, err := s.Settings.SIM(r.Context(), id)
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sim)
}

// activity is the dashboard's hourly and daily history of SMS and calls.
func (s *Server) activity(w http.ResponseWriter, r *http.Request) {
	a, err := s.Store.Activity(r.Context(), time.Now())
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}
