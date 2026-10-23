package assistant

import "math"

// stretcher speeds speech up without raising its pitch (WSOLA: windowed
// overlap-add of input segments chosen to line up with the previous one).
// The realtime models have no speech-rate setting, so the assistant's 8 kHz
// speech goes through this before the phone line.
type stretcher struct {
	speed float64
	win   []float64

	in   []float64 // input not yet consumed; in[0] is input sample base
	base int       // absolute index of in[0]
	ana  float64   // absolute analysis position of the next frame
	prev int       // absolute start of the previous frame; -1 before the first
	tail []float64 // second half of the last frame, still to be overlapped
}

const (
	stretchHop = 120 // 15 ms at 8 kHz: output advances by this per frame
	stretchLen = 2 * stretchHop
	stretchTol = 60 // search ±7.5 ms for the best-matching segment
)

func newStretcher(speed float64) *stretcher {
	s := &stretcher{speed: speed, prev: -1}
	s.win = make([]float64, stretchLen)
	for i := range s.win {
		// Periodic Hann: overlapping halves sum to exactly 1.
		s.win[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/stretchLen)
	}
	s.tail = make([]float64, stretchHop)
	return s
}

func (s *stretcher) active() bool { return s.speed > 1.001 || s.speed < 0.999 }

// process takes 16-bit PCM and returns what is ready of the faster speech.
func (s *stretcher) process(pcm []byte) []byte {
	if !s.active() {
		return pcm
	}
	for i := 0; i+1 < len(pcm); i += 2 {
		s.in = append(s.in, float64(int16(uint16(pcm[i])|uint16(pcm[i+1])<<8)))
	}
	var out []float64
	for {
		target := int(math.Round(s.ana))
		need := target + stretchTol + stretchLen
		if s.prev >= 0 {
			need = max(need, s.prev+stretchHop+stretchLen)
		}
		if need > s.base+len(s.in) {
			break
		}
		start := target
		if s.prev >= 0 {
			start = s.bestMatch(target)
		}
		seg := s.in[start-s.base : start-s.base+stretchLen]
		for i := 0; i < stretchHop; i++ {
			out = append(out, s.tail[i]+seg[i]*s.win[i])
		}
		for i := 0; i < stretchHop; i++ {
			s.tail[i] = seg[stretchHop+i] * s.win[stretchHop+i]
		}
		s.prev = start
		s.ana += stretchHop * s.speed
		s.trim()
	}
	return toPCM(out)
}

// bestMatch picks the start near target whose segment best continues the
// previous one.
func (s *stretcher) bestMatch(target int) int {
	natural := s.in[s.prev+stretchHop-s.base:]
	best, bestScore := target, math.Inf(-1)
	lo := max(target-stretchTol, s.base)
	for p := lo; p <= target+stretchTol; p++ {
		cand := s.in[p-s.base:]
		score := 0.0
		for i := 0; i < stretchHop; i += 2 { // every other sample is plenty
			score += natural[i] * cand[i]
		}
		if score > bestScore {
			best, bestScore = p, score
		}
	}
	return best
}

// trim drops input that no future frame can reach.
func (s *stretcher) trim() {
	keep := min(int(s.ana)-stretchTol, s.prev+stretchHop)
	if drop := keep - s.base; drop > 4096 {
		s.in = append(s.in[:0], s.in[drop:]...)
		s.base += drop
	}
}

// flush returns the remaining speech at the end of a response, unstretched.
func (s *stretcher) flush() []byte {
	if !s.active() {
		return nil
	}
	// The pending tail plus the rest of the previous frame's rising half add
	// up to the plain input from where that frame's second half begins.
	out := s.in
	if s.prev >= 0 {
		out = s.in[s.prev+stretchHop-s.base:]
	}
	pcm := toPCM(out)
	s.reset()
	return pcm
}

// reset forgets everything, when the caller interrupts.
func (s *stretcher) reset() {
	*s = *newStretcher(s.speed)
}

func toPCM(samples []float64) []byte {
	out := make([]byte, 0, 2*len(samples))
	for _, v := range samples {
		x := int16(max(-32768, min(32767, math.Round(v))))
		out = append(out, byte(x), byte(uint16(x)>>8))
	}
	return out
}
