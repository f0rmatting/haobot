package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/f0rmatting/haobot/internal/agent/modem"
	"github.com/f0rmatting/haobot/internal/agent/store"
)

// fakeAssistant answers at once, says one frame, waits to hear the caller
// and hangs up.
type fakeAssistant struct {
	delay    time.Duration
	readyErr error // the model cannot be reached
	heard    chan []byte
	done     chan struct{}
}

func (a *fakeAssistant) AnswerAfter(context.Context, string, string) (time.Duration, bool) {
	return a.delay, true
}

func (a *fakeAssistant) Ready(string) (bool, error) { return a.readyErr == nil, a.readyErr }

func (a *fakeAssistant) Handle(ctx context.Context, c *AssistantCall) {
	defer close(a.done)
	c.SetPhase(AssistantTalking)
	c.Play(bytes.Repeat([]byte{0x22, 0x11}, audioFrame/2))
	c.EndSpeech()
	select {
	case f := <-c.Frames():
		a.heard <- f
	case <-time.After(3 * time.Second):
		return
	}
	c.Transcript(RoleCaller, "我是快递", true)
	c.Transcript(RoleAssistant, "好的，我会转告机主", true)
	for c.Queued() > 0 {
		time.Sleep(10 * time.Millisecond)
	}
	c.SaveNote(ctx, `[]`, `{"brief":"快递"}`)
	time.Sleep(1500 * time.Millisecond) // recordings under a second are dropped
	c.Hangup(ctx)
}

func TestAssistantAnswersAndTalks(t *testing.T) {
	modemSide, agentSide := net.Pipe()
	defer modemSide.Close()
	ai := &fakeAssistant{delay: 1500 * time.Millisecond, heard: make(chan []byte, 1), done: make(chan struct{})}
	recDir := t.TempDir()
	svc, st, fake, _ := healthService(t, Options{
		OpenAudio:     func() (io.ReadWriteCloser, error) { return agentSide, nil },
		Assistant:     ai,
		RecordingDir:  recDir,
		KeepRecording: func(_ context.Context, _ string, byAssistant bool) bool { return byAssistant },
	})
	ctx := context.Background()

	fake.ring(modem.Call{ID: 7, Incoming: true, State: modem.CallIncoming, Number: "13800000009"})
	waitFor(t, "pickup scheduled", func() bool { return !svc.Snapshot().Assistant.AnswerAt.IsZero() })
	if fake.saw("ATA") {
		t.Fatal("answered before the ring delay")
	}
	waitFor(t, "assistant on the call", func() bool { return svc.Snapshot().Assistant.Active })
	if !fake.saw("ATA") {
		t.Fatal("ATA never sent")
	}

	// The assistant's speech goes up; the browser microphone does not.
	stream := svc.AttachAudio()
	defer stream.Close()
	stream.Write(bytes.Repeat([]byte{0x77, 0x77}, audioFrame/2))
	speech := bytes.Repeat([]byte{0x22, 0x11}, audioFrame/2)
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
	// Caller audio reaches the assistant.
	caller := bytes.Repeat([]byte{0x00, 0x08}, audioFrame/2)
	go modemSide.Write(caller)
	deadline := time.After(3 * time.Second)
	for got := false; !got; {
		select {
		case f := <-frames:
			if bytes.Equal(f, bytes.Repeat([]byte{0x77, 0x77}, audioFrame/2)) {
				t.Fatal("browser microphone reached the caller while the assistant talks")
			}
			got = bytes.Equal(f, speech)
		case <-deadline:
			t.Fatal("assistant speech never reached the modem")
		}
	}
	select {
	case f := <-ai.heard:
		if !bytes.Equal(f, caller) {
			t.Fatal("assistant heard something else")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("caller audio never reached the assistant")
	}
	// The browser hears the caller with the assistant mixed in.
	select {
	case <-stream.Frames():
	case <-time.After(2 * time.Second):
		t.Fatal("browser heard nothing")
	}

	<-ai.done
	waitFor(t, "call over", func() bool { return len(currentCalls(svc)) == 0 && !svc.Snapshot().Assistant.Active })
	calls, _, err := st.ListCalls(ctx, "", "", 10, 0)
	if err != nil || len(calls) != 1 {
		t.Fatalf("calls = %v, %v", calls, err)
	}
	c := calls[0]
	if c.HandledBy != store.HandledByAssistant || c.Status != store.CallCompleted || c.Summary != `{"brief":"快递"}` || c.Seen {
		t.Fatalf("call record = %+v", c)
	}
	// The call was recorded as a WAV with both sides.
	waitFor(t, "recording saved", func() bool {
		c, _ := st.GetCall(ctx, calls[0].ID)
		return c.Recording != ""
	})
	c, _ = st.GetCall(ctx, calls[0].ID)
	wav, err := os.ReadFile(filepath.Join(recDir, c.Recording))
	if err != nil || len(wav) < 44+16000 || string(wav[:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
		t.Fatalf("recording %q: %d bytes, %v", c.Recording, len(wav), err)
	}
	// Both sides are in it, alone or mixed: 0x1122 is the assistant, 0x0800
	// the caller.
	seen := map[uint16]bool{}
	for i := 44; i+1 < len(wav); i += 2 {
		seen[uint16(wav[i])|uint16(wav[i+1])<<8] = true
	}
	if !(seen[0x1122] || seen[0x1922]) || !(seen[0x0800] || seen[0x1922]) {
		t.Fatal("recording lacks a side of the call")
	}
}

func TestAnsweringByHandCancelsAssistant(t *testing.T) {
	_, agentSide := net.Pipe()
	ai := &fakeAssistant{delay: 2 * time.Second, heard: make(chan []byte, 1), done: make(chan struct{})}
	svc, _, fake, _ := healthService(t, Options{
		OpenAudio: func() (io.ReadWriteCloser, error) { return agentSide, nil },
		Assistant: ai,
	})
	fake.ring(modem.Call{ID: 8, Incoming: true, State: modem.CallIncoming, Number: "13800000010"})
	waitFor(t, "pickup scheduled", func() bool { return !svc.Snapshot().Assistant.AnswerAt.IsZero() })
	if err := svc.Answer(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Second)
	if svc.Snapshot().Assistant.Active {
		t.Fatal("assistant took a call answered by hand")
	}
	select {
	case <-ai.done:
		t.Fatal("assistant ran")
	default:
	}
	svc.Hangup(context.Background())
}

func TestAssistantSpeechIsBuffered(t *testing.T) {
	c := &AssistantCall{}
	frame := bytes.Repeat([]byte{1, 0}, audioFrame/2)
	// A reply starts playing only once 300 ms are queued...
	for range 10 {
		c.Play(frame)
	}
	if c.next() != nil {
		t.Fatal("played before the prebuffer filled")
	}
	for range 5 {
		c.Play(frame)
	}
	got := 0
	for c.next() != nil {
		got++
	}
	if got != 15 {
		t.Fatalf("played %d frames, want 15", got)
	}
	// ...running dry mid-reply waits for the prebuffer again...
	c.Play(frame)
	if c.next() != nil || c.Underruns() != 1 {
		t.Fatalf("resumed without rebuffering (underruns %d)", c.Underruns())
	}
	// ...and the end of a reply plays whatever is left.
	c.EndSpeech()
	if c.next() == nil || c.next() != nil {
		t.Fatal("tail of a finished reply not played exactly once")
	}
}

func TestURCsRoutedToTheATPort(t *testing.T) {
	_, _, fake, _ := healthService(t, Options{})
	waitFor(t, "urc port set", func() bool { return fake.saw(`AT+QURCCFG="urcport","usbat"`) })
}

func TestAssistantLeavesCallWhenModelDown(t *testing.T) {
	ai := &fakeAssistant{delay: 300 * time.Millisecond, readyErr: errors.New("连接实时语音接口失败（503）"), done: make(chan struct{})}
	svc, st, fake, _ := healthService(t, Options{Assistant: ai})

	fake.ring(modem.Call{ID: 3, Incoming: true, State: modem.CallIncoming, Number: "13800000008"})
	waitFor(t, "pickup scheduled", func() bool { return !svc.Snapshot().Assistant.AnswerAt.IsZero() })
	waitFor(t, "pickup called off", func() bool { return svc.Snapshot().Assistant.AnswerAt.IsZero() })
	time.Sleep(1500 * time.Millisecond)
	if fake.saw("ATA") {
		t.Fatal("answered a call the assistant cannot talk on")
	}
	if len(currentCalls(svc)) != 1 {
		t.Fatal("the call stopped ringing")
	}
	events, _, err := st.ListEvents(context.Background(), store.EventFilter{}, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events {
		found = found || strings.Contains(e.Text, "AI 助手没有接听")
	}
	if !found {
		t.Fatalf("no log entry explains the unanswered call: %+v", events)
	}
}

func TestFailedPickupLeavesCallToOwner(t *testing.T) {
	ai := &fakeAssistant{delay: 300 * time.Millisecond, done: make(chan struct{})}
	svc, _, fake, _ := healthService(t, Options{Assistant: ai})
	fake.set(func(f *healthModem) { f.failATA = true })

	fake.ring(modem.Call{ID: 3, Incoming: true, State: modem.CallIncoming, Number: "13800000007"})
	waitFor(t, "pickup scheduled", func() bool { return !svc.Snapshot().Assistant.AnswerAt.IsZero() })
	waitFor(t, "pickup tried", func() bool { return fake.saw("ATA") })
	// The UI must not keep saying the assistant is picking up.
	waitFor(t, "pickup called off", func() bool { return svc.Snapshot().Assistant.AnswerAt.IsZero() })
	if len(currentCalls(svc)) != 1 {
		t.Fatal("the call stopped ringing")
	}
}
