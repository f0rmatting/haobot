package assistant

import "math"

// The phone line carries 8 kHz audio; the model hears 16 kHz and speaks at
// a higher rate (24 kHz by default). All audio is 16-bit little-endian mono.

const lineRate = 8000

// upsampler doubles 8 kHz audio to 16 kHz by linear interpolation; the
// phone band holds nothing above 4 kHz to recover anyway.
type upsampler struct{ last int16 }

func (u *upsampler) process(in []byte) []byte {
	out := make([]byte, 0, len(in)*2)
	for i := 0; i+1 < len(in); i += 2 {
		v := int16(uint16(in[i]) | uint16(in[i+1])<<8)
		mid := int16((int32(u.last) + int32(v)) / 2)
		out = append(out, byte(mid), byte(uint16(mid)>>8), byte(v), byte(uint16(v)>>8))
		u.last = v
	}
	return out
}

// decimator brings the model's speech down to 8 kHz through a low-pass
// filter, keeping state between chunks so their joins stay seamless.
type decimator struct {
	ratio int
	taps  []float64
	hist  []float64 // the last len(taps)-1 input samples
	next  int       // index in hist+input of the next output sample
	carry []byte    // an odd byte left over from the last chunk
}

func newDecimator(inRate int) *decimator {
	ratio := max(1, inRate/lineRate)
	d := &decimator{ratio: ratio}
	if ratio == 1 {
		return d
	}
	// Windowed sinc with its cutoff just below the telephone band edge.
	n := 16*ratio + 1
	fc := 3600.0 / float64(inRate)
	m := float64(n-1) / 2
	d.taps = make([]float64, n)
	sum := 0.0
	for k := range n {
		x := float64(k) - m
		h := 2 * fc
		if x != 0 {
			h = math.Sin(2*math.Pi*fc*x) / (math.Pi * x)
		}
		w := 0.42 - 0.5*math.Cos(2*math.Pi*float64(k)/float64(n-1)) + 0.08*math.Cos(4*math.Pi*float64(k)/float64(n-1))
		d.taps[k] = h * w
		sum += d.taps[k]
	}
	for k := range d.taps {
		d.taps[k] /= sum
	}
	d.hist = make([]float64, n-1)
	d.next = n - 1
	return d
}

func (d *decimator) process(in []byte) []byte {
	if len(d.carry) > 0 {
		in = append(d.carry, in...)
		d.carry = nil
	}
	if len(in)%2 == 1 {
		d.carry = []byte{in[len(in)-1]}
		in = in[:len(in)-1]
	}
	if d.ratio == 1 {
		return append([]byte(nil), in...)
	}
	buf := make([]float64, len(d.hist), len(d.hist)+len(in)/2)
	copy(buf, d.hist)
	for i := 0; i+1 < len(in); i += 2 {
		buf = append(buf, float64(int16(uint16(in[i])|uint16(in[i+1])<<8)))
	}
	l := len(d.taps)
	var out []byte
	for ; d.next < len(buf); d.next += d.ratio {
		acc := 0.0
		window := buf[d.next-l+1 : d.next+1]
		for k, t := range d.taps {
			acc += t * window[k]
		}
		v := int16(max(-32768, min(32767, math.Round(acc))))
		out = append(out, byte(v), byte(uint16(v)>>8))
	}
	drop := len(buf) - (l - 1)
	d.hist = append(d.hist[:0], buf[drop:]...)
	d.next -= drop
	return out
}
