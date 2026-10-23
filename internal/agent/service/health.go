package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/f0rmatting/haobot/internal/agent/modem"
	"github.com/f0rmatting/haobot/internal/agent/transport"
)

// Health thresholds. Options can override the timings for tests.
const (
	defaultHeartbeat    = 15 * time.Second
	pingTimeout         = 5 * time.Second
	hangAfter           = 3 // missed heartbeats in a row
	repeatHangWindow    = 15 * time.Minute
	initFailuresToReset = 2
	defaultNetworkGrace = 10 * time.Minute
	resetSettle         = 10 * time.Second // re-enumeration after a USB reset
	// Reboots and USB resets per device and hour before automatic recovery
	// stops and asks for a person.
	recoveryBudget = 3
	recoveryWindow = time.Hour
	keptRecoveries = 10
)

var (
	// errHung ends a session whose modem stopped answering AT.
	errHung = errors.New("模组连续多次不响应 AT 指令，判定为卡死")
	// errRestarting ends a session after the agent rebooted the module.
	errRestarting = errors.New("模组正在重启")
)

// Recovery is one automatic action taken to bring a device back.
type Recovery struct {
	At     time.Time
	Action string // 重新连接, 重新搜网, 软重启, USB 复位
	Reason string
	Err    string // empty when the action itself succeeded
}

// SelfTest is the outcome of a manual send-and-receive check.
type SelfTest struct {
	State      string // running, passed, failed
	To         string
	StartedAt  time.Time
	FinishedAt time.Time
	// Delay is how long the test message took to arrive.
	Delay  time.Duration
	Detail string
}

// Health is what the device page shows about a device's wellbeing.
type Health struct {
	LastPing     time.Time
	PingLatency  time.Duration
	PingFailures int
	// UnregisteredSince is set while a ready SIM has no network.
	UnregisteredSince time.Time
	Recoveries        []Recovery // newest first
	AutoRecover       bool
	// Suspended is set when the recovery budget ran out; the agent then
	// only reconnects and waits for a person.
	Suspended bool
	SelfTest  *SelfTest
}

// Budget limits disruptive recoveries per device. It is shared by every
// service of a manager so a device that re-enumerates keeps its history.
type Budget struct {
	mu     sync.Mutex
	events map[string][]time.Time
}

func NewBudget() *Budget { return &Budget{events: make(map[string][]time.Time)} }

// Take records one recovery for key if the budget allows it.
func (b *Budget) Take(key string, now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	var recent []time.Time
	for _, t := range b.events[key] {
		if now.Sub(t) < recoveryWindow {
			recent = append(recent, t)
		}
	}
	if len(recent) >= recoveryBudget {
		b.events[key] = recent
		return false
	}
	b.events[key] = append(recent, now)
	return true
}

// Reset forgets key's history, after a person resumed automatic recovery.
func (b *Budget) Reset(key string) {
	b.mu.Lock()
	delete(b.events, key)
	b.mu.Unlock()
}

// recoveryKey identifies the device for the budget, before and after the
// IMEI is known.
func (s *Service) recoveryKey() string {
	if id := s.DeviceID(); id != "" {
		return id
	}
	return s.opts.Transport.Key()
}

func (s *Service) autoRecover(ctx context.Context) bool {
	if s.opts.AutoRecover == nil {
		return true
	}
	return s.opts.AutoRecover(s.DeviceID())
}

// record keeps a recovery in the health log and the event log.
func (s *Service) record(ctx context.Context, r Recovery) {
	r.At = time.Now()
	s.update(func(snap *Snapshot) {
		snap.Health.Recoveries = append([]Recovery{r}, snap.Health.Recoveries...)
		if len(snap.Health.Recoveries) > keptRecoveries {
			snap.Health.Recoveries = snap.Health.Recoveries[:keptRecoveries]
		}
	})
	text := "自动恢复：" + r.Action + "（" + r.Reason + "）"
	level := "warn"
	if r.Err != "" {
		text += "失败：" + r.Err
		level = "error"
	}
	s.note(ctx, level, text)
}

// mayDisrupt checks the setting and budget before a reboot or USB reset.
// When the budget is spent it suspends automatic recovery and says so once.
func (s *Service) mayDisrupt(ctx context.Context, action string) bool {
	if !s.autoRecover(ctx) {
		return false
	}
	if s.budget().Take(s.recoveryKey(), time.Now()) {
		return true
	}
	if !s.Snapshot().Health.Suspended {
		s.update(func(snap *Snapshot) { snap.Health.Suspended = true })
		s.note(ctx, "error", "1 小时内已自动重启 3 次仍未恢复，已暂停自动"+action+"，请检查模组、SIM 卡、天线和供电")
	}
	return false
}

func (s *Service) budget() *Budget { return s.opts.Budget }

// ResumeRecovery clears a suspension after a person had a look.
func (s *Service) ResumeRecovery() {
	s.budget().Reset(s.recoveryKey())
	s.update(func(snap *Snapshot) { snap.Health.Suspended = false })
}

// heartbeat pings the modem. Three misses in a row return errHung so the
// session is torn down and recovery starts.
func (s *Service) heartbeat(ctx context.Context, m *modem.Modem) error {
	latency, err := m.Ping(ctx, s.opts.PingTimeout)
	if err == nil {
		s.update(func(snap *Snapshot) {
			snap.Health.LastPing, snap.Health.PingLatency, snap.Health.PingFailures = time.Now(), latency, 0
		})
		return nil
	}
	if fatal(err) || ctx.Err() != nil {
		return err
	}
	failures := s.Snapshot().Health.PingFailures + 1
	s.update(func(snap *Snapshot) { snap.Health.PingFailures = failures })
	if failures >= hangAfter {
		return errHung
	}
	return nil
}

// watchNetwork handles a ready SIM that stays off the network: first it
// toggles the radio to force a new search, later it reboots the module.
func (s *Service) watchNetwork(ctx context.Context, m *modem.Modem, st modem.Status) error {
	if st.SIM != modem.SIMReady || st.Functionality != modem.FunFull || st.Registered {
		s.radioCycled = false
		s.update(func(snap *Snapshot) { snap.Health.UnregisteredSince = time.Time{} })
		return nil
	}
	since := s.Snapshot().Health.UnregisteredSince
	if since.IsZero() {
		s.update(func(snap *Snapshot) { snap.Health.UnregisteredSince = time.Now() })
		return nil
	}
	grace := s.opts.NetworkGrace
	if grace == 0 {
		grace = defaultNetworkGrace
	}
	down := time.Since(since)
	switch {
	case down >= 2*grace:
		if !s.mayDisrupt(ctx, "重启") {
			return nil
		}
		err := m.Reboot(ctx)
		s.record(ctx, Recovery{Action: "软重启", Reason: "长时间未注册到网络", Err: errText(err)})
		if err == nil {
			return errRestarting
		}
	case down >= grace && !s.radioCycled && s.autoRecover(ctx):
		s.radioCycled = true
		err := m.SetFunctionality(ctx, modem.FunAirplane)
		if err == nil {
			sleep(ctx, 3*time.Second)
			err = m.SetFunctionality(ctx, modem.FunFull)
		}
		s.record(ctx, Recovery{Action: "重新搜网", Reason: "SIM 卡正常但未注册到网络", Err: errText(err)})
	}
	return nil
}

// afterSession decides what to do when serve returned: count hangs and AT
// failures, and reset the USB device when reconnecting is not enough.
func (s *Service) afterSession(ctx context.Context, err error) {
	switch {
	case errors.Is(err, errHung):
		now := time.Now()
		if !s.lastHang.IsZero() && now.Sub(s.lastHang) < repeatHangWindow {
			s.rebootNext = true
		}
		s.lastHang = now
		s.record(ctx, Recovery{Action: "重新连接", Reason: "模组连续 3 次不响应 AT 指令"})
	case errors.Is(err, modem.ErrNoAnswer):
		s.initFailures++
		if s.initFailures >= initFailuresToReset && s.mayDisrupt(ctx, "复位") {
			s.initFailures = 0
			err := transport.Reset(s.opts.Transport)
			s.record(ctx, Recovery{Action: "USB 复位", Reason: "重新连接后模组仍不响应", Err: errText(err)})
			sleep(ctx, resetSettle)
		}
	}
}

// afterInit reboots a module that hung twice in a short time, now that it
// answers again: reconnecting alone evidently does not last.
func (s *Service) afterInit(ctx context.Context, m *modem.Modem) error {
	s.initFailures = 0
	if !s.rebootNext {
		return nil
	}
	s.rebootNext = false
	if !s.mayDisrupt(ctx, "重启") {
		return nil
	}
	err := m.Reboot(ctx)
	s.record(ctx, Recovery{Action: "软重启", Reason: "短时间内反复卡死", Err: errText(err)})
	if err != nil {
		return nil
	}
	return errRestarting
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
