package service

import (
	"encoding/binary"
	"math"
	"testing"
)

// speech makes a smooth, speech-like signal: a few voices with a slowly
// changing loudness.
func speech(samples int) []byte {
	out := make([]byte, 2*samples)
	for i := 0; i < samples; i++ {
		t := float64(i) / 8000
		env := 0.6 + 0.4*math.Sin(2*math.Pi*1.5*t)
		v := env * (7000*math.Sin(2*math.Pi*220*t) + 3000*math.Sin(2*math.Pi*450*t) + 1500*math.Sin(2*math.Pi*900*t))
		binary.LittleEndian.PutUint16(out[2*i:], uint16(int16(v)))
	}
	return out
}

// realign runs a byte stream through the aligner the way pumpAudio does
// and returns the frames it would emit.
func realign(stream []byte) (frames [][]byte, fixes int) {
	var a aligner
	for len(stream) > audioFrame {
		if a.check(stream) {
			stream = stream[1:]
			fixes++
			continue
		}
		frames = append(frames, stream[:audioFrame])
		stream = stream[audioFrame:]
	}
	return frames, fixes
}

func TestAlignerRepairsALostByte(t *testing.T) {
	clean := speech(8000 * 3)
	// The modem loses one byte after 1.2 s.
	cut := 2*9600 + 1
	broken := append(append([]byte{}, clean[:cut]...), clean[cut+1:]...)
	frames, fixes := realign(broken)
	if fixes != 1 {
		t.Fatalf("realigned %d times, want 1", fixes)
	}
	// Everything from 0.1 s after the glitch must be smooth again.
	for i, f := range frames {
		if i*audioFrame < cut+5*audioFrame {
			continue
		}
		if r, _ := roughness(f); r > alignGoodAt {
			t.Fatalf("frame %d still rough (%.2f) after the repair", i, r)
		}
	}
}

func TestAlignerLeavesGoodAudioAlone(t *testing.T) {
	if _, fixes := realign(speech(8000 * 3)); fixes != 0 {
		t.Fatalf("aligned speech was shifted %d times", fixes)
	}
	if _, fixes := realign(make([]byte, 16000)); fixes != 0 {
		t.Fatal("silence was shifted")
	}
}
