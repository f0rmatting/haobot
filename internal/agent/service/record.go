package service

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	// maxRecording caps a recording at one hour.
	maxRecording = 3600 * 8000
	// recordSlack is how far a stream may fall behind the clock before
	// the gap is filled with silence: bursts within it are kept in order.
	recordSlack = 800 // 100 ms
)

// recorder keeps both directions of a call, each placed by wall-clock time
// so they stay aligned when one of them stalls.
type recorder struct {
	start time.Time

	mu     sync.Mutex
	tracks [2]track // 0: the other side, 1: this side
}

type track struct {
	samples []int16
	pos     int
}

const (
	trackDown = 0
	trackUp   = 1
)

func newRecorder() *recorder { return &recorder{start: time.Now()} }

// add places a frame of 8 kHz 16-bit PCM that just arrived (downlink) or
// was just sent (uplink).
func (r *recorder) add(which int, frame []byte) {
	n := len(frame) / 2
	at := int(time.Since(r.start).Seconds()*8000) - n
	r.mu.Lock()
	defer r.mu.Unlock()
	t := &r.tracks[which]
	if t.pos < at-recordSlack {
		t.pos = at
	}
	if t.pos+n > maxRecording {
		return
	}
	if need := t.pos + n - len(t.samples); need > 0 {
		t.samples = append(t.samples, make([]int16, need)...)
	}
	for i := 0; i < n; i++ {
		t.samples[t.pos+i] = int16(uint16(frame[2*i]) | uint16(frame[2*i+1])<<8)
	}
	t.pos += n
}

// wav mixes both directions into a mono 8 kHz WAV file.
func (r *recorder) wav() ([]byte, time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := max(len(r.tracks[0].samples), len(r.tracks[1].samples))
	out := make([]byte, 44+2*n)
	copy(out, "RIFF")
	binary.LittleEndian.PutUint32(out[4:], uint32(36+2*n))
	copy(out[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(out[16:], 16)
	binary.LittleEndian.PutUint16(out[20:], 1) // PCM
	binary.LittleEndian.PutUint16(out[22:], 1) // mono
	binary.LittleEndian.PutUint32(out[24:], 8000)
	binary.LittleEndian.PutUint32(out[28:], 16000)
	binary.LittleEndian.PutUint16(out[32:], 2)
	binary.LittleEndian.PutUint16(out[34:], 16)
	copy(out[36:], "data")
	binary.LittleEndian.PutUint32(out[40:], uint32(2*n))
	for i := 0; i < n; i++ {
		v := int32(0)
		for _, t := range r.tracks {
			if i < len(t.samples) {
				v += int32(t.samples[i])
			}
		}
		x := int16(max(-32768, min(32767, v)))
		binary.LittleEndian.PutUint16(out[44+2*i:], uint16(x))
	}
	return out, time.Duration(n) * time.Second / 8000
}

// saveRecording writes a finished call's recording when the policy wants
// it and links it to the call record.
func (s *Service) saveRecording(ctx context.Context, b *audioBridge) {
	if b.rec == nil || b.recordID == 0 {
		return
	}
	keep := s.opts.KeepRecording
	if keep == nil || !keep(ctx, s.DeviceID(), b.byAI) {
		return
	}
	data, length := b.rec.wav()
	if length < time.Second {
		return
	}
	rel := filepath.Join(safeName(s.DeviceID()), fmt.Sprintf("call-%d-%s.wav", b.recordID, b.rec.start.Format("20060102-150405")))
	path := filepath.Join(s.opts.RecordingDir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		s.note(ctx, "warn", "保存通话录音失败："+err.Error())
		return
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		s.note(ctx, "warn", "保存通话录音失败："+err.Error())
		return
	}
	if err := s.store.SetCallRecording(ctx, b.recordID, rel); err != nil {
		s.note(ctx, "warn", "保存通话录音失败："+err.Error())
		return
	}
	s.hub.Publish(TopicCalls)
}

// safeName keeps a device id usable as a directory name.
func safeName(id string) string {
	out := []rune(id)
	for i, r := range out {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '-' || r == '_') {
			out[i] = '_'
		}
	}
	if len(out) == 0 {
		return "unknown"
	}
	return string(out)
}
