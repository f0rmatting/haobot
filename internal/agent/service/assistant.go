package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/f0rmatting/haobot/internal/agent/modem"
	"github.com/f0rmatting/haobot/internal/agent/store"
)

// Assistant answers incoming calls for the owner: it talks with the caller
// and leaves a note. Package assistant implements it.
type Assistant interface {
	// AnswerAfter reports how long an incoming call rings before the
	// assistant picks up; ok is false while the assistant is off.
	AnswerAfter(ctx context.Context, deviceID, number string) (delay time.Duration, ok bool)
	// Ready reports whether the assistant can talk on deviceID's ringing
	// call yet. An error means it cannot: the call is left to the owner.
	Ready(deviceID string) (bool, error)
	// Handle talks with the caller until the call ends or the owner takes
	// over, then records its note. It runs on a goroutine of its own.
	Handle(ctx context.Context, call *AssistantCall)
}

// Assistant phases shown in the UI.
const (
	AssistantConnecting = "connecting" // opening the model session
	AssistantTalking    = "talking"
	AssistantEnding     = "ending" // saying goodbye before hanging up
)

// AssistantState is the assistant's part in the current call.
type AssistantState struct {
	// AnswerAt is when the assistant picks up the ringing call; zero when
	// it will not.
	AnswerAt time.Time
	Active   bool // the assistant is on the call
	RecordID int64
	Phase    string
	Error    string
	Lines    []TranscriptLine
}

// TranscriptLine is one utterance of the conversation.
type TranscriptLine struct {
	Role  string    `json:"role"` // RoleCaller or RoleAssistant
	Text  string    `json:"text"`
	Final bool      `json:"-"` // false while still being spoken
	At    time.Time `json:"at"`
}

const (
	RoleCaller    = "caller"
	RoleAssistant = "assistant"
)

const (
	// maxAssistantAudio caps the speech waiting for the uplink.
	maxAssistantAudio = 120 * bytesPerSecond
	// publishEvery limits status pushes for partial transcript lines.
	publishEvery = 300 * time.Millisecond
	// speechPrebuffer is how much of a reply is collected before it plays,
	// and again after the model fell behind: one short pause instead of
	// speech chopped by every network hiccup.
	speechPrebuffer = 300 * bytesPerSecond / 1000
	// Each underrun lengthens the prebuffer by speechPrebufferStep, up to
	// speechPrebufferMax: a slow link then gets a longer pause before a
	// reply instead of one stutter after another.
	speechPrebufferStep = 200 * bytesPerSecond / 1000
	speechPrebufferMax  = 2 * bytesPerSecond
)

// AssistantCall is the assistant's end of a call: caller audio in, speech
// out, and the transcript the UI shows.
type AssistantCall struct {
	DeviceID string
	RecordID int64
	Number   string

	svc  *Service
	in   chan []byte
	done chan struct{}
	once sync.Once

	mu        sync.Mutex
	out       []byte // speech waiting for the uplink pacer
	playing   bool   // out is being played; false while buffering
	complete  bool   // the reply in out is whole: play it to the end
	underruns int    // times a reply ran dry while playing
	prebuffer int    // bytes collected before playing; grows on underruns
	phase     string
	err       string
	lines     []TranscriptLine
	published time.Time
}

// Frames delivers the caller's audio: 20 ms frames of 8 kHz, 16-bit
// little-endian mono PCM.
func (c *AssistantCall) Frames() <-chan []byte { return c.in }

// Done is closed when the call ends or the owner takes over.
func (c *AssistantCall) Done() <-chan struct{} { return c.done }

// Play queues speech in the same format as Frames.
func (c *AssistantCall) Play(pcm []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.out)+len(pcm) > maxAssistantAudio {
		return
	}
	c.out = append(c.out, pcm...)
	c.complete = false
}

// EndSpeech marks the end of a reply, so what is queued plays out even
// when it is shorter than the prebuffer.
func (c *AssistantCall) EndSpeech() {
	c.mu.Lock()
	c.complete = true
	c.mu.Unlock()
}

// Underruns counts how often speech ran dry in the middle of a reply.
func (c *AssistantCall) Underruns() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.underruns
}

// Flush drops queued speech, when the caller starts talking.
func (c *AssistantCall) Flush() {
	c.mu.Lock()
	c.out, c.playing = nil, false
	c.mu.Unlock()
}

// Queued is how much speech has yet to reach the caller.
func (c *AssistantCall) Queued() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Duration(len(c.out)) * time.Second / bytesPerSecond
}

// next takes one frame of speech for the pacer, or nil.
func (c *AssistantCall) next() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.prebuffer == 0 {
		c.prebuffer = speechPrebuffer
	}
	if !c.playing {
		if len(c.out) < c.prebuffer && !(c.complete && len(c.out) > 0) {
			return nil
		}
		c.playing = true
	}
	if len(c.out) == 0 {
		c.playing = false
		if !c.complete {
			c.underruns++
			c.prebuffer = min(c.prebuffer+speechPrebufferStep, speechPrebufferMax)
		}
		return nil
	}
	frame := make([]byte, audioFrame)
	n := copy(frame, c.out)
	c.out = c.out[n:]
	if len(c.out) == 0 {
		c.out = nil
	}
	return frame
}

// Hangup ends the call.
func (c *AssistantCall) Hangup(ctx context.Context) error {
	select {
	case <-c.done:
		return nil // the call is over or no longer the assistant's
	default:
	}
	return c.svc.Hangup(ctx)
}

// SetPhase updates what the UI shows the assistant doing.
func (c *AssistantCall) SetPhase(phase string) {
	c.mu.Lock()
	c.phase = phase
	c.mu.Unlock()
	c.publish(true)
}

// SetError reports a problem, e.g. the model could not be reached.
func (c *AssistantCall) SetError(text string) {
	c.mu.Lock()
	c.err = text
	c.mu.Unlock()
	c.publish(true)
}

// Transcript adds to the conversation. A line that is not final is replaced
// by the next one of the same role: the caller's words are transcribed while
// the assistant may already be answering.
func (c *AssistantCall) Transcript(role, text string, final bool) {
	c.mu.Lock()
	open := -1
	for i := len(c.lines) - 1; i >= 0 && open < 0; i-- {
		if c.lines[i].Role == role && !c.lines[i].Final {
			open = i
		}
	}
	if open >= 0 {
		c.lines[open].Text, c.lines[open].Final = text, final
	} else {
		c.lines = append(c.lines, TranscriptLine{Role: role, Text: text, Final: final, At: time.Now()})
	}
	c.mu.Unlock()
	c.publish(final)
}

// Lines returns the conversation so far.
func (c *AssistantCall) Lines() []TranscriptLine {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]TranscriptLine(nil), c.lines...)
}

// SaveNote stores the transcript and summary (both JSON) with the call.
func (c *AssistantCall) SaveNote(ctx context.Context, transcript, summary string) error {
	err := c.svc.store.SaveCallNote(ctx, c.RecordID, transcript, summary)
	c.svc.hub.Publish(TopicCalls)
	if err == nil {
		c.svc.callEvent(ctx, CallEventNote, c.RecordID)
	}
	return err
}

// Note records an event in the device log.
func (c *AssistantCall) Note(ctx context.Context, level, text string) { c.svc.note(ctx, level, text) }

func (c *AssistantCall) end() { c.once.Do(func() { close(c.done) }) }

// publish copies the state into the snapshot; partial updates are
// throttled.
func (c *AssistantCall) publish(force bool) {
	c.mu.Lock()
	if !force && time.Since(c.published) < publishEvery {
		c.mu.Unlock()
		return
	}
	c.published = time.Now()
	state := AssistantState{Active: true, RecordID: c.RecordID, Phase: c.phase, Error: c.err, Lines: append([]TranscriptLine(nil), c.lines...)}
	c.mu.Unlock()
	s := c.svc
	s.audioMu.Lock()
	current := s.ai == c
	s.audioMu.Unlock()
	if current {
		s.update(func(snap *Snapshot) { snap.Assistant = state })
	}
}

// assistantReadyWait is how long past the ring delay a call waits for the
// assistant's model to connect before it is left to the owner.
const assistantReadyWait = 8 * time.Second

// ErrNoAssistant is returned when the assistant is not configured.
var ErrNoAssistant = errors.New("没有配置 AI 接听助手")

// AnswerWithAssistant lets the assistant pick up the ringing call now.
func (s *Service) AnswerWithAssistant(ctx context.Context) error {
	if s.opts.Assistant == nil {
		return ErrNoAssistant
	}
	return s.Do(ctx, func(ctx context.Context, m *modem.Modem) error {
		t := s.ringingCall()
		if t == nil {
			return errors.New("没有正在响铃的来电")
		}
		return s.assistantAnswer(ctx, m, t)
	})
}

// SkipAssistant keeps the assistant from picking up the ringing call.
func (s *Service) SkipAssistant(ctx context.Context) error {
	return s.Do(ctx, func(ctx context.Context, m *modem.Modem) error {
		if t := s.ringingCall(); t != nil {
			t.aiAt = time.Time{}
		}
		s.update(func(snap *Snapshot) { snap.Assistant.AnswerAt = time.Time{} })
		return nil
	})
}

// TakeOver ends the assistant's part; the call goes on with the browser's
// microphone.
func (s *Service) TakeOver(ctx context.Context) error {
	s.audioMu.Lock()
	c := s.ai
	s.audioMu.Unlock()
	if c == nil {
		return errors.New("AI 助手没有在通话中")
	}
	s.endAssistant(c)
	s.note(ctx, "info", "已从 AI 助手接过通话："+displayNumber(c.Number))
	return nil
}

func (s *Service) ringingCall() *trackedCall {
	for _, t := range s.calls.tracked {
		if t.info.Incoming && t.info.AnsweredAt.IsZero() {
			return t
		}
	}
	return nil
}

// scheduleAssistant decides, when a call starts ringing, whether and when
// the assistant answers it. Only a lone call qualifies: a waiting call
// during another one is left to the owner.
func (s *Service) scheduleAssistant(ctx context.Context, t *trackedCall) {
	if s.opts.Assistant == nil || len(s.calls.tracked) != 1 {
		return
	}
	delay, ok := s.opts.Assistant.AnswerAfter(ctx, s.DeviceID(), t.info.Number)
	if !ok {
		return
	}
	t.aiAt = time.Now().Add(delay)
	s.update(func(snap *Snapshot) { snap.Assistant.AnswerAt = t.aiAt })
}

// autoAnswer picks up for the assistant once the ring delay is over.
func (s *Service) autoAnswer(ctx context.Context, m *modem.Modem) {
	t := s.ringingCall()
	if t == nil || t.aiAt.IsZero() || time.Now().Before(t.aiAt) {
		return
	}
	if ready, err := s.opts.Assistant.Ready(s.DeviceID()); err != nil || !ready {
		late := time.Since(t.aiAt) > assistantReadyWait
		if err == nil && !late {
			return // still connecting; checked again on the next poll
		}
		if err == nil {
			err = errors.New("连接 AI 模型超时")
		}
		t.aiAt = time.Time{}
		s.update(func(snap *Snapshot) { snap.Assistant.AnswerAt = time.Time{} })
		s.note(ctx, "warn", "AI 助手没有接听 "+displayNumber(t.info.Number)+"："+err.Error()+"。电话照常响铃，没人接会按未接来电提醒你")
		return
	}
	t.aiAt = time.Time{}
	if err := s.assistantAnswer(ctx, m, t); err != nil {
		s.note(ctx, "warn", "AI 助手接听失败："+err.Error()+"。电话照常响铃，可以自己接")
	}
}

func (s *Service) assistantAnswer(ctx context.Context, m *modem.Modem, t *trackedCall) error {
	s.enableVoice(ctx, m)
	if err := m.Answer(ctx); err != nil {
		// The call rings on for the owner; the UI stops saying the
		// assistant is picking up.
		t.aiAt = time.Time{}
		s.update(func(snap *Snapshot) { snap.Assistant.AnswerAt = time.Time{} })
		return friendly(err)
	}
	t.aiAt, t.byAI = time.Time{}, true
	s.store.SetCallHandler(ctx, t.info.RecordID, store.HandledByAssistant)
	s.note(ctx, "info", "AI 助手已接听来电："+displayNumber(t.info.Number))
	// Open the audio right away, as a manual answer does: AT+QPCMV left
	// unserviced for a second stalls the downlink and loses the caller's
	// first words.
	return s.updateCalls(ctx, m)
}

// startAssistant hands an answered call to the assistant. The audio bridge
// must be running.
func (s *Service) startAssistant(ctx context.Context, t *trackedCall) {
	t.aiStarted = true
	c := &AssistantCall{
		DeviceID: s.DeviceID(), RecordID: t.info.RecordID, Number: t.info.Number,
		svc: s, in: make(chan []byte, 100), done: make(chan struct{}), phase: AssistantConnecting,
	}
	s.audioMu.Lock()
	s.ai = c
	if s.audio != nil {
		s.audio.byAI = true
	}
	s.audioMu.Unlock()
	c.publish(true)
	go func() {
		s.opts.Assistant.Handle(context.WithoutCancel(ctx), c)
		s.endAssistant(c)
	}()
}

// endAssistant takes the call away from the assistant.
func (s *Service) endAssistant(c *AssistantCall) {
	c.end()
	s.audioMu.Lock()
	current := s.ai == c
	if current {
		s.ai = nil
	}
	s.audioMu.Unlock()
	if current {
		s.update(func(snap *Snapshot) { snap.Assistant = AssistantState{} })
	}
}

// mix adds src into dst, both 16-bit little-endian PCM, saturating.
func mix(dst, src []byte) {
	for i := 0; i+1 < len(dst) && i+1 < len(src); i += 2 {
		v := int32(int16(uint16(dst[i])|uint16(dst[i+1])<<8)) + int32(int16(uint16(src[i])|uint16(src[i+1])<<8))
		v = max(-32768, min(32767, v))
		dst[i], dst[i+1] = byte(v), byte(uint16(v)>>8)
	}
}
