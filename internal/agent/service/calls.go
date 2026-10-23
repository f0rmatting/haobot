package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/f0rmatting/haobot/internal/agent/modem"
	"github.com/f0rmatting/haobot/internal/agent/store"
	"github.com/f0rmatting/haobot/internal/agent/transport"
)

// TopicCalls is published when the call log changes.
const TopicCalls = "calls"

const (
	// callPoll is how often AT+CLCC is read while a call is up.
	callPoll = time.Second
	// idleCallPoll reads the call list every that many callPolls while no
	// call is up.
	idleCallPoll = 3
	// audioFrame is 20 ms of 8 kHz 16-bit mono PCM, the AT+QPCMV format.
	audioFrame = 320
)

// Call events for notifications.
const (
	CallEventRinging = "ringing" // an incoming call started ringing
	CallEventMissed  = "missed"  // it ended unanswered
	CallEventNote    = "note"    // the assistant saved its note
)

// CallEvent is a call worth telling the owner about.
type CallEvent struct {
	Kind       string
	Record     store.CallRecord
	DeviceName string
}

// callEvent passes the current record of a call to Options.OnCall.
func (s *Service) callEvent(ctx context.Context, kind string, id int64) {
	if s.opts.OnCall == nil || id == 0 {
		return
	}
	rec, err := s.store.GetCall(ctx, id)
	if err != nil {
		return
	}
	s.opts.OnCall(CallEvent{Kind: kind, Record: rec, DeviceName: s.Snapshot().Device.Name})
}

// ErrCallBusy is returned when a call is already in progress.
var ErrCallBusy = errors.New("已经有一通电话在进行中")

// CallInfo is a call in progress, as shown in the UI.
type CallInfo struct {
	ID         int
	Incoming   bool
	State      modem.CallState
	Number     string
	StartedAt  time.Time
	AnsweredAt time.Time
	RecordID   int64
}

// AudioState tells the UI whether call audio reaches the browser.
type AudioState struct {
	Active    bool   // the audio port is open and flowing
	Listeners int    // browsers attached
	Error     string // why audio could not start

	// Stream statistics, refreshed once a second during a call.
	RxPerSecond int // bytes from the modem in the last second; ~16000 is right
	MaxGapMs    int
	MicFrames   int64
	FillFrames  int64
}

// trackedCall follows one AT+CLCC call id between polls.
type trackedCall struct {
	info     CallInfo
	rejected bool // hung up here while ringing

	aiAt      time.Time // when the assistant answers; zero: it will not
	byAI      bool      // the assistant answered
	aiStarted bool      // the assistant was handed the call
}

// callState is owned by the goroutine running serve.
type callState struct {
	tracked map[int]*trackedCall
	check   bool // poll AT+CLCC at the next chance
	voiceOn bool // AT+QPCMV is enabled
}

// isCallURC reports result codes that mean a call changed.
func isCallURC(urc string) bool {
	for _, prefix := range []string{"RING", "+CRING:", "+CLIP:", "NO CARRIER", "BUSY", "NO ANSWER", "+CLCC:", `+QIND: "ccinfo"`} {
		if strings.HasPrefix(urc, prefix) {
			return true
		}
	}
	return false
}

// updateCalls reads AT+CLCC and turns changes into call log entries and
// audio starts and stops.
func (s *Service) updateCalls(ctx context.Context, m *modem.Modem) error {
	calls, err := m.Calls(ctx)
	if err != nil {
		return err
	}
	cs := &s.calls
	if cs.tracked == nil {
		cs.tracked = map[int]*trackedCall{}
	}
	cs.check = false
	now := time.Now()
	seen := map[int]bool{}
	for _, c := range calls {
		seen[c.ID] = true
		t := cs.tracked[c.ID]
		if t == nil {
			t = &trackedCall{info: CallInfo{ID: c.ID, Incoming: c.Incoming, Number: c.Number, StartedAt: now}}
			rec := store.CallRecord{DeviceID: s.DeviceID(), Number: c.Number, Direction: store.DirectionOut, Status: store.CallDialing, StartedAt: now}
			if c.Incoming {
				rec.Direction, rec.Status = store.DirectionIn, store.CallRinging
			}
			if saved, err := s.store.StartCall(ctx, rec); err == nil {
				t.info.RecordID = saved.ID
			}
			cs.tracked[c.ID] = t
			if c.Incoming {
				s.note(ctx, "info", "来电："+displayNumber(c.Number))
				s.scheduleAssistant(ctx, t)
				s.callEvent(ctx, CallEventRinging, t.info.RecordID)
			} else {
				s.note(ctx, "info", "拨出电话："+displayNumber(c.Number))
			}
			s.hub.Publish(TopicCalls)
		}
		if t.info.Number == "" && c.Number != "" {
			t.info.Number = c.Number
		}
		t.info.State = c.State
		if c.State == modem.CallActive && t.info.AnsweredAt.IsZero() {
			t.info.AnsweredAt = now
			s.store.AnswerCall(ctx, t.info.RecordID, now)
			s.hub.Publish(TopicCalls)
		}
	}
	for id, t := range cs.tracked {
		if seen[id] {
			continue
		}
		status := store.CallCanceled
		switch {
		case !t.info.AnsweredAt.IsZero():
			status = store.CallCompleted
		case t.info.Incoming && t.rejected:
			status = store.CallRejected
		case t.info.Incoming:
			status = store.CallMissed
		}
		s.store.EndCall(ctx, t.info.RecordID, status, now)
		delete(cs.tracked, id)
		if status == store.CallMissed {
			s.callEvent(ctx, CallEventMissed, t.info.RecordID)
		}
		text := map[string]string{store.CallCompleted: "通话结束", store.CallRejected: "已拒接", store.CallMissed: "未接来电", store.CallCanceled: "未接通"}[status]
		s.note(ctx, "info", text+"："+displayNumber(t.info.Number))
		s.hub.Publish(TopicCalls)
	}

	active := false
	var forAI *trackedCall
	infos := make([]CallInfo, 0, len(cs.tracked))
	for _, t := range cs.tracked {
		infos = append(infos, t.info)
		if t.info.State == modem.CallActive {
			active = true
			if t.byAI && !t.aiStarted {
				forAI = t
			}
		}
	}
	s.update(func(snap *Snapshot) {
		snap.Calls = infos
		if s.ringingCall() == nil {
			snap.Assistant.AnswerAt = time.Time{}
		}
	})
	if active {
		s.startAudio(ctx, m)
		if forAI != nil && s.audioRunning() {
			s.startAssistant(ctx, forAI)
		}
	} else if len(infos) == 0 {
		s.stopAudio(ctx, m)
	}
	s.autoAnswer(ctx, m)
	return nil
}

func (s *Service) audioRunning() bool {
	s.audioMu.Lock()
	defer s.audioMu.Unlock()
	return s.audio != nil
}

func displayNumber(n string) string {
	if n == "" {
		return "未知号码"
	}
	return n
}

// canCall checks the device can place or take calls now.
func (s *Service) canCall() error {
	snap := s.Snapshot()
	switch {
	case !snap.Connected:
		return ErrOffline
	case !snap.Device.Enabled:
		return errors.New("设备已停用，请先在设备页启用")
	case snap.Modem.SIM != modem.SIMReady:
		return ErrNotReady
	case !snap.Modem.Registered:
		return errors.New("还没有注册到网络，暂时无法通话")
	}
	return nil
}

// Dial places a call.
func (s *Service) Dial(ctx context.Context, number string) error {
	number, err := modem.CleanNumber(number)
	if err != nil {
		return err
	}
	if err := s.canCall(); err != nil {
		return err
	}
	return s.Do(ctx, func(ctx context.Context, m *modem.Modem) error {
		if len(s.calls.tracked) > 0 {
			return ErrCallBusy
		}
		s.enableVoice(ctx, m)
		if err := m.Dial(ctx, number); err != nil {
			return friendly(err)
		}
		return s.updateCalls(ctx, m)
	})
}

// Answer picks up the ringing call.
func (s *Service) Answer(ctx context.Context) error {
	return s.Do(ctx, func(ctx context.Context, m *modem.Modem) error {
		t := s.ringingCall()
		if t == nil {
			return errors.New("没有正在响铃的来电")
		}
		t.aiAt = time.Time{}
		s.enableVoice(ctx, m)
		if err := m.Answer(ctx); err != nil {
			return friendly(err)
		}
		return s.updateCalls(ctx, m)
	})
}

// Hangup ends the call, or rejects it while it rings.
func (s *Service) Hangup(ctx context.Context) error {
	return s.Do(ctx, func(ctx context.Context, m *modem.Modem) error {
		for _, t := range s.calls.tracked {
			if t.info.Incoming && t.info.AnsweredAt.IsZero() {
				t.rejected, t.aiAt = true, time.Time{}
			}
		}
		if err := m.Hangup(ctx); err != nil {
			return friendly(err)
		}
		return s.updateCalls(ctx, m)
	})
}

// SendDTMF plays a key tone into the active call.
func (s *Service) SendDTMF(ctx context.Context, key string) error {
	return s.Do(ctx, func(ctx context.Context, m *modem.Modem) error {
		return friendly(m.SendDTMF(ctx, key))
	})
}

// Forwarding reads every forwarding reason from the network.
func (s *Service) Forwarding(ctx context.Context) ([]modem.Forwarding, error) {
	var out []modem.Forwarding
	err := s.Do(ctx, func(ctx context.Context, m *modem.Modem) error {
		for _, reason := range []int{modem.ForwardAlways, modem.ForwardBusy, modem.ForwardNoReply, modem.ForwardUnreachable} {
			f, err := m.QueryForwarding(ctx, reason)
			if err != nil {
				return fmt.Errorf("查询%s失败：%w", modem.ForwardReasons[reason], friendly(err))
			}
			out = append(out, f)
		}
		return nil
	})
	return out, err
}

// SetForwarding changes one forwarding reason.
func (s *Service) SetForwarding(ctx context.Context, reason int, enabled bool, number string) error {
	return s.Do(ctx, func(ctx context.Context, m *modem.Modem) error {
		if err := m.SetForwarding(ctx, reason, enabled, number); err != nil {
			return friendly(err)
		}
		state := "已关闭"
		if enabled {
			state = "已设置为转到 " + number
		}
		s.note(ctx, "info", modem.ForwardReasons[reason]+state)
		return nil
	})
}

// ---- audio ----

const (
	// uplinkQueue holds up to 200 ms of microphone audio for the pacer.
	uplinkQueue = 10
	frameTime   = 20 * time.Millisecond
	// bytesPerSecond is what 8 kHz 16-bit mono audio should deliver.
	bytesPerSecond = 16000
)

// audioStats measure the audio stream, to tell a starved or misaligned
// stream from network trouble.
type audioStats struct {
	Started    time.Time
	RxBytes    int64
	Reads      int64
	OddReads   int64 // reads of odd length: the 16-bit alignment can slip
	MaxGap     time.Duration
	MicFrames  int64 // frames from the browser microphone
	AIFrames   int64 // frames of the assistant's speech
	FillFrames int64 // silence sent because no microphone frame was ready
	Dropped    int64 // microphone frames dropped because the queue was full
	WriteFails int64 // frames the modem did not take in time
	Realigned  int64 // times the 16-bit byte alignment was repaired
	lastRead   time.Time
}

// audioBridge copies call audio between the modem's audio port and the
// browsers listening to it. A pacer writes exactly one frame every 20 ms to
// the modem, microphone audio when there is some and silence otherwise:
// AT+QPCMV expects a steady uplink and its downlink falters without one.
type audioBridge struct {
	port io.ReadWriteCloser
	done chan struct{}
	up   chan []byte
	// monitor carries the assistant's speech to the browsers, mixed into
	// the caller's audio so the owner hears both sides.
	monitor chan []byte

	mu    sync.Mutex
	stats audioStats

	rxFile, txFile *os.File // raw PCM copies when Options.AudioDebugDir is set

	// rec records the call for the call log; recordID and byAI say which
	// call it is and whether the assistant took it (guarded by audioMu).
	rec      *recorder
	recordID int64
	byAI     bool
}

// AudioStream is one browser's end of the call audio.
type AudioStream struct {
	svc    *Service
	frames chan []byte
	once   sync.Once
}

// Frames delivers 20 ms PCM frames (8 kHz, 16-bit little-endian, mono).
func (a *AudioStream) Frames() <-chan []byte { return a.frames }

// Write queues microphone PCM in the same format for the call.
func (a *AudioStream) Write(pcm []byte) error {
	a.svc.audioMu.Lock()
	b := a.svc.audio
	speaker := a.svc.speaker == a && a.svc.ai == nil
	a.svc.audioMu.Unlock()
	if b == nil || !speaker {
		return nil // no call audio yet, another browser holds the mic, or the assistant talks
	}
	frame := append([]byte(nil), pcm...)
	select {
	case b.up <- frame:
	default:
		// Full: the browser runs ahead of the modem. Drop the oldest frame
		// so the delay does not grow.
		select {
		case <-b.up:
		default:
		}
		b.up <- frame
		b.mu.Lock()
		b.stats.Dropped++
		b.mu.Unlock()
	}
	return nil
}

// Close detaches the browser.
func (a *AudioStream) Close() {
	a.once.Do(func() {
		s := a.svc
		s.audioMu.Lock()
		delete(s.listeners, a)
		if s.speaker == a {
			s.speaker = nil
		}
		n := len(s.listeners)
		s.audioMu.Unlock()
		s.update(func(snap *Snapshot) { snap.Audio.Listeners = n })
	})
}

// AttachAudio connects a browser to call audio. The latest browser to
// attach also becomes the one whose microphone is heard.
func (s *Service) AttachAudio() *AudioStream {
	a := &AudioStream{svc: s, frames: make(chan []byte, 50)}
	s.audioMu.Lock()
	if s.listeners == nil {
		s.listeners = map[*AudioStream]bool{}
	}
	s.listeners[a] = true
	s.speaker = a
	n := len(s.listeners)
	s.audioMu.Unlock()
	s.update(func(snap *Snapshot) { snap.Audio.Listeners = n })
	return a
}

func (s *Service) enableVoice(ctx context.Context, m *modem.Modem) {
	if s.calls.voiceOn {
		return
	}
	if err := m.SetVoiceOverUSB(ctx, true); err != nil {
		s.update(func(snap *Snapshot) {
			snap.Audio.Error = "模组不支持通过 USB 传输通话声音：" + err.Error()
		})
		return
	}
	s.calls.voiceOn = true
}

func (s *Service) startAudio(ctx context.Context, m *modem.Modem) {
	s.audioMu.Lock()
	running := s.audio != nil
	s.audioMu.Unlock()
	if running {
		return
	}
	s.enableVoice(ctx, m)
	open := s.opts.OpenAudio
	if open == nil {
		open = func() (io.ReadWriteCloser, error) { return transport.OpenAudio(s.opts.Transport, s.port) }
	}
	port, err := open()
	if err != nil {
		s.update(func(snap *Snapshot) {
			snap.Audio = AudioState{Error: "无法打开通话音频口：" + err.Error(), Listeners: snap.Audio.Listeners}
		})
		s.note(ctx, "warn", "无法打开通话音频口："+err.Error())
		return
	}
	b := &audioBridge{port: port, done: make(chan struct{}), up: make(chan []byte, uplinkQueue), monitor: make(chan []byte, uplinkQueue)}
	b.stats.Started = time.Now()
	if s.opts.RecordingDir != "" && s.opts.KeepRecording != nil {
		b.rec = newRecorder()
		for _, t := range s.calls.tracked {
			if t.info.State == modem.CallActive {
				b.recordID, b.byAI = t.info.RecordID, t.byAI
			}
		}
	}
	if dir := s.opts.AudioDebugDir; dir != "" {
		stamp := time.Now().Format("20060102-150405")
		b.rxFile, _ = os.Create(filepath.Join(dir, "call-"+stamp+"-rx.pcm"))
		b.txFile, _ = os.Create(filepath.Join(dir, "call-"+stamp+"-tx.pcm"))
		s.note(ctx, "info", "正在录制通话音频诊断文件："+filepath.Join(dir, "call-"+stamp+"-*.pcm"))
	}
	s.audioMu.Lock()
	s.audio = b
	s.audioMu.Unlock()
	s.update(func(snap *Snapshot) { snap.Audio.Active, snap.Audio.Error = true, "" })
	go s.pumpAudio(b)
	go s.paceUplink(b)
}

// pumpAudio reads the audio port and hands whole frames to every listener;
// a slow browser drops frames rather than delaying the others.
func (s *Service) pumpAudio(b *audioBridge) {
	buf := make([]byte, 4096)
	var pending []byte
	var align aligner
	for {
		n, err := b.port.Read(buf)
		if n > 0 {
			now := time.Now()
			b.mu.Lock()
			st := &b.stats
			if !st.lastRead.IsZero() {
				st.MaxGap = max(st.MaxGap, now.Sub(st.lastRead))
			}
			st.lastRead = now
			st.RxBytes += int64(n)
			st.Reads++
			if n%2 == 1 {
				st.OddReads++
			}
			b.mu.Unlock()
			if b.rxFile != nil {
				b.rxFile.Write(buf[:n])
			}

			pending = append(pending, buf[:n]...)
			for len(pending) >= audioFrame {
				// The check looks one byte ahead when that byte is here.
				if len(pending) > audioFrame && align.check(pending) {
					pending = pending[1:]
					b.mu.Lock()
					b.stats.Realigned++
					b.mu.Unlock()
					continue
				}
				frame := make([]byte, audioFrame)
				copy(frame, pending)
				pending = pending[audioFrame:]
				if b.rec != nil {
					b.rec.add(trackDown, frame)
				}
				s.audioMu.Lock()
				if s.ai != nil {
					select {
					case s.ai.in <- frame:
					default:
					}
				}
				if len(s.listeners) > 0 {
					heard := frame
					select {
					case speech := <-b.monitor:
						heard = append([]byte(nil), frame...)
						mix(heard, speech)
					default:
					}
					for l := range s.listeners {
						select {
						case l.frames <- heard:
						default:
						}
					}
				}
				s.audioMu.Unlock()
			}
		}
		if err != nil {
			select {
			case <-b.done:
			default:
				s.log.Warn("call audio read", "error", err)
			}
			return
		}
	}
}

// aligner repairs the 16-bit sample alignment of the downlink. EC20 now and
// then delivers an odd number of bytes (seen on a real module: once in a
// 15 s call), after which every sample has its bytes split across two and
// speech turns into loud noise. Speech is smooth, so when the next frame is
// clearly smoother read one byte later, for three frames in a row, a byte is
// dropped. Quiet frames carry no evidence and are skipped.
type aligner struct{ misses int }

const (
	alignMinRMS  = 1000 // below this a frame is too quiet to judge
	alignBadAt   = 0.7  // roughness of byte-split audio is about 1
	alignGoodAt  = 0.45 // speech is about 0.1-0.4
	alignConfirm = 3
)

// check reports whether one byte should be dropped before the next frame.
// p holds at least one frame and one byte.
func (a *aligner) check(p []byte) bool {
	now, rms := roughness(p[:audioFrame])
	later, _ := roughness(p[1 : audioFrame+1])
	if rms < alignMinRMS || now <= alignBadAt || later >= alignGoodAt {
		a.misses = 0
		return false
	}
	a.misses++
	if a.misses < alignConfirm {
		return false
	}
	a.misses = 0
	return true
}

// roughness is the mean step between samples relative to the RMS level.
func roughness(frame []byte) (ratio, rms float64) {
	n := len(frame) / 2
	var sum, steps float64
	prev := 0.0
	for i := 0; i < n; i++ {
		v := float64(int16(uint16(frame[2*i]) | uint16(frame[2*i+1])<<8))
		sum += v * v
		if i > 0 {
			steps += math.Abs(v - prev)
		}
		prev = v
	}
	rms = math.Sqrt(sum / float64(n))
	if rms == 0 || n < 2 {
		return 0, rms
	}
	return steps / float64(n-1) / rms, rms
}

// paceUplink writes one frame every 20 ms and publishes the stream's
// statistics once a second.
func (s *Service) paceUplink(b *audioBridge) {
	tick := time.NewTicker(frameTime)
	defer tick.Stop()
	silence := make([]byte, audioFrame)
	var lastRx int64
	n := 0
	for {
		select {
		case <-b.done:
			return
		case <-tick.C:
		}
		frame, mic, speech := silence, false, false
		s.audioMu.Lock()
		ai := s.ai
		s.audioMu.Unlock()
		if ai != nil {
			if f := ai.next(); f != nil {
				frame, speech = f, true
				select {
				case b.monitor <- f:
				default:
				}
			}
		} else {
			select {
			case f := <-b.up:
				frame, mic = f, true
			default:
			}
		}
		// A frame the modem does not take in time is skipped; the pacing
		// must go on, since the downlink stalls without an uplink. Only the
		// end of the call stops it.
		_, err := b.port.Write(frame)
		if err != nil && errors.Is(err, io.ErrClosedPipe) {
			return
		}
		if b.txFile != nil && err == nil {
			b.txFile.Write(frame)
		}
		if b.rec != nil && err == nil {
			b.rec.add(trackUp, frame)
		}
		b.mu.Lock()
		switch {
		case err != nil:
			b.stats.WriteFails++
		case speech:
			b.stats.AIFrames++
		case mic:
			b.stats.MicFrames++
		default:
			b.stats.FillFrames++
		}
		st := b.stats
		b.mu.Unlock()
		if n++; n%50 == 0 {
			rate := st.RxBytes - lastRx
			lastRx = st.RxBytes
			s.update(func(snap *Snapshot) {
				snap.Audio.RxPerSecond, snap.Audio.MaxGapMs = int(rate), int(st.MaxGap.Milliseconds())
				snap.Audio.MicFrames, snap.Audio.FillFrames = st.MicFrames, st.FillFrames
			})
		}
	}
}

func (s *Service) stopAudio(ctx context.Context, m *modem.Modem) {
	s.audioMu.Lock()
	b, ai := s.audio, s.ai
	s.audio = nil
	s.audioMu.Unlock()
	if ai != nil {
		s.endAssistant(ai)
	}
	if b != nil {
		close(b.done)
		b.port.Close()
		for _, f := range []*os.File{b.rxFile, b.txFile} {
			if f != nil {
				f.Close()
			}
		}
		s.noteAudioStats(ctx, b)
		go s.saveRecording(context.WithoutCancel(ctx), b)
	}
	if s.calls.voiceOn && m != nil {
		m.SetVoiceOverUSB(ctx, false)
		s.calls.voiceOn = false
	}
	if b != nil {
		s.update(func(snap *Snapshot) { snap.Audio.Active = false })
	}
}

// noteAudioStats logs how the audio stream behaved, the first thing to look
// at when a call sounds wrong.
func (s *Service) noteAudioStats(ctx context.Context, b *audioBridge) {
	b.mu.Lock()
	st := b.stats
	b.mu.Unlock()
	secs := time.Since(st.Started).Seconds()
	if secs < 1 {
		return
	}
	rate := float64(st.RxBytes) / secs
	text := fmt.Sprintf("通话音频统计：时长 %.0f 秒；下行平均 %.0f 字节/秒（正常约 %d），共读取 %d 次，奇数长度 %d 次，最长中断 %d 毫秒；上行麦克风 %d 帧，AI 语音 %d 帧，补静音 %d 帧，丢弃 %d 帧，写入超时 %d 帧；字节对齐修正 %d 次",
		secs, rate, bytesPerSecond, st.Reads, st.OddReads, st.MaxGap.Milliseconds(), st.MicFrames, st.AIFrames, st.FillFrames, st.Dropped, st.WriteFails, st.Realigned)
	level := "info"
	if rate < bytesPerSecond*0.8 || rate > bytesPerSecond*1.2 || st.MaxGap > 200*time.Millisecond || st.WriteFails > 0 {
		level = "warn"
	}
	s.note(ctx, level, text)
}
