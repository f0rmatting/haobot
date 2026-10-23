import clsx from "clsx";
import { useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Bot, Grid3x3, Hand, Headphones, Loader2, Mic, MicOff, Phone, PhoneIncoming, PhoneOff, Volume2 } from "lucide-react";
import { api, type AssistantLive, type DeviceStatus, type LiveCall } from "../lib/api";
import { useStatus } from "../lib/hooks";
import { callAudio, useCallAudio } from "../lib/callAudio";
import { startRingtone, stopRingtone } from "../lib/ringtone";
import { knownDevices } from "../lib/devices";
import { Button, IconButton, useToast } from "./ui";
import { Popover } from "./Popover";

const stateLabel: Record<LiveCall["state"], string> = {
  dialing: "正在呼叫…",
  alerting: "对方响铃中…",
  active: "通话中",
  held: "已保持",
  incoming: "来电",
  waiting: "来电等待",
};

type CallActionName = "answer" | "hangup" | "dial" | "dtmf" | "assistant" | "assistant/skip" | "takeover";

export async function callAction(deviceId: string, action: CallActionName, body: unknown = {}) {
  return api.post(`/api/devices/${encodeURIComponent(deviceId)}/call/${action}`, body);
}

function useTicker(on: boolean) {
  const [, setNow] = useState(0);
  useEffect(() => {
    if (!on) return;
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, [on]);
}

function duration(from: string | null): string {
  if (!from) return "00:00";
  const s = Math.max(0, Math.floor((Date.now() - new Date(from).getTime()) / 1000));
  const mm = String(Math.floor(s / 60)).padStart(2, "0");
  const ss = String(s % 60).padStart(2, "0");
  return s >= 3600 ? `${Math.floor(s / 3600)}:${mm.slice(-2)}:${ss}` : `${mm}:${ss}`;
}

// CallCenter shows the incoming-call dialog and the in-call bar on every page.
export function CallCenter() {
  const { data: status } = useStatus();
  const devices = knownDevices(status);
  const live = devices.flatMap((d) => d.calls.map((c) => ({ d, c })));
  const ringing = live.find(({ c }) => c.state === "incoming" || c.state === "waiting");
  const current = live.find(({ c }) => c.state !== "incoming" && c.state !== "waiting");
  const audio = useCallAudio();

  useEffect(() => {
    if (ringing && !current) startRingtone();
    else stopRingtone();
    return () => stopRingtone();
  }, [ringing?.c.recordId, current?.c.recordId]); // eslint-disable-line react-hooks/exhaustive-deps

  // Tab title flashes while a call rings, in case the window is hidden.
  useEffect(() => {
    if (!ringing) return;
    const base = document.title;
    let on = false;
    const id = setInterval(() => {
      on = !on;
      document.title = on ? `📞 来电 ${ringing.c.number || ""}` : base;
    }, 1000);
    return () => {
      clearInterval(id);
      document.title = base;
    };
  }, [ringing?.c.recordId]); // eslint-disable-line react-hooks/exhaustive-deps

  // Audio follows the call: stop when the call that used it is gone.
  useEffect(() => {
    if (audio.phase !== "idle" && !live.some(({ d }) => d.id === audio.deviceId)) callAudio.stop();
  }, [live.length, audio.phase, audio.deviceId]); // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <>
      {ringing && <IncomingCall d={ringing.d} c={ringing.c} />}
      {current && <CallBar d={current.d} c={current.c} />}
    </>
  );
}

function IncomingCall({ d, c }: { d: DeviceStatus; c: LiveCall }) {
  const toast = useToast();
  const qc = useQueryClient();
  const [busy, setBusy] = useState<CallActionName | null>(null);
  const due = d.assistant?.answerAt;
  useTicker(!!due);
  // The agent gives up well within this (8 s for the model, 10 s for the
  // modem); past it the call is the owner's, whatever a stale status says.
  const answerAt = due && Date.now() - new Date(due).getTime() < assistantGiveUpMs ? due : undefined;
  const left = answerAt ? Math.max(0, Math.ceil((new Date(answerAt).getTime() - Date.now()) / 1000)) : 0;
  const act = async (action: CallActionName) => {
    setBusy(action);
    try {
      if (action === "answer") {
        // Start audio inside the click: browsers require a user gesture.
        void callAudio.start(d.id);
      }
      await callAction(d.id, action);
      qc.invalidateQueries({ queryKey: ["status"] });
    } catch (e) {
      toast.error((e as Error).message);
      if (action === "answer") callAudio.stop();
    } finally {
      setBusy(null);
    }
  };
  return (
    <div className="fixed inset-x-0 top-4 z-[65] flex justify-center px-4" role="alertdialog" aria-label="来电">
      <div className="animate-in w-full max-w-sm rounded-3xl bg-panel-solid p-5 shadow-2xl ring-1 ring-ok/40">
        <div className="flex items-center gap-4">
          <div className="relative flex size-14 shrink-0 items-center justify-center rounded-2xl bg-ok/15 text-ok">
            <span className="absolute inset-0 animate-ping rounded-2xl bg-ok/20" />
            <PhoneIncoming className="relative size-7" />
          </div>
          <div className="min-w-0">
            <p className="text-xs text-muted">来电 · {d.name}</p>
            <p className="truncate text-xl font-semibold tracking-tight tabular-nums">{c.number || "未知号码"}</p>
          </div>
        </div>
        <div className="mt-5 grid grid-cols-2 gap-3">
          <Button variant="hangup" size="lg" icon={PhoneOff} loading={busy === "hangup"} onClick={() => act("hangup")}>
            拒接
          </Button>
          <Button variant="success" size="lg" icon={Phone} loading={busy === "answer"} onClick={() => act("answer")}>
            接听
          </Button>
        </div>
        {answerAt ? (
          <div className="mt-3 flex items-center justify-between gap-2 rounded-2xl bg-accent/10 px-3 py-2 text-xs text-accent">
            <span className="flex items-center gap-1.5">
              <Bot className="size-4" />
              {left > 0 ? `${left} 秒后 AI 助手接听` : "AI 助手正在接听…"}
            </span>
            <span className="flex gap-1">
              <Button size="sm" variant="ghost" loading={busy === "assistant/skip"} onClick={() => act("assistant/skip")}>
                不用 AI
              </Button>
              <Button size="sm" loading={busy === "assistant"} onClick={() => act("assistant")}>
                现在接
              </Button>
            </span>
          </div>
        ) : (
          <div className="mt-3 flex items-center justify-between gap-2">
            <p className="text-[11px] text-subtle">接听后用这台电脑的麦克风和扬声器通话，建议戴耳机</p>
            <Button size="sm" variant="ghost" icon={Bot} loading={busy === "assistant"} onClick={() => act("assistant")}>
              交给 AI
            </Button>
          </div>
        )}
      </div>
    </div>
  );
}

const assistantGiveUpMs = 20_000;

const keys = ["1", "2", "3", "4", "5", "6", "7", "8", "9", "*", "0", "#"];

function CallBar({ d, c }: { d: DeviceStatus; c: LiveCall }) {
  const toast = useToast();
  const qc = useQueryClient();
  const audio = useCallAudio();
  const [hanging, setHanging] = useState(false);
  const [pad, setPad] = useState(false);
  const padAnchor = useRef<HTMLButtonElement>(null);
  useTicker(true);
  const mine = audio.deviceId === d.id && audio.phase !== "idle";
  const ai = d.assistant?.active ? d.assistant : null;
  const [taking, setTaking] = useState(false);

  const takeOver = async () => {
    setTaking(true);
    // Audio starts inside the click so the browser lets it play.
    if (!mine) void callAudio.start(d.id);
    try {
      await callAction(d.id, "takeover");
      qc.invalidateQueries({ queryKey: ["status"] });
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setTaking(false);
    }
  };

  const hangup = async () => {
    setHanging(true);
    try {
      await callAction(d.id, "hangup");
      qc.invalidateQueries({ queryKey: ["status"] });
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setHanging(false);
      callAudio.stop();
    }
  };
  const dtmf = async (key: string) => {
    try {
      await callAction(d.id, "dtmf", { key });
    } catch (e) {
      toast.error((e as Error).message);
    }
  };

  const audioNote = ai
    ? mine
      ? "正在旁听，对方听不到你"
      : "可以旁听或接过通话"
    : !mine
    ? "声音未接到这个页面"
    : audio.phase === "connecting"
      ? "正在连接声音…"
      : audio.phase === "error"
        ? audio.error
        : d.audio.error
          ? d.audio.error
          : !d.audio.active
            ? "等待模组的声音…"
            : audio.rx === 0
              ? "已连接，还没有收到对方的声音"
              : audio.micOk
              ? audio.muted
                ? "已静音"
                : "声音已连接"
              : audio.error;

  return (
    <div className="fixed inset-x-0 bottom-20 z-[60] flex justify-center px-4 lg:bottom-6">
      <div className="animate-in flex w-full max-w-xl flex-wrap items-center gap-3 rounded-3xl bg-panel-solid px-4 py-3 shadow-2xl ring-1 ring-accent/40">
        {ai && <AssistantTranscript ai={ai} />}
        <div className={clsx("flex size-10 shrink-0 items-center justify-center rounded-2xl", ai ? "bg-accent/15 text-accent" : "bg-ok/15 text-ok")}>
          {ai ? <Bot className="size-5" /> : c.state === "active" ? <Volume2 className="size-5" /> : <Loader2 className="size-5 animate-spin" />}
        </div>
        <div className="min-w-0 flex-1">
          <p className="truncate text-sm font-semibold tabular-nums">
            {c.number || "未知号码"} <span className="font-normal text-muted">· {d.name}</span>
          </p>
          <p className="truncate text-xs text-muted">
            {ai && <span className="font-medium text-accent">{assistantPhase[ai.phase] || "AI 接听中"} · </span>}
            <span className={clsx("font-medium tabular-nums", c.state === "active" ? "text-ok" : "text-fg")}>{c.state === "active" ? duration(c.answeredAt) : stateLabel[c.state]}</span>
            <span className="text-subtle"> · {audioNote}</span>
          </p>
          {mine && audio.micSilent && (
            <p className="truncate text-[11px] font-medium text-warn" title="macOS：系统设置 → 隐私与安全性 → 麦克风，打开浏览器；系统设置 → 声音 → 输入，确认选对麦克风">
              没有检测到麦克风声音：请检查系统的麦克风权限和输入设备，或在“电话”页换一个麦克风
            </p>
          )}
          {d.audio.active && (
            <p className={clsx("truncate font-mono text-[10px]", d.audio.rxPerSecond < 12800 || d.audio.maxGapMs > 200 ? "text-warn" : "text-subtle")} title="模组下行字节数（正常约 16000/秒）、最长中断、网页麦克风帧数 / 程序补的静音帧数">
              下行 {d.audio.rxPerSecond} B/s · 最长中断 {d.audio.maxGapMs} ms · 麦克风 {d.audio.micFrames} / 静音 {d.audio.fillFrames} · 已收 {audio.rx} 帧
            </p>
          )}
          {mine && audio.micInfo && (
            <p className="truncate font-mono text-[10px] text-subtle" title="正在使用的麦克风和采样率">
              {audio.micInfo}
            </p>
          )}
        </div>
        {mine && audio.phase === "live" && audio.micOk && (
          <div className="hidden h-6 w-1.5 flex-col justify-end overflow-hidden rounded-full bg-line-strong sm:flex" title="麦克风音量">
            <div className="w-full rounded-full bg-ok transition-all" style={{ height: `${Math.round(audio.level * 100)}%` }} />
          </div>
        )}
        <div className="flex items-center gap-1">
          {!mine && (
            <Button size="sm" icon={Headphones} onClick={() => callAudio.start(d.id)}>
              {ai ? "旁听" : "接入声音"}
            </Button>
          )}
          {ai && (
            <Button size="sm" variant="success" icon={Hand} loading={taking} onClick={takeOver}>
              我来接
            </Button>
          )}
          {!ai && mine && audio.micOk && <IconButton icon={audio.muted ? MicOff : Mic} label={audio.muted ? "取消静音" : "静音"} onClick={() => callAudio.setMuted(!audio.muted)} className={clsx(audio.muted && "bg-warn/15 text-warn")} />}
          {c.state === "active" && <IconButton ref={padAnchor} icon={Grid3x3} label="拨号盘" onClick={() => setPad(!pad)} />}
          <Button variant="hangup" size="sm" icon={PhoneOff} loading={hanging} onClick={hangup}>
            挂断
          </Button>
        </div>
      </div>
      <Popover anchor={padAnchor} open={pad} onClose={() => setPad(false)} width={220} align="end">
        <div className="grid grid-cols-3 gap-2 p-1">
          {keys.map((k) => (
            <button key={k} type="button" onClick={() => dtmf(k)} className="h-11 rounded-xl bg-panel-2 text-lg font-semibold ring-1 ring-line transition hover:bg-accent/10 active:scale-95">
              {k}
            </button>
          ))}
        </div>
      </Popover>
    </div>
  );
}

const assistantPhase: Record<AssistantLive["phase"], string> = {
  "": "",
  connecting: "AI 助手连接中",
  talking: "AI 助手通话中",
  ending: "AI 助手正在道别",
};

// AssistantTranscript shows the live conversation above the call bar.
function AssistantTranscript({ ai }: { ai: AssistantLive }) {
  const box = useRef<HTMLDivElement>(null);
  useEffect(() => {
    box.current?.scrollTo({ top: box.current.scrollHeight });
  }, [ai.lines.length, ai.lines[ai.lines.length - 1]?.text]);
  return (
    <div className="w-full">
      {ai.error && <p className="mb-2 rounded-xl bg-bad/10 px-3 py-1.5 text-xs text-bad">{ai.error}</p>}
      <div ref={box} className="max-h-44 space-y-1.5 overflow-y-auto rounded-2xl bg-panel-2 p-3 text-sm ring-1 ring-line">
        {ai.lines.length === 0 ? (
          <p className="text-xs text-subtle">{ai.phase === "connecting" ? "正在连接 AI 模型…" : "等待对方说话…"}</p>
        ) : (
          ai.lines.map((l, i) => (
            <p key={i} className={clsx("leading-relaxed", !l.final && "opacity-60")}>
              <span className={clsx("mr-1.5 text-xs font-semibold", l.role === "caller" ? "text-ok" : "text-accent")}>{l.role === "caller" ? "对方" : "AI"}</span>
              {l.text}
            </p>
          ))
        )}
      </div>
    </div>
  );
}
