import clsx from "clsx";
import type { Tone } from "../lib/format";

const barTone: Record<Tone, string> = { ok: "bg-ok", warn: "bg-warn", bad: "bg-bad", info: "bg-info", muted: "bg-subtle" };

// SignalBars draws 4 bars, `bars` of them lit.
export function SignalBars({ bars, tone, className }: { bars: number; tone: Tone; className?: string }) {
  return (
    <div className={clsx("flex items-end gap-[3px]", className)} aria-label={`信号 ${bars}/4 格`}>
      {[1, 2, 3, 4].map((i) => (
        <span key={i} className={clsx("w-[5px] rounded-sm transition-all", i <= bars ? barTone[tone] : "bg-line-strong")} style={{ height: `${i * 25}%` }} />
      ))}
    </div>
  );
}

// Gauge is a semicircular meter for a value in [min, max].
export function Gauge({ value, min, max, label, unit, tone }: { value: number | null; min: number; max: number; label: string; unit: string; tone: Tone }) {
  const ratio = value === null ? 0 : Math.min(1, Math.max(0, (value - min) / (max - min)));
  const r = 52;
  const len = Math.PI * r;
  const color = { ok: "var(--ok)", warn: "var(--warn)", bad: "var(--bad)", info: "var(--info)", muted: "var(--subtle)" }[tone];
  return (
    <div className="relative mx-auto w-full max-w-[220px]">
      <svg viewBox="0 0 128 72" className="w-full overflow-visible">
        <defs>
          <linearGradient id="gauge-track" x1="0" x2="1">
            <stop offset="0" stopColor="var(--accent)" />
            <stop offset="1" stopColor="var(--accent-2)" />
          </linearGradient>
        </defs>
        <path d="M12 64 A52 52 0 0 1 116 64" fill="none" stroke="var(--line-strong)" strokeWidth="9" strokeLinecap="round" />
        <path
          d="M12 64 A52 52 0 0 1 116 64"
          fill="none"
          stroke={tone === "muted" ? color : "url(#gauge-track)"}
          strokeWidth="9"
          strokeLinecap="round"
          strokeDasharray={`${len * ratio} ${len}`}
          className="transition-all duration-700"
        />
      </svg>
      <div className="absolute inset-x-0 bottom-0 text-center">
        <div className="text-3xl font-semibold tracking-tight tabular-nums">
          {value === null ? "—" : value}
          <span className="ml-1 text-sm font-normal text-muted">{value === null ? "" : unit}</span>
        </div>
        <div className="text-xs font-medium" style={{ color }}>
          {label}
        </div>
      </div>
    </div>
  );
}

// Meter is a thin horizontal progress bar.
export function Meter({ value, max, warnAt = 0.8 }: { value: number; max: number; warnAt?: number }) {
  const ratio = max > 0 ? Math.min(1, value / max) : 0;
  return (
    <div className="h-2 w-full overflow-hidden rounded-full bg-line-strong/60">
      <div className={clsx("h-full rounded-full transition-all duration-700", ratio >= warnAt ? "bg-warn" : "bg-gradient-accent")} style={{ width: `${ratio * 100}%` }} />
    </div>
  );
}

// Donut draws category shares.
export function Donut({ data, size = 132 }: { data: { label: string; value: number; color: string }[]; size?: number }) {
  const total = data.reduce((s, d) => s + d.value, 0);
  const r = 46;
  const c = 2 * Math.PI * r;
  let offset = 0;
  return (
    <svg viewBox="0 0 120 120" width={size} height={size} className="shrink-0 -rotate-90">
      <circle cx="60" cy="60" r={r} fill="none" stroke="var(--line-strong)" strokeWidth="14" />
      {total > 0 &&
        data.map((d) => {
          const len = (d.value / total) * c;
          const el = (
            <circle key={d.label} cx="60" cy="60" r={r} fill="none" stroke={d.color} strokeWidth="14" strokeDasharray={`${Math.max(0, len - 1.5)} ${c}`} strokeDashoffset={-offset}>
              <title>{`${d.label}：${d.value}`}</title>
            </circle>
          );
          offset += len;
          return el;
        })}
    </svg>
  );
}
