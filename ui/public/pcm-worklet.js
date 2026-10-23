// AudioWorklet processors bridging the browser to call audio: 8 kHz, 16-bit
// little-endian mono PCM in 20 ms frames, as the modem's AT+QPCMV produces.
// Served as a plain file so the strict CSP (script-src 'self') allows it.

const MODEM_RATE = 8000;

// pcm-player plays frames posted to its port. A jitter buffer absorbs
// network and USB bursts; when it grows too long the oldest audio is dropped
// so the delay stays low. Starting and stopping fade over a few
// milliseconds: a hard cut to silence is what sounds like a "pop".
const FADE = 96; // output samples, about 2 ms

class PcmPlayer extends AudioWorkletProcessor {
  constructor() {
    super();
    this.buf = new Float32Array(MODEM_RATE * 2); // 2 s ring buffer
    this.read = 0;
    this.write = 0;
    this.pos = 0; // fractional read position within the current sample
    this.playing = false;
    this.gain = 0; // 0..1, ramps for fades
    this.last = 0; // last sample played, faded out on underrun
    this.step = MODEM_RATE / sampleRate;
    this.target = MODEM_RATE * 0.1; // start playing with 100 ms buffered
    this.max = MODEM_RATE * 0.4; // never lag more than 400 ms
    this.port.onmessage = (e) => {
      const pcm = new Int16Array(e.data);
      for (let i = 0; i < pcm.length; i++) {
        this.buf[this.write] = pcm[i] / 32768;
        this.write = (this.write + 1) % this.buf.length;
      }
      if (this.available() > this.max) {
        this.read = (this.write - this.target + this.buf.length) % this.buf.length;
      }
    };
  }

  available() {
    return (this.write - this.read + this.buf.length) % this.buf.length;
  }

  process(_inputs, outputs) {
    const out = outputs[0][0];
    if (!this.playing && this.available() >= this.target) this.playing = true;
    for (let i = 0; i < out.length; i++) {
      if (this.playing && this.available() < 2) this.playing = false; // underrun
      if (this.playing) {
        const a = this.buf[this.read];
        const b = this.buf[(this.read + 1) % this.buf.length];
        this.last = a + (b - a) * this.pos;
        this.pos += this.step;
        while (this.pos >= 1) {
          this.pos -= 1;
          this.read = (this.read + 1) % this.buf.length;
        }
        this.gain = Math.min(1, this.gain + 1 / FADE);
      } else {
        // Hold the last value while fading out instead of jumping to zero.
        this.gain = Math.max(0, this.gain - 1 / FADE);
      }
      out[i] = this.last * this.gain;
    }
    for (let c = 1; c < outputs[0].length; c++) outputs[0][c].set(out);
    return true;
  }
}

// pcm-recorder turns microphone input into 20 ms frames of 8 kHz PCM. Each
// output sample averages the input samples it covers, a simple low-pass
// filter against aliasing.
class PcmRecorder extends AudioWorkletProcessor {
  constructor() {
    super();
    this.ratio = sampleRate / MODEM_RATE;
    this.acc = 0;
    this.count = 0;
    this.phase = 0;
    this.frame = new Int16Array(160);
    this.n = 0;
  }

  process(inputs) {
    const input = inputs[0][0];
    if (!input) return true;
    for (let i = 0; i < input.length; i++) {
      this.acc += input[i];
      this.count++;
      this.phase += 1;
      if (this.phase >= this.ratio) {
        this.phase -= this.ratio;
        const v = Math.max(-1, Math.min(1, this.acc / this.count));
        this.frame[this.n++] = v < 0 ? v * 32768 : v * 32767;
        this.acc = 0;
        this.count = 0;
        if (this.n === this.frame.length) {
          const out = this.frame.slice().buffer;
          this.port.postMessage(out, [out]);
          this.n = 0;
        }
      }
    }
    return true;
  }
}

registerProcessor("pcm-player", PcmPlayer);
registerProcessor("pcm-recorder", PcmRecorder);
