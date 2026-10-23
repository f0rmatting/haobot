import type { ModemStatus } from "./api";

const pad = (n: number) => String(n).padStart(2, "0");

export function dateTime(iso: string | null | undefined): string {
  if (!iso) return "—";
  const d = new Date(iso);
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

// Short, list-friendly time: "14:05", "昨天", "10月3日", "2025/10/3".
export function shortTime(iso: string): string {
  const d = new Date(iso);
  const now = new Date();
  const startOfToday = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
  const t = d.getTime();
  if (t >= startOfToday) return `${pad(d.getHours())}:${pad(d.getMinutes())}`;
  if (t >= startOfToday - 86400000) return "昨天";
  if (d.getFullYear() === now.getFullYear()) return `${d.getMonth() + 1}月${d.getDate()}日`;
  return `${d.getFullYear()}/${d.getMonth() + 1}/${d.getDate()}`;
}

export function ago(iso: string | null | undefined): string {
  if (!iso) return "从未";
  const s = Math.max(0, Math.round((Date.now() - new Date(iso).getTime()) / 1000));
  if (s < 10) return "刚刚";
  if (s < 60) return `${s} 秒前`;
  if (s < 3600) return `${Math.floor(s / 60)} 分钟前`;
  if (s < 86400) return `${Math.floor(s / 3600)} 小时前`;
  return `${Math.floor(s / 86400)} 天前`;
}

export type Tone = "ok" | "warn" | "bad" | "muted" | "info";

// Signal strength in 0..4 bars plus a human label, preferring LTE RSRP.
export function signal(m: ModemStatus): { bars: number; label: string; tone: Tone; dbm: number | null } {
  if (m.cell?.valid && m.cell.rsrp < 0) {
    const r = m.cell.rsrp;
    const bars = r >= -85 ? 4 : r >= -95 ? 3 : r >= -105 ? 2 : r >= -115 ? 1 : 0;
    return { bars, label: ["极差", "较差", "一般", "良好", "极好"][bars], tone: bars >= 3 ? "ok" : bars === 2 ? "warn" : "bad", dbm: r };
  }
  if (m.csq === 99 || m.csq < 0) return { bars: 0, label: "无信号", tone: "muted", dbm: null };
  const bars = m.csq >= 20 ? 4 : m.csq >= 15 ? 3 : m.csq >= 10 ? 2 : m.csq >= 5 ? 1 : 0;
  return { bars, label: ["极差", "较差", "一般", "良好", "极好"][bars], tone: bars >= 3 ? "ok" : bars === 2 ? "warn" : "bad", dbm: m.signalDbm };
}

export function simLabel(sim: string): { label: string; tone: Tone } {
  switch (sim) {
    case "READY":
      return { label: "正常", tone: "ok" };
    case "SIM PIN":
      return { label: "需要 PIN 解锁", tone: "warn" };
    case "SIM PUK":
      return { label: "已锁定，需要 PUK", tone: "bad" };
    case "NOT INSERTED":
      return { label: "未插卡", tone: "bad" };
    case "":
      return { label: "未知", tone: "muted" };
    default:
      return { label: sim, tone: "warn" };
  }
}

export function radioLabel(fun: number): string {
  switch (fun) {
    case 1:
      return "正常工作";
    case 4:
      return "飞行模式";
    case 0:
      return "最小功能（射频与 SIM 关闭）";
    case -1:
      return "未知";
    default:
      return `模式 ${fun}`;
  }
}

// Network generation from the access technology reported by the modem.
export function generation(tech: string): string {
  const t = tech.toUpperCase();
  if (t.includes("LTE") || t.includes("TDD") || t.includes("FDD")) return "4G";
  if (t.includes("WCDMA") || t.includes("HSPA") || t.includes("UMTS") || t.includes("TD-SCDMA")) return "3G";
  if (t.includes("GSM") || t.includes("EDGE") || t.includes("GPRS")) return "2G";
  if (t.includes("NR")) return "5G";
  return tech || "—";
}

export function bytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}
