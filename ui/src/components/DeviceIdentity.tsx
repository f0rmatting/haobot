import clsx from "clsx";
import type { DeviceStatus } from "../lib/api";
import { deviceColor, deviceHealth, deviceSubtitle, deviceTag } from "../lib/devices";
import { Dot } from "./ui";

// DeviceAvatar is a device's coloured mark, with its health as a dot.
export function DeviceAvatar({ d, size = "md" }: { d: DeviceStatus; size?: "sm" | "md" | "lg" }) {
  const c = deviceColor(d.id);
  const health = deviceHealth(d);
  return (
    <span
      className={clsx(
        "relative flex shrink-0 items-center justify-center rounded-xl font-mono font-semibold",
        c.avatar,
        size === "sm" && "size-7 text-[11px]",
        size === "md" && "size-9 text-[13px]",
        size === "lg" && "size-12 text-base",
      )}
      aria-hidden
    >
      {deviceTag(d)}
      <span className="absolute -top-1 -right-1 flex rounded-full bg-panel-solid p-[2px]">
        <Dot tone={health.tone} />
      </span>
    </span>
  );
}

// DeviceIdentity names a device everywhere a setting belongs to one: avatar,
// name, health, and its number or IMEI.
export function DeviceIdentity({ d, size = "md", extra }: { d: DeviceStatus; size?: "sm" | "md" | "lg"; extra?: string }) {
  const health = deviceHealth(d);
  return (
    <div className="flex min-w-0 items-center gap-3">
      <DeviceAvatar d={d} size={size} />
      <div className="min-w-0">
        <div className="flex items-center gap-2">
          <span className={clsx("truncate font-semibold", size === "lg" ? "text-lg tracking-tight" : "text-sm")}>{d.name}</span>
          <span className={clsx("shrink-0 text-[11px]", { ok: "text-ok", warn: "text-warn", bad: "text-bad", info: "text-info", muted: "text-subtle" }[health.tone])}>{health.label}</span>
        </div>
        <p className="truncate font-mono text-[11px] text-muted">{[deviceSubtitle(d), extra].filter(Boolean).join(" · ")}</p>
      </div>
    </div>
  );
}

// DeviceMark is a device's colour and number as an inline tag, for lines of
// text such as the log.
export function DeviceMark({ d, name }: { d?: DeviceStatus; name: string }) {
  const c = d ? deviceColor(d.id) : null;
  return (
    <span className={clsx("inline-flex h-[18px] items-center gap-1 rounded-md px-1.5 align-[1px] text-[11px] leading-none font-medium whitespace-nowrap", c ? c.avatar : "bg-panel-2 text-muted ring-1 ring-line")}>
      {d && <span className="font-mono font-semibold">{deviceTag(d)}</span>}
      <span className={clsx(c && "text-fg/80")}>{name}</span>
    </span>
  );
}
