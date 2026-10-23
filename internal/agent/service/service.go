// Package service keeps the modem connected, imports received SMS into the
// store, publishes a status snapshot for the web UI, and runs the device
// operations requested there.
package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/f0rmatting/haobot/internal/agent/mms"
	"github.com/f0rmatting/haobot/internal/agent/modem"
	"github.com/f0rmatting/haobot/internal/agent/pdu"
	"github.com/f0rmatting/haobot/internal/agent/store"
	"github.com/f0rmatting/haobot/internal/agent/transport"
)

const (
	maxReconnectDelay = 30 * time.Second
	// partialAfter is how long a multipart message waits for missing parts
	// before it is stored as incomplete.
	partialAfter = 2 * time.Hour
	// fullPINRetries is the attempt count of a SIM whose PIN has never been
	// mistyped. Auto-unlock only runs then, so a wrong configured PIN can
	// never be what locks the card.
	fullPINRetries = 3
)

// Options configures the service.
type Options struct {
	Transport       transport.Config
	Storage         string        // where new SMS are received, e.g. "ME"
	ScanStorages    []string      // storages read on every sync, e.g. ME and SM
	DeleteAfterSave bool          // delete from the modem/SIM once stored locally
	KeepBinary      bool          // store binary SMS that are not MMS notifications
	PollInterval    time.Duration // status refresh and fallback SMS scan
	// PIN returns the SIM PIN for a device, or "" when none is configured.
	PIN func(deviceID string) (string, error)
	// OnMessages is called with the messages a sync just stored.
	OnMessages func([]store.Message)
	// Hub receives change notifications; shared by all devices.
	Hub *Hub
	// GiveUpAfter makes Run return after that many failed connection
	// attempts in a row, so discovery can drop unplugged modems. Zero
	// retries forever.
	GiveUpAfter int

	// Budget limits automatic reboots and USB resets; shared per manager.
	Budget *Budget
	// AutoRecover reports whether a device may be rebooted or reset
	// automatically; nil means always.
	AutoRecover func(deviceID string) bool
	// Heartbeat, PingTimeout and NetworkGrace override the health timings
	// (tests).
	Heartbeat    time.Duration
	PingTimeout  time.Duration
	NetworkGrace time.Duration
	// OpenAudio overrides how the call audio port is opened (tests).
	OpenAudio func() (io.ReadWriteCloser, error)
	// AudioDebugDir, when set, receives raw PCM copies of every call's
	// audio (call-*-rx.pcm from the modem, call-*-tx.pcm to it).
	AudioDebugDir string
	// Assistant, when set, may answer incoming calls.
	Assistant Assistant
	// OnCall is told about calls worth a notification. It must not block.
	OnCall func(CallEvent)
	// RecordingDir receives call recordings that KeepRecording wants; an
	// empty directory turns recording off.
	RecordingDir  string
	KeepRecording func(ctx context.Context, deviceID string, byAssistant bool) bool
}

// Snapshot is what the web UI shows about the modem.
type Snapshot struct {
	// Device is the stored record (IMEI, user-given name, enabled); empty
	// until the modem has answered once.
	Device    store.Device
	Connected bool
	Endpoint  string
	LastError string
	SMSReady  bool
	LastSync  time.Time
	Modem     modem.Status
	Identity  modem.Identity
	// PINBlocked is set after a failed automatic PIN attempt; automatic
	// unlocking stays off until the PIN is entered by hand or changed.
	PINBlocked bool
	Health     Health
	Calls      []CallInfo
	Audio      AudioState
	Assistant  AssistantState
}

// ErrOffline is returned by device operations while no modem is connected.
var ErrOffline = errors.New("模组未连接")

// op is a device operation run on the goroutine that owns the connection, so
// it never interleaves with an SMS sync.
type op struct {
	ctx  context.Context
	fn   func(context.Context, *modem.Modem) error
	done chan error
}

type Service struct {
	opts  Options
	store *store.Store
	log   *slog.Logger
	hub   *Hub
	ops   chan op

	mu       sync.RWMutex
	snapshot Snapshot

	pinBlocked atomic.Bool // a PIN attempt failed; no automatic retries

	noteMu   sync.Mutex
	lastNote string // suppresses repeating the same event

	// Call state and the open port, used only by the goroutine running serve.
	calls callState
	port  io.ReadWriteCloser

	audioMu   sync.Mutex
	audio     *audioBridge
	listeners map[*AudioStream]bool
	speaker   *AudioStream
	ai        *AssistantCall // the assistant's end of the call, if it has one

	// Recovery state, used only by the goroutine running Run.
	lastHang     time.Time
	rebootNext   bool
	initFailures int
	radioCycled  bool
}

func New(opts Options, st *store.Store, log *slog.Logger) *Service {
	hub := opts.Hub
	if hub == nil {
		hub = NewHub()
	}
	if opts.Budget == nil {
		opts.Budget = NewBudget()
	}
	if opts.Heartbeat == 0 {
		opts.Heartbeat = defaultHeartbeat
	}
	if opts.PingTimeout == 0 {
		opts.PingTimeout = pingTimeout
	}
	return &Service{
		opts:     opts,
		store:    st,
		log:      log.With("endpoint", opts.Transport.Describe()),
		hub:      hub,
		ops:      make(chan op),
		snapshot: Snapshot{Endpoint: opts.Transport.Describe()},
	}
}

// DeviceID returns the IMEI-based id, or "" before the modem answered.
func (s *Service) DeviceID() string { return s.Snapshot().Device.ID }

// Endpoint identifies the port this service drives.
func (s *Service) Endpoint() string { return s.opts.Transport.Key() }

// Hub delivers change notifications to the web UI.
func (s *Service) Hub() *Hub { return s.hub }

// Snapshot returns the latest status.
func (s *Service) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshot
}

func (s *Service) update(change func(*Snapshot)) {
	s.mu.Lock()
	change(&s.snapshot)
	s.snapshot.PINBlocked = s.pinBlocked.Load()
	s.mu.Unlock()
	s.hub.Publish(TopicStatus)
}

// Note records an operational event, e.g. an action taken in the web UI.
func (s *Service) Note(ctx context.Context, level, text string) { s.note(ctx, level, text) }

// note records an operational event, skipping exact repeats of the last one.
func (s *Service) note(ctx context.Context, level, text string) {
	s.noteMu.Lock()
	repeated := text == s.lastNote
	s.lastNote = text
	s.noteMu.Unlock()
	if repeated {
		return
	}
	if name := s.Snapshot().Device.Name; name != "" {
		text = "【" + name + "】" + text
	}
	switch level {
	case "error":
		s.log.Error(text)
	case "warn":
		s.log.Warn(text)
	default:
		s.log.Info(text)
	}
	if err := s.store.AddEvent(context.WithoutCancel(ctx), level, text); err != nil {
		s.log.Warn("record event", "error", err)
	}
	s.hub.Publish(TopicEvents)
}

// Run connects, serves the modem, and reconnects until ctx is cancelled.
func (s *Service) Run(ctx context.Context) {
	delay := time.Second
	failures := 0
	for ctx.Err() == nil {
		port, err := transport.Open(s.opts.Transport)
		if err != nil {
			failures++
			s.update(func(snap *Snapshot) { snap.Connected, snap.SMSReady, snap.LastError = false, false, err.Error() })
			if s.opts.GiveUpAfter > 0 && failures >= s.opts.GiveUpAfter {
				s.log.Info("modem gone", "error", err)
				return
			}
			s.note(ctx, "warn", "无法连接模组："+err.Error())
			if !sleep(ctx, delay) {
				return
			}
			delay = min(delay*2, maxReconnectDelay)
			continue
		}
		delay, failures = time.Second, 0
		err = s.serve(ctx, port)
		s.update(func(snap *Snapshot) {
			snap.Connected, snap.SMSReady = false, false
			if err != nil {
				snap.LastError = err.Error()
			}
		})
		if err != nil && ctx.Err() == nil {
			if !errors.Is(err, errHung) && !errors.Is(err, errRestarting) {
				s.note(ctx, "warn", "与模组的连接中断："+err.Error())
			}
			s.afterSession(ctx, err)
		}
		if !sleep(ctx, 2*time.Second) {
			return
		}
	}
}

func (s *Service) serve(ctx context.Context, port io.ReadWriteCloser) error {
	session := modem.NewSession(port)
	defer session.Close()
	m := modem.New(session)
	s.port, s.calls = port, callState{tracked: map[int]*trackedCall{}}
	// The audio port borrows this connection; close it first.
	defer s.stopAudio(context.WithoutCancel(ctx), nil)
	defer s.update(func(snap *Snapshot) { snap.Calls, snap.Assistant = nil, AssistantState{} })
	if err := m.Init(ctx); err != nil {
		return err
	}
	if err := s.identify(ctx, m); err != nil {
		return err
	}
	if err := s.afterInit(ctx, m); err != nil {
		return err
	}
	s.update(func(snap *Snapshot) {
		snap.Connected, snap.LastError, snap.Identity = true, "", modem.Identity{}
		snap.Health.LastPing, snap.Health.PingFailures = time.Now(), 0
	})
	// Calls left open by a previous connection ended with it.
	s.store.EndOpenCalls(ctx, s.DeviceID(), time.Now())
	m.EnableCallReports(ctx)
	if prev, err := m.RouteURCs(ctx, s.urcPort()); err == nil && prev != "" {
		s.note(ctx, "info", "已把模组的来电和短信通知改到当前连接的口（原来发往 "+prev+"），来电会立即提示")
	}
	s.calls.check = true
	// Connecting and SMS becoming ready are one line in the log when they
	// follow each other, as they usually do.
	connected := "已连接模组（" + s.opts.Transport.Describe() + "）"
	notedConnect := false

	smsReady, haveIdentity := false, false
	step := func() error {
		// A modem that misses heartbeats would make every status query
		// time out; skip the poll until it answers again.
		if err := s.heartbeat(ctx, m); err != nil || s.Snapshot().Health.PingFailures > 0 {
			return err
		}
		status, err := m.ReadStatus(ctx)
		if err != nil {
			return err
		}
		auto := s.autoRecover(ctx)
		s.update(func(snap *Snapshot) { snap.Modem, snap.Health.AutoRecover = status, auto })
		if dev, err := s.store.GetDevice(ctx, s.DeviceID()); err == nil {
			s.update(func(snap *Snapshot) { snap.Device = dev })
			if !dev.Enabled {
				if !notedConnect {
					s.note(ctx, "info", connected+"，设备已停用，不读取短信")
					notedConnect = true
				}
				return nil // a disabled device is watched but not synced
			}
		}
		if err := s.watchNetwork(ctx, m, status); err != nil {
			return err
		}
		// A RING lost on the wire would leave a call unnoticed; every poll
		// looks at the call list too.
		if err := s.updateCalls(ctx, m); err != nil && fatal(err) {
			return err
		}
		if !haveIdentity && status.SIM == modem.SIMReady {
			id, err := m.ReadIdentity(ctx)
			if err != nil {
				return err
			}
			haveIdentity = true
			s.update(func(snap *Snapshot) { snap.Identity = id })
		}
		if !smsReady {
			if smsReady, err = s.prepare(ctx, m, status); err != nil || !smsReady {
				if !notedConnect {
					s.note(ctx, "info", connected+"，短信功能还没就绪")
					notedConnect = true
				}
				return err
			}
			s.update(func(snap *Snapshot) { snap.SMSReady = true })
			ready := "短信功能就绪，存储位置 " + s.opts.Storage
			if !notedConnect {
				ready = connected + "，" + ready
				notedConnect = true
			}
			s.note(ctx, "info", ready)
		}
		// Reading the stores can take seconds: a call in progress, or a
		// notification waiting (it may be RING), goes first.
		if len(s.calls.tracked) > 0 {
			return nil
		}
		return s.sync(ctx, m, func() bool { return len(session.URCs()) > 0 })
	}

	ticker := time.NewTicker(s.opts.PollInterval)
	defer ticker.Stop()
	beat := time.NewTicker(s.opts.Heartbeat)
	defer beat.Stop()
	callTick := time.NewTicker(callPoll)
	defer callTick.Stop()
	// Every wake-up runs a full poll except a heartbeat, which only pings.
	runStep := true
	idleTicks := 0
	for {
		if runStep {
			if err := step(); err != nil {
				if fatal(err) {
					return err
				}
				s.note(ctx, "warn", err.Error())
			}
		}
		runStep = true
		select {
		case <-beat.C:
			runStep = false
			if err := s.heartbeat(ctx, m); err != nil && fatal(err) {
				return err
			}
		case <-callTick.C:
			runStep = false
			// A command timed out: ask the modem right away whether it still
			// answers, so a hang is found in seconds rather than at the next
			// heartbeats.
			if session.Stale() || s.Snapshot().Health.PingFailures > 0 {
				if err := s.heartbeat(ctx, m); err != nil {
					if fatal(err) {
						return err
					}
					continue
				}
			}
			// While idle the call list is still read every few seconds, in
			// case a RING notification never arrives.
			idleTicks++
			if s.calls.check || len(s.calls.tracked) > 0 || idleTicks%idleCallPoll == 0 {
				if err := s.updateCalls(ctx, m); err != nil && fatal(err) {
					return err
				}
			}
		case <-ctx.Done():
			return nil
		case <-session.Done():
			return session.Err()
		case urc := <-session.URCs():
			s.log.Debug("unsolicited result", "line", urc)
			if isCallURC(urc) {
				runStep = false
				if err := s.updateCalls(ctx, m); err != nil && fatal(err) {
					return err
				}
				continue
			}
			if strings.HasPrefix(urc, "RDY") || strings.HasPrefix(urc, "+CPIN:") {
				// The module restarted or the SIM changed: redo SMS setup.
				smsReady, haveIdentity = false, false
				s.update(func(snap *Snapshot) { snap.SMSReady = false })
			}
		case o := <-s.ops:
			err := o.fn(o.ctx, m)
			o.done <- err
			if errors.Is(err, modem.ErrClosed) {
				return err
			}
			// Looping runs step at once, so the UI sees the effect right away.
		case <-ticker.C:
		}
	}
}

// Do runs fn against the connected modem between polls. It fails with
// ErrOffline when no modem is connected.
func (s *Service) Do(ctx context.Context, fn func(context.Context, *modem.Modem) error) error {
	if !s.Snapshot().Connected {
		return ErrOffline
	}
	o := op{ctx: ctx, fn: fn, done: make(chan error, 1)}
	select {
	case s.ops <- o:
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(60 * time.Second):
		return errors.New("模组忙，请稍后重试")
	}
	select {
	case err := <-o.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// identify reads the IMEI and records the device, so everything stored from
// now on is attributed to it. A modem that hides its IMEI is keyed by port.
func (s *Service) identify(ctx context.Context, m *modem.Modem) error {
	imei, err := m.IMEI(ctx)
	if errors.Is(err, modem.ErrClosed) {
		return err
	}
	id := imei
	if id == "" {
		id = "port:" + s.opts.Transport.Key()
	}
	model := s.opts.Transport.Model
	if lines, err := m.Raw(ctx, "ATI", 3*time.Second); err == nil && len(lines) >= 2 {
		model = strings.TrimSpace(lines[0] + " " + lines[1])
	}
	name := shortModel(model)
	if len(imei) >= 4 {
		name += "-" + imei[len(imei)-4:]
	}
	dev, err := s.store.SeeDevice(ctx, store.Device{ID: id, Name: name, Model: model, IMEI: imei, Endpoint: s.opts.Transport.Describe()}, time.Now())
	if err != nil {
		return fmt.Errorf("记录设备失败：%w", err)
	}
	s.update(func(snap *Snapshot) { snap.Device = dev })
	return nil
}

// shortModel turns "Quectel EC20F" into "EC20F" for default names.
func shortModel(model string) string {
	fields := strings.Fields(model)
	if len(fields) == 0 {
		return "模组"
	}
	return fields[len(fields)-1]
}

// fatal reports errors that require reopening the connection. Command errors
// and timeouts on a live port are retried on the next poll.
func fatal(err error) bool {
	return errors.Is(err, modem.ErrClosed) || errors.Is(err, errHung) || errors.Is(err, errRestarting)
}

// prepare unlocks the SIM if allowed and configures SMS delivery. It returns
// false (without error) while the SIM is not usable yet.
func (s *Service) prepare(ctx context.Context, m *modem.Modem, status modem.Status) (bool, error) {
	switch status.SIM {
	case modem.SIMReady:
	case modem.SIMPIN:
		if !s.unlock(ctx, m, status) {
			return false, nil
		}
		// The SIM needs a moment after unlocking before SMS commands work.
		if !sleep(ctx, 3*time.Second) {
			return false, nil
		}
		state, err := m.SIMState(ctx)
		if err != nil || state != modem.SIMReady {
			return false, err
		}
	case "":
		return false, nil
	case modem.SIMPUK:
		s.note(ctx, "error", "SIM 卡已被 PUK 锁定，请用 PUK 码解锁")
		return false, nil
	case modem.SIMAbsent:
		s.note(ctx, "warn", "未检测到 SIM 卡")
		return false, nil
	default:
		s.note(ctx, "warn", "SIM 卡状态："+status.SIM)
		return false, nil
	}
	if err := m.ConfigureSMS(ctx, s.opts.Storage); err != nil {
		return false, fmt.Errorf("配置短信失败：%w", err)
	}
	return true, nil
}

func (s *Service) unlock(ctx context.Context, m *modem.Modem, status modem.Status) bool {
	if s.pinBlocked.Load() {
		return false
	}
	pin := ""
	if s.opts.PIN != nil {
		var err error
		if pin, err = s.opts.PIN(s.DeviceID()); err != nil {
			s.note(ctx, "error", "读取 SIM PIN 失败："+err.Error())
			return false
		}
	}
	if pin == "" {
		s.note(ctx, "warn", "SIM 卡需要 PIN，未配置 PIN，等待手动解锁")
		return false
	}
	if status.PINRetries != fullPINRetries {
		s.pinBlocked.Store(true)
		s.note(ctx, "error", fmt.Sprintf("SIM PIN 剩余 %d 次（不是满额 %d 次），为防止锁卡不自动输入，请手动解锁", status.PINRetries, fullPINRetries))
		return false
	}
	if err := m.EnterPIN(ctx, pin); err != nil {
		s.pinBlocked.Store(true)
		s.note(ctx, "error", "自动输入 SIM PIN 失败，已停止自动解锁："+err.Error())
		return false
	}
	s.note(ctx, "info", "SIM 卡已自动解锁")
	return true
}

// sync imports received messages from every scanned storage, then
// assembles multipart messages. It stops before the next storage once
// interrupted reports something more urgent.
func (s *Service) sync(ctx context.Context, m *modem.Modem, interrupted func() bool) error {
	now := time.Now()
	complete := true
	for _, storage := range s.opts.ScanStorages {
		if interrupted() {
			complete = false
			break
		}
		if err := m.SelectReadStorage(ctx, storage); err != nil {
			// A modem that stopped answering is not asked for more.
			if fatal(err) || errors.Is(err, modem.ErrTimeout) {
				return err
			}
			s.note(ctx, "warn", fmt.Sprintf("无法读取 %s 存储：%v", storage, err))
			continue
		}
		if err := s.syncStorage(ctx, m, storage, now); err != nil {
			return err
		}
	}
	// Leave reading pointed at the receive storage, as ConfigureSMS set it.
	if err := m.SelectReadStorage(ctx, s.opts.Storage); err != nil {
		return err
	}
	created, err := s.store.AssemblePending(ctx, now, partialAfter, finalizeMMS)
	if err != nil {
		return fmt.Errorf("整理短信失败：%w", err)
	}
	for _, msg := range created {
		s.log.Info("new message", "id", msg.ID, "kind", msg.Kind, "sender", msg.Sender, "parts", msg.Parts, "complete", msg.Complete)
	}
	if len(created) > 0 {
		s.hub.Publish(TopicMessages)
		if s.opts.OnMessages != nil {
			s.opts.OnMessages(created)
		}
	}
	if complete {
		s.update(func(snap *Snapshot) { snap.LastSync = now })
	}
	return nil
}

func (s *Service) syncStorage(ctx context.Context, m *modem.Modem, storage string, now time.Time) error {
	entries, err := m.ListSMS(ctx)
	if err != nil {
		return fmt.Errorf("读取 %s 短信失败：%w", storage, err)
	}
	for _, entry := range entries {
		if entry.Stat > 1 { // stored outgoing messages
			continue
		}
		seg, err := pdu.Decode(entry.PDU)
		if errors.Is(err, pdu.ErrNotDeliver) {
			continue
		}
		if err != nil {
			// Left in place, broken messages anyone can send would fill the
			// storage until no SMS gets through; the raw PDU stays in the log.
			s.note(ctx, "warn", fmt.Sprintf("无法解码 %s 第 %d 条短信，已从模组删除：%v；原始 PDU：%s", storage, entry.Index, err, entry.PDU))
			s.deleteEntry(ctx, m, storage, entry.Index)
			continue
		}
		if seg.Binary && seg.Port != pdu.WAPPushPort && !s.opts.KeepBinary {
			// Machine data (not text, not an MMS notification) is dropped.
			s.note(ctx, "info", fmt.Sprintf("已忽略来自 %s 的二进制短信（%d 字节）", seg.Sender, len(seg.Text)/2))
			s.deleteEntry(ctx, m, storage, entry.Index)
			continue
		}
		upper := strings.ToUpper(entry.PDU)
		// The device is part of the hash: the same broadcast can reach two
		// SIMs as byte-identical PDUs.
		sum := sha256.Sum256([]byte(s.DeviceID() + "|" + upper))
		legacy := sha256.Sum256([]byte(upper))
		if _, err := s.store.SaveSegment(ctx, store.Segment{
			PDUHash:    hex.EncodeToString(sum[:]),
			LegacyHash: hex.EncodeToString(legacy[:]),
			DeviceID:   s.DeviceID(),
			PDU:        entry.PDU,
			Sender:     seg.Sender,
			SentAt:     seg.SentAt,
			ReceivedAt: now,
			Ref:        seg.Ref,
			Total:      seg.Total,
			Seq:        seg.Seq,
			Body:       seg.Text,
			Binary:     seg.Binary,
			Port:       seg.Port,
		}); err != nil {
			return err
		}
		// Only delete what is safely stored; a crash before this line just
		// means the PDU is seen again and deduplicated by its hash.
		s.deleteEntry(ctx, m, storage, entry.Index)
	}
	return nil
}

func (s *Service) deleteEntry(ctx context.Context, m *modem.Modem, storage string, index int) {
	if !s.opts.DeleteAfterSave {
		return
	}
	if err := m.DeleteSMS(ctx, index); err != nil {
		s.note(ctx, "warn", fmt.Sprintf("删除 %s 第 %d 条短信失败：%v", storage, index, err))
	}
}

// finalizeMMS turns a WAP Push carrying an MMS notification into an MMS
// message. Other pushes stay binary.
func finalizeMMS(msg *store.Message) {
	if !msg.Binary || msg.Port != pdu.WAPPushPort || !msg.Complete {
		return
	}
	raw, err := hex.DecodeString(strings.ReplaceAll(msg.Body, " ", ""))
	if err != nil {
		return
	}
	n, err := mms.ParseWAPPush(raw, msg.SentAt)
	if err != nil {
		return
	}
	msg.Kind, msg.Binary = store.KindMMS, false
	msg.Body = n.Subject
	// The From inside the notification is whatever its sender wrote, so it
	// does not replace the number the SMS came from; it is shown as a claim.
	if from := strings.TrimSpace(n.From); from != "" && store.NormalizePeer(from) != store.NormalizePeer(msg.Sender) {
		msg.Body = strings.TrimSpace(msg.Body + "（彩信里写的发件人：" + from + "，未经核实）")
	}
	if u, err := url.Parse(n.URL); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		msg.MMSURL = n.URL
	}
	msg.MMSSize, msg.MMSExpiry = n.Size, n.Expiry
}

func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// urcPort is the module's name for the interface this service reads.
func (s *Service) urcPort() string {
	t := s.opts.Transport
	if t.Interface == 3 || strings.HasSuffix(t.Device, "USB3") {
		return modem.URCPortModem
	}
	return modem.URCPortAT
}
