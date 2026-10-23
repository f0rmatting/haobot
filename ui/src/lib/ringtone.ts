// A synthesised ringtone (no audio file, so nothing extra under the CSP).
// Browsers may block it until the page has had a click; the incoming-call
// dialog still shows.

let ctx: AudioContext | null = null;
let timer: number | undefined;

function burst(c: AudioContext, at: number) {
  for (const [freq, offset] of [
    [880, 0],
    [660, 0.18],
  ] as const) {
    const osc = c.createOscillator();
    const gain = c.createGain();
    osc.type = "sine";
    osc.frequency.value = freq;
    gain.gain.setValueAtTime(0, at + offset);
    gain.gain.linearRampToValueAtTime(0.18, at + offset + 0.02);
    gain.gain.linearRampToValueAtTime(0, at + offset + 0.16);
    osc.connect(gain).connect(c.destination);
    osc.start(at + offset);
    osc.stop(at + offset + 0.17);
  }
}

export function startRingtone() {
  if (timer !== undefined) return;
  try {
    ctx = new AudioContext();
  } catch {
    return;
  }
  const ring = () => {
    if (!ctx) return;
    const t = ctx.currentTime + 0.05;
    burst(ctx, t);
    burst(ctx, t + 0.4);
  };
  ring();
  timer = window.setInterval(ring, 2500);
}

export function stopRingtone() {
  if (timer !== undefined) window.clearInterval(timer);
  timer = undefined;
  ctx?.close().catch(() => {});
  ctx = null;
}
