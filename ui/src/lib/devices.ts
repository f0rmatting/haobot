import type { DeviceStatus, Status } from "./api";
import type { Tone } from "./format";

// deviceHealth summarises one device for dots and badges.
export function deviceHealth(d: DeviceStatus): { tone: Tone; label: string } {
  if (!d.online) return { tone: "muted", label: "离线" };
  if (!d.connected) return { tone: "bad", label: "未连接" };
  if (!d.enabled) return { tone: "muted", label: "已停用" };
  if (d.modem.sim === "SIM PIN") return { tone: "warn", label: "需要 PIN" };
  if (d.modem.sim === "SIM PUK") return { tone: "bad", label: "SIM 已锁" };
  if (d.modem.sim === "NOT INSERTED") return { tone: "bad", label: "未插卡" };
  if (d.modem.functionality === 4) return { tone: "warn", label: "飞行模式" };
  if (d.health.level === "bad") return { tone: "bad", label: d.health.suspended ? "需要人工处理" : d.health.pingFailures >= 2 ? "疑似卡死" : "自检未通过" };
  if (!d.smsReady) return { tone: "warn", label: "准备中" };
  if (!d.modem.registered) return { tone: "warn", label: "未注册网络" };
  if (d.health.level === "warn") return { tone: "warn", label: "需要留意" };
  return { tone: "ok", label: "正常" };
}

// knownDevices are devices with an id (the port has answered).
export function knownDevices(status: Status | undefined): DeviceStatus[] {
  return (status?.devices ?? []).filter((d) => d.id);
}

export function deviceName(status: Status | undefined, id: string): string {
  return status?.devices.find((d) => d.id === id)?.name ?? (id ? `设备 ${id.slice(-4)}` : "未知设备");
}

// canSend tells whether a device can send right now, and why not.
export function sendState(d: DeviceStatus | undefined): { ok: boolean; reason: string } {
  if (!d) return { ok: false, reason: "设备不存在" };
  if (!d.online || !d.connected) return { ok: false, reason: "设备未连接" };
  if (!d.enabled) return { ok: false, reason: "设备已停用" };
  if (d.modem.sim !== "READY" || !d.smsReady) return { ok: false, reason: "SIM 卡未就绪" };
  return { ok: true, reason: "" };
}

// DeviceColor tells devices apart at a glance; classes are listed in full so
// the build keeps them.
export interface DeviceColor {
  avatar: string; // background and text of the avatar
  ring: string; // ring around a selected item
  text: string;
}

const palette: DeviceColor[] = [
  { avatar: "bg-cyan-500/15 text-cyan-600 dark:text-cyan-300", ring: "ring-cyan-500/50", text: "text-cyan-600 dark:text-cyan-300" },
  { avatar: "bg-violet-500/15 text-violet-600 dark:text-violet-300", ring: "ring-violet-500/50", text: "text-violet-600 dark:text-violet-300" },
  { avatar: "bg-amber-500/15 text-amber-600 dark:text-amber-300", ring: "ring-amber-500/50", text: "text-amber-600 dark:text-amber-300" },
  { avatar: "bg-emerald-500/15 text-emerald-600 dark:text-emerald-300", ring: "ring-emerald-500/50", text: "text-emerald-600 dark:text-emerald-300" },
  { avatar: "bg-rose-500/15 text-rose-600 dark:text-rose-300", ring: "ring-rose-500/50", text: "text-rose-600 dark:text-rose-300" },
  { avatar: "bg-sky-500/15 text-sky-600 dark:text-sky-300", ring: "ring-sky-500/50", text: "text-sky-600 dark:text-sky-300" },
];

// deviceColor picks a stable colour from the device id (the IMEI).
export function deviceColor(id: string): DeviceColor {
  let h = 0;
  for (const ch of id) h = (h * 31 + ch.charCodeAt(0)) >>> 0;
  return palette[h % palette.length];
}

// deviceTag is a short label for the avatar: the last two digits of the
// IMEI, which stay the same however the device is renamed.
export function deviceTag(d: DeviceStatus): string {
  const digits = (d.imei || d.id).replace(/\D/g, "");
  return digits.slice(-2) || "?";
}

// deviceSubtitle identifies a device by number, or by IMEI when the SIM
// does not report its number.
export function deviceSubtitle(d: DeviceStatus): string {
  const imei = d.imei || d.id;
  return d.identity.number || (imei ? `IMEI ··${imei.slice(-4)}` : "");
}
