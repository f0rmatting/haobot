package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/warthog618/sms"
	"github.com/warthog618/sms/encoding/tpdu"

	"github.com/f0rmatting/haobot/internal/agent/modem"
	"github.com/f0rmatting/haobot/internal/agent/store"
	"github.com/f0rmatting/haobot/internal/agent/transport"
)

// healthModem is a fake EC20 whose failures can be switched on: it can stop
// answering, drop off the network, and it delivers sent SMS back to itself.
type healthModem struct {
	t      *testing.T
	number string

	mu         sync.Mutex
	hung       bool
	failATA    bool // ATA fails, as on a modem that will not pick up
	registered bool
	fun        int
	box        map[int]string
	next       int
	commands   []string
	// calls are the voice calls AT+CLCC reports, besides a data bearer.
	calls  []modem.Call
	device net.Conn
}

// ring makes a call come in, announced with RING and +CLIP like a real one.
func (f *healthModem) ring(c modem.Call) {
	f.set(func(f *healthModem) { f.calls = []modem.Call{c} })
	io.WriteString(f.device, "\r\nRING\r\n\r\n+CLIP: \""+c.Number+"\",129\r\n")
}

func newHealthModem(t *testing.T) *healthModem {
	return &healthModem{t: t, number: "13800138000", registered: true, fun: 1, box: map[int]string{}}
}

func (f *healthModem) set(change func(*healthModem)) {
	f.mu.Lock()
	change(f)
	f.mu.Unlock()
}

func (f *healthModem) saw(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.commands {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func (f *healthModem) serve(device net.Conn) {
	f.mu.Lock()
	f.device = device
	f.mu.Unlock()
	reader := bufio.NewReader(device)
	write := func(s string) bool {
		_, err := io.WriteString(device, s)
		return err == nil
	}
	for {
		cmd, err := reader.ReadString('\r')
		if err != nil {
			return
		}
		cmd = strings.TrimSuffix(cmd, "\r")
		f.mu.Lock()
		f.commands = append(f.commands, cmd)
		hung := f.hung
		f.mu.Unlock()
		if hung {
			continue // a hung module reads but never answers
		}
		if strings.HasPrefix(cmd, "AT+CMGS=") {
			write("\r\n> ")
			payload, err := reader.ReadString('\x1a')
			if err != nil {
				return
			}
			text := f.loopback(strings.TrimSuffix(payload, "\x1a"))
			if !write("\r\n+CMGS: 1\r\n\r\nOK\r\n") {
				return
			}
			if text != "" {
				f.mu.Lock()
				i := f.next
				f.next++
				f.box[i] = textPDU(f.t, f.number, text)
				f.mu.Unlock()
				write(fmt.Sprintf("\r\n+CMTI: \"ME\",%d\r\n", i))
			}
			continue
		}
		if !write("\r\n" + f.answer(cmd) + "\r\n") {
			return
		}
	}
}

// loopback decodes a submitted PDU's text, as if the network delivered it
// back to this SIM.
func (f *healthModem) loopback(payload string) string {
	raw, err := hex.DecodeString(payload)
	if err != nil || len(raw) < 2 {
		return ""
	}
	d := tpdu.TPDU{Direction: tpdu.MO}
	if err := d.UnmarshalBinary(raw[1:]); err != nil {
		return ""
	}
	text, err := sms.Decode([]*tpdu.TPDU{&d})
	if err != nil {
		return ""
	}
	return string(text)
}

func (f *healthModem) answer(cmd string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case cmd == "AT+CGSN":
		return "866000000000042\r\nOK"
	case cmd == "ATI":
		return "Quectel\r\nEC20F\r\nRevision: X\r\nOK"
	case cmd == "AT+CPIN?":
		return "+CPIN: READY\r\nOK"
	case cmd == "AT+CFUN?":
		return fmt.Sprintf("+CFUN: %d\r\nOK", f.fun)
	case cmd == "AT+CFUN=4":
		f.fun = 4
	case cmd == "AT+CFUN=1":
		f.fun = 1
	case cmd == "AT+CEREG?" || cmd == "AT+CREG?":
		state := 2
		if f.registered {
			state = 1
		}
		return fmt.Sprintf("%s: 0,%d\r\nOK", cmd[2:len(cmd)-1], state)
	case cmd == "AT+CMGL=4":
		var b strings.Builder
		for i, p := range f.box {
			fmt.Fprintf(&b, "+CMGL: %d,0,,%d\r\n%s\r\n", i, len(p)/2-1, p)
		}
		return b.String() + "OK"
	case cmd == `AT+QURCCFG="urcport"`:
		return "+QURCCFG: \"urcport\",\"usbmodem\"\r\nOK"
	case cmd == "AT+CLCC":
		// The real EC20 lists its data connection too.
		b := strings.Builder{}
		b.WriteString("+CLCC: 1,1,0,1,0,\"\",128\r\n")
		for _, c := range f.calls {
			dir := 0
			if c.Incoming {
				dir = 1
			}
			fmt.Fprintf(&b, "+CLCC: %d,%d,%d,0,0,\"%s\",129\r\n", c.ID, dir, c.State, c.Number)
		}
		return b.String() + "OK"
	case strings.HasPrefix(cmd, "ATD") && strings.HasSuffix(cmd, ";"):
		f.calls = append(f.calls, modem.Call{ID: 3, State: modem.CallAlerting, Number: strings.TrimSuffix(cmd[3:], ";")})
	case cmd == "ATA":
		if f.failATA {
			return "ERROR"
		}
		for i := range f.calls {
			f.calls[i].State = modem.CallActive
		}
	case cmd == "AT+CHUP":
		f.calls = nil
	case strings.HasPrefix(cmd, "AT+CMGD="):
		var i int
		fmt.Sscanf(cmd, "AT+CMGD=%d", &i)
		delete(f.box, i)
	}
	return "OK"
}

func healthService(t *testing.T, opts Options) (*Service, *store.Store, *healthModem, chan error) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	fake := newHealthModem(t)
	host, device := net.Pipe()
	go fake.serve(device)
	opts.Storage, opts.ScanStorages, opts.DeleteAfterSave = "ME", []string{"ME"}, true
	if opts.PollInterval == 0 {
		opts.PollInterval = time.Hour
	}
	svc := New(opts, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- svc.serve(ctx, host) }()
	waitFor(t, "SMS ready", func() bool { return svc.Snapshot().SMSReady })
	return svc, st, fake, done
}

func TestHungModemIsDetected(t *testing.T) {
	svc, _, fake, done := healthService(t, Options{Heartbeat: 30 * time.Millisecond, PingTimeout: 50 * time.Millisecond})
	if h := svc.Snapshot().Health; h.LastPing.IsZero() || h.PingFailures != 0 {
		t.Fatalf("health before hang = %+v", h)
	}
	fake.set(func(f *healthModem) { f.hung = true })
	select {
	case err := <-done:
		if !errors.Is(err, errHung) {
			t.Fatalf("serve returned %v, want errHung", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("hung modem not detected")
	}
}

func TestNetworkWatchdogResearchesThenReboots(t *testing.T) {
	svc, _, fake, done := healthService(t, Options{PollInterval: 20 * time.Millisecond, NetworkGrace: 150 * time.Millisecond})
	fake.set(func(f *healthModem) { f.registered = false })
	waitFor(t, "radio toggled", func() bool { return fake.saw("AT+CFUN=4") })
	select {
	case err := <-done:
		if !errors.Is(err, errRestarting) {
			t.Fatalf("serve returned %v, want errRestarting", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no reboot after the network stayed down")
	}
	if !fake.saw("AT+CFUN=1,1") {
		t.Fatal("module was not rebooted")
	}
	actions := []string{}
	for _, r := range svc.Snapshot().Health.Recoveries {
		actions = append(actions, r.Action)
	}
	if strings.Join(actions, ",") != "软重启,重新搜网" {
		t.Fatalf("recoveries = %v", actions)
	}
}

func TestRecoveryBudgetSuspends(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "b.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	auto := true
	svc := New(Options{AutoRecover: func(string) bool { return auto }}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()
	for i := 0; i < recoveryBudget; i++ {
		if !svc.mayDisrupt(ctx, "重启") {
			t.Fatalf("recovery %d refused", i+1)
		}
	}
	if svc.mayDisrupt(ctx, "重启") || !svc.Snapshot().Health.Suspended {
		t.Fatal("budget not enforced")
	}
	svc.ResumeRecovery()
	if svc.Snapshot().Health.Suspended || !svc.mayDisrupt(ctx, "重启") {
		t.Fatal("resume did not restore the budget")
	}
	auto = false
	if svc.mayDisrupt(ctx, "重启") {
		t.Fatal("disruptive recovery while automatic recovery is off")
	}
}

func TestRepeatedFailuresEscalateToUSBReset(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := New(Options{Transport: transport.Config{Kind: transport.KindSerial, Device: "/dev/does-not-exist"}}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	// A cancelled context skips the settle wait after the reset.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	noAnswer := fmt.Errorf("%w: timeout", modem.ErrNoAnswer)
	svc.afterSession(ctx, errHung)
	svc.afterSession(ctx, errHung)
	if !svc.rebootNext {
		t.Fatal("two hangs in a row should schedule a soft reboot")
	}
	svc.afterSession(ctx, noAnswer)
	svc.afterSession(ctx, noAnswer)
	got := svc.Snapshot().Health.Recoveries
	if len(got) != 3 || got[0].Action != "USB 复位" || got[1].Action != "重新连接" {
		t.Fatalf("recoveries = %+v", got)
	}
	// This fake port has no USB device behind it, so the reset reports why.
	if got[0].Err == "" {
		t.Fatal("failed USB reset not reported")
	}
}

func TestSelfTestRoundTrip(t *testing.T) {
	var forwarded []store.Message
	var fmu sync.Mutex
	m, st := managerWith(t)
	m.base.OnMessages = func(msgs []store.Message) {
		if rest := m.takeSelfTests(msgs); len(rest) > 0 {
			fmu.Lock()
			forwarded = append(forwarded, rest...)
			fmu.Unlock()
		}
	}
	opts := m.base
	opts.PollInterval = 50 * time.Millisecond
	svc, _, fake, _ := healthServiceWithStore(t, st, opts)
	m.services[svc.Endpoint()] = svc

	if err := m.SelfTest(context.Background(), svc.DeviceID(), fake.number); err != nil {
		t.Fatal(err)
	}
	if err := m.SelfTest(context.Background(), svc.DeviceID(), fake.number); !errors.Is(err, ErrSelfTestRunning) {
		t.Fatalf("second self-test = %v, want ErrSelfTestRunning", err)
	}
	waitFor(t, "self-test result", func() bool {
		st := svc.Snapshot().Health.SelfTest
		return st != nil && st.State != "running"
	})
	if res := svc.Snapshot().Health.SelfTest; res.State != "passed" {
		t.Fatalf("self-test = %+v", res)
	}
	// Both test messages are gone and none reached classification.
	if _, total, _ := st.ListMessages(context.Background(), store.Filter{DeviceID: svc.DeviceID()}, 10, 0); total != 0 {
		t.Fatalf("%d test messages left behind", total)
	}
	fmu.Lock()
	defer fmu.Unlock()
	if len(forwarded) != 0 {
		t.Fatalf("self-test message forwarded: %+v", forwarded)
	}
}

// healthServiceWithStore is healthService on an existing store and options.
func healthServiceWithStore(t *testing.T, st *store.Store, opts Options) (*Service, *store.Store, *healthModem, chan error) {
	t.Helper()
	fake := newHealthModem(t)
	host, device := net.Pipe()
	go fake.serve(device)
	opts.Storage, opts.ScanStorages, opts.DeleteAfterSave = "ME", []string{"ME"}, true
	svc := New(opts, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- svc.serve(ctx, host) }()
	waitFor(t, "SMS ready", func() bool { return svc.Snapshot().SMSReady })
	return svc, st, fake, done
}

func currentCalls(svc *Service) []CallInfo { return svc.Snapshot().Calls }

func TestCallLifecycleAndAudio(t *testing.T) {
	modemSide, agentSide := net.Pipe()
	defer modemSide.Close()
	opened := make(chan struct{}, 4)
	svc, st, fake, _ := healthService(t, Options{OpenAudio: func() (io.ReadWriteCloser, error) {
		opened <- struct{}{}
		return agentSide, nil
	}})
	ctx := context.Background()

	// Outgoing: dial, the other side answers, audio flows both ways.
	if err := svc.Dial(ctx, "13900000000"); err != nil {
		t.Fatal(err)
	}
	if calls := currentCalls(svc); len(calls) != 1 || calls[0].Incoming || calls[0].State != modem.CallAlerting {
		t.Fatalf("calls after dial = %+v (the data bearer must not count)", calls)
	}
	if err := svc.Dial(ctx, "13900000001"); !errors.Is(err, ErrCallBusy) {
		t.Fatalf("second dial = %v, want ErrCallBusy", err)
	}
	if !fake.saw("AT+QPCMV=1,0") {
		t.Fatal("voice over USB not enabled before dialing")
	}
	stream := svc.AttachAudio()
	defer stream.Close()
	fake.set(func(f *healthModem) { f.calls[0].State = modem.CallActive })
	waitFor(t, "call answered", func() bool {
		c := currentCalls(svc)
		return len(c) == 1 && !c[0].AnsweredAt.IsZero()
	})
	select {
	case <-opened:
	case <-time.After(3 * time.Second):
		t.Fatal("audio port not opened when the call was answered")
	}
	waitFor(t, "audio active", func() bool { return svc.Snapshot().Audio.Active })

	// Downlink: whatever the modem sends arrives as 320-byte frames.
	go modemSide.Write(make([]byte, 2*audioFrame))
	for i := 0; i < 2; i++ {
		select {
		case f := <-stream.Frames():
			if len(f) != audioFrame {
				t.Fatalf("frame of %d bytes", len(f))
			}
		case <-time.After(2 * time.Second):
			t.Fatal("no audio frame from the modem")
		}
	}
	// Uplink: one frame every 20 ms, silence until the browser's
	// microphone has something, then the microphone frame itself.
	mic := bytes.Repeat([]byte{0x34, 0x12}, audioFrame/2)
	frames := make(chan []byte, 200)
	go func() {
		for {
			f := make([]byte, audioFrame)
			if _, err := io.ReadFull(modemSide, f); err != nil {
				close(frames)
				return
			}
			frames <- f
		}
	}()
	silent := 0
	for silent < 3 {
		select {
		case f := <-frames:
			if bytes.Equal(f, make([]byte, audioFrame)) {
				silent++
			}
		case <-time.After(2 * time.Second):
			t.Fatal("no silence sent while the microphone is quiet")
		}
	}
	stream.Write(mic)
	deadline := time.After(2 * time.Second)
	for found := false; !found; {
		select {
		case f := <-frames:
			found = bytes.Equal(f, mic)
		case <-deadline:
			t.Fatal("microphone frame never reached the modem")
		}
	}

	if err := svc.Hangup(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "audio stopped", func() bool { return !svc.Snapshot().Audio.Active && len(currentCalls(svc)) == 0 })
	if !fake.saw("AT+QPCMV=0") {
		t.Fatal("voice over USB not switched off after the call")
	}

	// Incoming, rejected here; then incoming and missed.
	fake.ring(modem.Call{ID: 5, Incoming: true, State: modem.CallIncoming, Number: "13800000002"})
	waitFor(t, "incoming call", func() bool { return len(currentCalls(svc)) == 1 })
	if err := svc.Hangup(ctx); err != nil {
		t.Fatal(err)
	}
	fake.ring(modem.Call{ID: 6, Incoming: true, State: modem.CallIncoming, Number: "13800000003"})
	waitFor(t, "second incoming call", func() bool { return len(currentCalls(svc)) == 1 })
	fake.set(func(f *healthModem) { f.calls = nil })
	io.WriteString(fake.device, "\r\nNO CARRIER\r\n")
	waitFor(t, "call gone", func() bool { return len(currentCalls(svc)) == 0 })

	log, total, err := st.ListCalls(ctx, "", "", 10, 0)
	if err != nil || total != 3 {
		t.Fatalf("call log = %d entries, %v", total, err)
	}
	want := []string{store.CallMissed, store.CallRejected, store.CallCompleted}
	for i, c := range log {
		if c.Status != want[i] {
			t.Errorf("call %d (%s %s) status %s, want %s", i, c.Direction, c.Number, c.Status, want[i])
		}
	}
	if n, _ := st.UnseenMissedCalls(ctx); n != 1 {
		t.Fatalf("unseen missed calls = %d, want 1", n)
	}
}

// flakyPort fails its first writes, like a USB endpoint that NAKs while the
// call is set up, then accepts frames.
type flakyPort struct {
	mu     sync.Mutex
	fails  int
	frames int
	closed chan struct{}
}

func (p *flakyPort) Read(b []byte) (int, error) { <-p.closed; return 0, io.EOF }
func (p *flakyPort) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fails > 0 {
		p.fails--
		return 0, errors.New("usb write: LIBUSB_ERROR_TIMEOUT")
	}
	p.frames++
	return len(b), nil
}
func (p *flakyPort) Close() error { close(p.closed); return nil }

func TestUplinkSurvivesWriteTimeouts(t *testing.T) {
	port := &flakyPort{fails: 5, closed: make(chan struct{})}
	svc, _, fake, _ := healthService(t, Options{OpenAudio: func() (io.ReadWriteCloser, error) { return port, nil }})
	ctx := context.Background()
	if err := svc.Dial(ctx, "13900000000"); err != nil {
		t.Fatal(err)
	}
	fake.set(func(f *healthModem) { f.calls[0].State = modem.CallActive })
	waitFor(t, "uplink resumed after timeouts", func() bool {
		port.mu.Lock()
		defer port.mu.Unlock()
		return port.fails == 0 && port.frames >= 20
	})
	svc.audioMu.Lock()
	b := svc.audio
	svc.audioMu.Unlock()
	b.mu.Lock()
	fails := b.stats.WriteFails
	b.mu.Unlock()
	if fails != 5 {
		t.Fatalf("write failures counted = %d, want 5", fails)
	}
	svc.Hangup(ctx)
}
