import { useSyncExternalStore } from "react";

// CallAudio connects the browser's speaker and microphone to a device's call
// audio over a WebSocket. One instance for the whole app: only one call can
// be heard at a time.

export type AudioPhase = "idle" | "connecting" | "live" | "error";

export interface AudioSnapshot {
  phase: AudioPhase;
  deviceId: string;
  muted: boolean;
  micOk: boolean; // false: listening only
  error: string;
  level: number; // 0..1 microphone level, for the meter
  rx: number; // frames received from the call, to tell silence from no audio
  // micSilent is set when the microphone delivered only silence for a few
  // seconds: on macOS a missing system permission looks exactly like that.
  micSilent: boolean;
  // micInfo describes the capture, e.g. "MacBook Pro 麦克风 · 48000 Hz".
  micInfo: string;
}

let state: AudioSnapshot = { phase: "idle", deviceId: "", muted: false, micOk: false, error: "", level: 0, rx: 0, micSilent: false, micInfo: "" };
let rx = 0;
const listeners = new Set<() => void>();
const set = (patch: Partial<AudioSnapshot>) => {
  state = { ...state, ...patch };
  listeners.forEach((l) => l());
};

let ctx: AudioContext | null = null;
let ws: WebSocket | null = null;
let mic: MediaStream | null = null;
let player: AudioWorkletNode | null = null;
let recorder: AudioWorkletNode | null = null;
// Capture runs in its own context, created after the microphone opened:
// opening a headset's microphone can switch the audio device (Bluetooth
// goes to its call profile at another sample rate), and a context created
// before that switch receives only silence from it.
let captureCtx: AudioContext | null = null;
let micSource: MediaStreamAudioSourceNode | null = null; // kept so it is not collected

function micError(e: unknown): string {
  if (!window.isSecureContext) return "浏览器只允许在 HTTPS 或 localhost 下使用麦克风，当前只能听不能说";
  const name = (e as DOMException)?.name;
  if (name === "NotAllowedError") return "麦克风权限被拒绝，当前只能听不能说（可在浏览器地址栏旁重新授权）";
  if (name === "NotFoundError") return "没有找到麦克风，当前只能听不能说";
  return "无法打开麦克风，当前只能听不能说";
}

const inputKey = "haobot.mic";

// The microphone chosen on the 电话 page; "" means the system default.
export function preferredInput(): string {
  try {
    return localStorage.getItem(inputKey) ?? "";
  } catch {
    return "";
  }
}

export function setPreferredInput(id: string) {
  try {
    localStorage.setItem(inputKey, id);
  } catch {
    /* ignore */
  }
}

export function micConstraints(): MediaTrackConstraints {
  const id = preferredInput();
  return { echoCancellation: true, noiseSuppression: true, autoGainControl: true, channelCount: 1, ...(id ? { deviceId: { exact: id } } : {}) };
}

// Below this peak level (about -54 dBFS) a microphone is delivering silence.
const silentPeak = 0.002;

async function attachMic(socket: WebSocket) {
  try {
    const stream = await navigator.mediaDevices.getUserMedia({ audio: micConstraints() });
    if (ws !== socket || !ctx) {
      stream.getTracks().forEach((t) => t.stop()); // the call ended meanwhile
      return;
    }
    mic = stream;
    const track = stream.getAudioTracks()[0];
    const rate = track?.getSettings().sampleRate;
    try {
      captureCtx = rate ? new AudioContext({ sampleRate: rate }) : new AudioContext();
    } catch {
      captureCtx = new AudioContext(); // the browser refused that rate
    }
    await captureCtx.audioWorklet.addModule("/pcm-worklet.js");
    if (ws !== socket) return;
    recorder = new AudioWorkletNode(captureCtx, "pcm-recorder");
    micSource = captureCtx.createMediaStreamSource(stream);
    micSource.connect(recorder);
    // The recorder must be pulled by the graph; route it to a muted sink.
    const sink = captureCtx.createGain();
    sink.gain.value = 0;
    recorder.connect(sink).connect(captureCtx.destination);
    await captureCtx.resume();
    set({ micInfo: `${track?.label || "麦克风"} · ${rate ?? "?"} Hz → ${captureCtx.sampleRate} Hz` });
    let peak = 0;
    let frames = 0;
    let quietFrames = 0;
    recorder.port.onmessage = (e) => {
      const pcm = new Int16Array(e.data);
      let framePeak = 0;
      for (let i = 0; i < pcm.length; i++) framePeak = Math.max(framePeak, Math.abs(pcm[i]) / 32768);
      peak = Math.max(peak, framePeak);
      quietFrames = framePeak < silentPeak && !state.muted ? quietFrames + 1 : 0;
      if (++frames % 5 === 0) {
        // 200 frames = 4 s of nothing but silence.
        set({ level: state.muted ? 0 : Math.min(1, peak * 2), micSilent: quietFrames >= 200 });
        peak = 0;
      }
      if (!state.muted && socket.readyState === WebSocket.OPEN) socket.send(e.data);
    };
    set({ micOk: true, error: "" });
  } catch (e) {
    if (ws === socket) set({ micOk: false, error: micError(e) });
  }
}

export const callAudio = {
  // start must run inside a click handler: browsers only allow audio after a
  // user gesture.
  async start(deviceId: string) {
    if (state.phase !== "idle" && state.phase !== "error" && state.deviceId === deviceId) return;
    callAudio.stop();
    set({ phase: "connecting", deviceId, error: "", muted: false, micOk: false, rx: 0, micSilent: false, micInfo: "" });
    try {
      ctx = new AudioContext({ latencyHint: "interactive" });
      await ctx.audioWorklet.addModule("/pcm-worklet.js");
      player = new AudioWorkletNode(ctx, "pcm-player", { outputChannelCount: [1] });
      player.connect(ctx.destination);

      const proto = location.protocol === "https:" ? "wss:" : "ws:";
      const socket = new WebSocket(`${proto}//${location.host}/api/devices/${encodeURIComponent(deviceId)}/call/audio`);
      ws = socket;
      socket.binaryType = "arraybuffer";
      rx = 0;
      socket.onmessage = (e) => {
        player?.port.postMessage(e.data, [e.data]);
        // Publishing every frame would re-render 50 times a second.
        if (++rx === 1 || rx % 25 === 0) set({ rx });
      };
      socket.onclose = () => {
        if (ws === socket && (state.phase === "live" || state.phase === "connecting")) set({ phase: "error", error: "通话音频连接已断开" });
      };
      // The speaker works as soon as the socket is open; the microphone
      // joins when the permission prompt is answered, so an unanswered
      // prompt never blocks hearing the other side.
      socket.onopen = () => set({ phase: "live", error: state.micOk ? "" : "正在请求麦克风权限…" });
      void attachMic(socket);
      await ctx.resume();
    } catch (e) {
      callAudio.stop();
      set({ phase: "error", deviceId, error: `无法启动通话音频：${(e as Error).message}` });
    }
  },

  stop() {
    if (ws) {
      ws.onclose = null;
      ws.close();
    }
    mic?.getTracks().forEach((t) => t.stop());
    ctx?.close().catch(() => {});
    captureCtx?.close().catch(() => {});
    ws = null;
    mic = null;
    ctx = null;
    captureCtx = null;
    micSource = null;
    player = null;
    recorder = null;
    if (state.phase !== "idle") set({ phase: "idle", level: 0, error: "" });
  },

  setMuted(muted: boolean) {
    set({ muted, level: muted ? 0 : state.level });
  },
};

export function useCallAudio(): AudioSnapshot {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l);
      return () => listeners.delete(l);
    },
    () => state,
  );
}
