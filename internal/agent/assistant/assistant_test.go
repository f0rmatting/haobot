package assistant

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/f0rmatting/haobot/internal/agent/classify"
)

func TestEndpoint(t *testing.T) {
	cfg := DefaultConfig()
	llm := classify.Config{Provider: classify.ProviderOpenAI, BaseURL: "https://ws-abc.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1"}
	got, err := cfg.Endpoint(llm)
	want := "wss://ws-abc.ap-southeast-1.maas.aliyuncs.com/api-ws/v1/realtime?model=qwen3.8-omni-flash-realtime"
	if err != nil || got != want {
		t.Fatalf("Endpoint = %q, %v; want %q", got, err, want)
	}
	cfg.URL = "wss://example.com/rt?model=x"
	if got, _ := cfg.Endpoint(llm); got != "wss://example.com/rt?model=x" {
		t.Fatalf("explicit URL = %q", got)
	}
	if _, err := DefaultConfig().Endpoint(classify.Config{Provider: classify.ProviderAnthropic}); err == nil {
		t.Fatal("no error without a Bailian base URL")
	}
}

func sine(rate, freq int, d time.Duration, amp float64) []byte {
	n := int(float64(rate) * d.Seconds())
	out := make([]byte, 0, 2*n)
	for i := range n {
		v := int16(amp * math.Sin(2*math.Pi*float64(freq)*float64(i)/float64(rate)))
		out = append(out, byte(v), byte(uint16(v)>>8))
	}
	return out
}

func rms(pcm []byte) float64 {
	sum := 0.0
	for i := 0; i+1 < len(pcm); i += 2 {
		v := float64(int16(uint16(pcm[i]) | uint16(pcm[i+1])<<8))
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(pcm)/2))
}

func TestDecimator(t *testing.T) {
	d := newDecimator(24000)
	// In passband: kept. Above 4 kHz: removed (it would alias).
	var pass, stop []byte
	in := sine(24000, 1000, time.Second, 10000)
	for i := 0; i < len(in); i += 962 { // odd chunk sizes test the carry
		pass = append(pass, d.process(in[i:min(i+962, len(in))])...)
	}
	if len(pass) < 15990 || len(pass) > 16000 {
		t.Fatalf("1 s at 24 kHz became %d bytes at 8 kHz", len(pass))
	}
	if r := rms(pass[1000:]); r < 6800 || r > 7300 {
		t.Fatalf("1 kHz rms = %.0f, want about 7071", r)
	}
	d = newDecimator(24000)
	stop = d.process(sine(24000, 6000, time.Second, 10000))
	if r := rms(stop[1000:]); r > 300 {
		t.Fatalf("6 kHz leaked with rms %.0f", r)
	}
	var u upsampler
	if got := len(u.process(make([]byte, 320))); got != 640 {
		t.Fatalf("upsampled 320 bytes to %d", got)
	}
}

// fakeLine is a call end for the session.
type fakeLine struct {
	in   chan []byte
	done chan struct{}
	once sync.Once

	mu      sync.Mutex
	played  []byte
	flushes int
	hungUp  bool
	phases  []string
	errs    []string
	lines   []string
}

func newFakeLine() *fakeLine { return &fakeLine{in: make(chan []byte, 200), done: make(chan struct{})} }

func (l *fakeLine) Frames() <-chan []byte { return l.in }
func (l *fakeLine) Done() <-chan struct{} { return l.done }
func (l *fakeLine) Play(p []byte)         { l.mu.Lock(); l.played = append(l.played, p...); l.mu.Unlock() }
func (l *fakeLine) Flush()                { l.mu.Lock(); l.flushes++; l.mu.Unlock() }
func (l *fakeLine) EndSpeech()            {}
func (l *fakeLine) Queued() time.Duration { return 0 }
func (l *fakeLine) Hangup(context.Context) error {
	l.mu.Lock()
	l.hungUp = true
	l.mu.Unlock()
	l.once.Do(func() { close(l.done) })
	return nil
}
func (l *fakeLine) SetPhase(p string) { l.mu.Lock(); l.phases = append(l.phases, p); l.mu.Unlock() }
func (l *fakeLine) SetError(e string) { l.mu.Lock(); l.errs = append(l.errs, e); l.mu.Unlock() }
func (l *fakeLine) Transcript(role, text string, final bool) {
	if !final {
		return
	}
	l.mu.Lock()
	l.lines = append(l.lines, role+":"+text)
	l.mu.Unlock()
}

// fakeRealtime plays the server side of one call: it greets, waits for a
// second of caller audio, "hears" the caller, says goodbye and calls
// end_call.
func fakeRealtime(t *testing.T, gotAuth *string, gotAudio *int, update *map[string]any) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotAuth = r.Header.Get("Authorization")
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		ctx := r.Context()
		ws.SetReadLimit(1 << 22)
		send := func(e map[string]any) {
			data, _ := json.Marshal(e)
			ws.Write(ctx, websocket.MessageText, data)
		}
		speak := func(text string, endCall bool) {
			send(map[string]any{"type": "response.created"})
			send(map[string]any{"type": "response.audio.delta", "delta": base64.StdEncoding.EncodeToString(sine(24000, 500, 300*time.Millisecond, 8000))})
			send(map[string]any{"type": "response.audio_transcript.delta", "delta": text})
			send(map[string]any{"type": "response.audio_transcript.done", "transcript": text})
			if endCall {
				send(map[string]any{"type": "response.function_call_arguments.done", "name": "end_call", "call_id": "call_1", "arguments": "{}"})
			}
			send(map[string]any{"type": "response.done", "response": map[string]any{"usage": map[string]any{"input_tokens": 10, "output_tokens": 5}}})
		}
		heard, hasInput := false, false
		for {
			_, data, err := ws.Read(ctx)
			if err != nil {
				return
			}
			var e map[string]any
			json.Unmarshal(data, &e)
			switch e["type"] {
			case "session.update":
				fakeMu.Lock()
				*update = e
				fakeMu.Unlock()
				send(map[string]any{"type": "session.updated", "session": map[string]any{}})
			case "conversation.item.create":
				item := e["item"].(map[string]any)
				if item["type"] == "message" {
					content := item["content"].([]any)[0].(map[string]any)
					hasInput = content["type"] == "input_text" && content["text"] != ""
				}
			case "response.create":
				if !hasInput {
					send(map[string]any{"type": "error", "error": map[string]any{"code": "invalid_request_error", "message": "Cannot create response without input, history, or instructions"}})
					continue
				}
				speak("您好，我是机主的 AI 助理", false)
			case "input_audio_buffer.append":
				audio, _ := base64.StdEncoding.DecodeString(e["audio"].(string))
				fakeMu.Lock()
				*gotAudio += len(audio)
				enough := *gotAudio >= 32000
				fakeMu.Unlock()
				if enough && !heard {
					heard = true
					send(map[string]any{"type": "input_audio_buffer.speech_started"})
					send(map[string]any{"type": "conversation.item.input_audio_transcription.completed", "transcript": "我是快递，放门口可以吗"})
					speak("好的，我会转告机主，再见", true)
				}
			}
		}
	}))
}

// fakeMu guards what fakeRealtime records, read by the test while the
// server may still be handling the last messages.
var fakeMu sync.Mutex

func TestTalk(t *testing.T) {
	var auth string
	var audio int
	var update map[string]any
	srv := fakeRealtime(t, &auth, &audio, &update)
	defer srv.Close()
	line := newFakeLine()
	go func() {
		// 1.2 s of caller audio in 20 ms frames.
		for range 60 {
			line.in <- make([]byte, 320)
		}
	}()
	cfg := DefaultConfig()
	cfg.Instructions = "快递放门口"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ss, err := open(ctx, cfg, "ws"+strings.TrimPrefix(srv.URL, "http")+"/api-ws/v1/realtime?model=m", "sk-test")
	if err != nil {
		t.Fatal(err)
	}
	usage, err := talk(ctx, cfg, ss, line)
	if err != nil {
		t.Fatal(err)
	}
	fakeMu.Lock()
	defer fakeMu.Unlock()
	if auth != "Bearer sk-test" {
		t.Fatalf("Authorization = %q", auth)
	}
	session := update["session"].(map[string]any)
	output := session["audio"].(map[string]any)["output"].(map[string]any)
	if session["voice"] != cfg.Voice || output["voice"] != cfg.Voice {
		t.Fatalf("voice not sent both ways: session.voice=%v audio.output.voice=%v", session["voice"], output["voice"])
	}
	if !strings.Contains(session["instructions"].(string), "快递放门口") || !strings.Contains(session["instructions"].(string), cfg.Greeting) {
		t.Fatal("instructions lack the greeting or the owner's notes")
	}
	if audio < 32000 || audio%640 != 0 {
		t.Fatalf("sent %d bytes of 16 kHz audio", audio)
	}
	line.mu.Lock()
	defer line.mu.Unlock()
	if !line.hungUp {
		t.Fatal("did not hang up after end_call")
	}
	// Two 300 ms responses at 8 kHz.
	if len(line.played) < 9000 || len(line.played) > 9700 {
		t.Fatalf("played %d bytes", len(line.played))
	}
	if line.flushes != 1 {
		t.Fatalf("flushes = %d, want 1 (barge-in)", line.flushes)
	}
	want := []string{"assistant:您好，我是机主的 AI 助理", "caller:我是快递，放门口可以吗", "assistant:好的，我会转告机主，再见"}
	if strings.Join(line.lines, "|") != strings.Join(want, "|") {
		t.Fatalf("transcript = %q", line.lines)
	}
	if usage.InputTokens != 20 || usage.OutputTokens != 10 {
		t.Fatalf("usage = %+v", usage)
	}
	if len(line.errs) > 0 {
		t.Fatalf("errors: %v", line.errs)
	}
}

func TestTalkReportsServerErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		ws.Read(r.Context())
		ws.Write(r.Context(), websocket.MessageText, []byte(`{"type":"error","error":{"code":"invalid_api_key","message":"bad key"}}`))
		time.Sleep(100 * time.Millisecond)
	}))
	defer srv.Close()
	_, _, err := Test(context.Background(), DefaultConfig(), "ws"+strings.TrimPrefix(srv.URL, "http"), "x")
	if err == nil || !strings.Contains(err.Error(), "invalid_api_key") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateRejectsNonConversationModels(t *testing.T) {
	for _, m := range []string{"qwen3-tts-flash-realtime", "qwen3-asr-flash-realtime", "qwen3.8-livetranslate-flash-realtime"} {
		cfg := DefaultConfig()
		cfg.Model = m
		if err := cfg.Validate(); err == nil {
			t.Errorf("%s accepted", m)
		}
	}
	for _, m := range []string{"qwen3.8-omni-flash-realtime", "qwen3.5-omni-flash-realtime-2025-12-01"} {
		cfg := DefaultConfig()
		cfg.Model = m
		if err := cfg.Validate(); err != nil {
			t.Errorf("%s rejected: %v", m, err)
		}
	}
}

// zeroCrossings counts sign changes, a rough frequency measure.
func zeroCrossings(pcm []byte) int {
	n, prev := 0, 0.0
	for i := 0; i+1 < len(pcm); i += 2 {
		v := float64(int16(uint16(pcm[i]) | uint16(pcm[i+1])<<8))
		if (v >= 0) != (prev >= 0) {
			n++
		}
		prev = v
	}
	return n
}

func TestStretcherKeepsPitch(t *testing.T) {
	in := sine(8000, 300, 2*time.Second, 8000)
	s := newStretcher(1.25)
	var out []byte
	for i := 0; i < len(in); i += 640 { // 40 ms chunks, as the model streams
		out = append(out, s.process(in[i:min(i+640, len(in))])...)
	}
	body := len(out)
	out = append(out, s.flush()...)
	// 2 s at 1.25x is 1.6 s, plus the unstretched remainder from flush.
	if got := float64(body) / 16000; got < 1.5 || got > 1.65 {
		t.Fatalf("stretched part lasts %.2f s, want about 1.6", got)
	}
	// Same pitch: crossings per second unchanged (300 Hz -> 600/s).
	rate := float64(zeroCrossings(out[:body])) / (float64(body) / 16000)
	if rate < 570 || rate > 630 {
		t.Fatalf("pitch changed: %.0f zero crossings/s, want about 600", rate)
	}
	// Smooth: no clicks where frames join.
	if r := rms(out[2000:body]); r < 5300 || r > 6000 {
		t.Fatalf("level %.0f, want about 5657", r)
	}
	if p := newStretcher(1); string(p.process(in[:100])) != string(in[:100]) || p.flush() != nil {
		t.Fatal("speed 1 should pass audio through")
	}
}

// formatServer answers session.update like Bailian: it echoes the rates it
// takes and rejects the ones it does not.
func formatServer(t *testing.T, accepts func(in, out float64) bool) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		for {
			_, data, err := ws.Read(r.Context())
			if err != nil {
				return
			}
			var e map[string]any
			json.Unmarshal(data, &e)
			if e["type"] != "session.update" {
				continue
			}
			audio := e["session"].(map[string]any)["audio"].(map[string]any)
			in := audio["input"].(map[string]any)["format"].(map[string]any)["sample_rate"].(float64)
			out := audio["output"].(map[string]any)["format"].(map[string]any)["sample_rate"].(float64)
			reply := `{"type":"error","error":{"code":"invalid_value","message":"unsupported sample_rate"}}`
			if accepts(in, out) {
				b, _ := json.Marshal(map[string]any{"type": "session.updated", "session": map[string]any{"audio": audio}})
				reply = string(b)
			}
			ws.Write(r.Context(), websocket.MessageText, []byte(reply))
		}
	}))
}

func TestConfigurePrefersNarrowband(t *testing.T) {
	for _, tc := range []struct {
		name    string
		accepts func(in, out float64) bool
		want    audioFormat
	}{
		{"takes 8 kHz", func(in, out float64) bool { return true }, audioFormat{8000, 8000}},
		{"only the classic rates", func(in, out float64) bool { return in == 16000 }, audioFormat{16000, 24000}},
	} {
		srv := formatServer(t, tc.accepts)
		c, err := dial(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http"), "k")
		if err != nil {
			t.Fatal(err)
		}
		events, _ := c.events(context.Background())
		got, err := configure(context.Background(), c, events, DefaultConfig(), "p")
		c.close()
		srv.Close()
		if err != nil || got != tc.want {
			t.Errorf("%s: format %v, %v; want %v", tc.name, got, err, tc.want)
		}
	}
}

func TestInstructionsUseOwnerName(t *testing.T) {
	cfg := DefaultConfig()
	cfg.OwnerName, cfg.Style, cfg.Instructions = "张先生", StyleFormal, "快递放门口"
	p := instructions(cfg)
	for _, want := range []string{"你正在替张先生接一通电话", "我帮张先生接一下电话", "谢谢您来电", "张先生的补充说明", "快递放门口", "认真地问你是不是真人", cfg.Greeting} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	if strings.Contains(p, "%!") {
		t.Fatal("format verbs left in the prompt")
	}
	// Without a name the prompt never tells the model to say 机主.
	cfg.OwnerName = ""
	if p := instructions(cfg); strings.Count(p, "机主") != 1 { // only in “don't say 机主”
		t.Fatalf("机主 appears %d times", strings.Count(p, "机主"))
	}
}

func TestWarmupFailureLeavesCallToOwner(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
	}))
	defer down.Close()
	a := &Agent{
		LLM: func(context.Context) (classify.Config, error) {
			return classify.Config{Provider: classify.ProviderOpenAI, APIKey: "sk-test"}, nil
		},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	cfg := DefaultConfig()
	cfg.URL = "ws" + strings.TrimPrefix(down.URL, "http") + "/api-ws/v1/realtime"
	a.prepare(context.Background(), "dev1", cfg)

	var err error
	deadline := time.Now().Add(5 * time.Second)
	for ready := false; err == nil && !ready; {
		if time.Now().After(deadline) {
			t.Fatal("warmup never finished")
		}
		ready, err = a.Ready("dev1")
		if ready {
			t.Fatal("ready with the model down")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(err.Error(), "503") {
		t.Fatalf("err = %v", err)
	}
	// The failed session is dropped: a later manual hand-over opens anew.
	if ready, err := a.Ready("dev1"); !ready || err != nil {
		t.Fatalf("after failure Ready = %v, %v", ready, err)
	}
}

// TestTalkAsksForAMissingReply: the server hears the caller out but never
// starts a reply on its own.
func TestTalkAsksForAMissingReply(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		ctx := r.Context()
		ws.SetReadLimit(1 << 22)
		send := func(e map[string]any) {
			data, _ := json.Marshal(e)
			ws.Write(ctx, websocket.MessageText, data)
		}
		creates, audio, heard := 0, 0, false
		for {
			_, data, err := ws.Read(ctx)
			if err != nil {
				return
			}
			var e map[string]any
			json.Unmarshal(data, &e)
			switch e["type"] {
			case "session.update":
				send(map[string]any{"type": "session.updated", "session": map[string]any{}})
			case "response.create":
				creates++
				send(map[string]any{"type": "response.created"})
				if creates == 2 {
					send(map[string]any{"type": "response.function_call_arguments.done", "name": "end_call", "call_id": "c", "arguments": "{}"})
				}
				send(map[string]any{"type": "response.done", "response": map[string]any{}})
			case "input_audio_buffer.append":
				b, _ := base64.StdEncoding.DecodeString(e["audio"].(string))
				if audio += len(b); audio >= 16000 && !heard {
					heard = true
					send(map[string]any{"type": "input_audio_buffer.speech_started"})
					send(map[string]any{"type": "input_audio_buffer.speech_stopped"})
					send(map[string]any{"type": "conversation.item.input_audio_transcription.completed", "transcript": "喂，有人吗"})
				}
			}
		}
	}))
	defer srv.Close()
	line := newFakeLine()
	go func() {
		for {
			select {
			case line.in <- make([]byte, 320):
				time.Sleep(20 * time.Millisecond)
			case <-line.done:
				return
			}
		}
	}()
	cfg := DefaultConfig()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ss, err := open(ctx, cfg, "ws"+strings.TrimPrefix(srv.URL, "http")+"/api-ws/v1/realtime?model=m", "sk-test")
	if err != nil {
		t.Fatal(err)
	}
	usage, err := talk(ctx, cfg, ss, line)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Nudged != 1 || usage.Replies != 2 || usage.Heard != 1 || usage.SpeechTurns != 1 {
		t.Fatalf("usage = %+v; want one reply asked for", usage)
	}
	if usage.CallerSec == 0 || usage.TalkSec == 0 {
		t.Fatalf("call flow not measured: %+v", usage)
	}
	line.mu.Lock()
	defer line.mu.Unlock()
	if !line.hungUp {
		t.Fatal("the asked-for reply did not end the call")
	}
}

func TestInstructionsDiscloseAIWhenAsked(t *testing.T) {
	cfg := DefaultConfig()
	cfg.OwnerName = "张先生"
	if p := instructions(cfg); strings.Contains(p, "紧接着用一句话说明") || !strings.Contains(p, "不要主动介绍自己") {
		t.Fatal("by default the assistant should not announce itself")
	}
	cfg.DiscloseAI = true
	if p := instructions(cfg); !strings.Contains(p, "我是帮张先生接电话的 AI 助手") || strings.Contains(p, "不要主动介绍自己") {
		t.Fatal("DiscloseAI does not make the assistant say it is an AI")
	}
}

// The caller can talk the summary model into anything; what steers the
// push only takes known values.
func TestSummaryFieldsAreChecked(t *testing.T) {
	s := Summary{Urgency: "紧急！！", Category: "妈妈", Brief: strings.Repeat("长", 500)}.checked()
	if s.Urgency != "中" || s.Category != "其他" || len([]rune(s.Brief)) > 80 {
		t.Fatalf("checked = %+v", s)
	}
	if s := (Summary{Urgency: "高", Category: "工作"}).checked(); s.Urgency != "高" || s.Category != "工作" {
		t.Fatalf("valid values changed: %+v", s)
	}
}
