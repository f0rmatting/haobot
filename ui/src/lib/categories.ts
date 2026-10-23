import {
  BadgeDollarSign,
  Bell,
  KeyRound,
  Megaphone,
  MessageCircle,
  PhoneOff,
  RadioTower,
  Shapes,
  Truck,
  type LucideIcon,
} from "lucide-react";

export interface CategoryStyle {
  icon: LucideIcon;
  // Tailwind classes for the chip/avatar; listed in full so the build keeps them.
  chip: string;
  dot: string;
  hex: string;
}

const styles: Record<string, CategoryStyle> = {
  验证码: { icon: KeyRound, chip: "bg-cyan-500/12 text-cyan-600 dark:text-cyan-300 ring-cyan-500/25", dot: "bg-cyan-400", hex: "#22d3ee" },
  账单交易: { icon: BadgeDollarSign, chip: "bg-emerald-500/12 text-emerald-600 dark:text-emerald-300 ring-emerald-500/25", dot: "bg-emerald-400", hex: "#34d399" },
  快递物流: { icon: Truck, chip: "bg-amber-500/12 text-amber-600 dark:text-amber-300 ring-amber-500/25", dot: "bg-amber-400", hex: "#fbbf24" },
  营销推广: { icon: Megaphone, chip: "bg-fuchsia-500/12 text-fuchsia-600 dark:text-fuchsia-300 ring-fuchsia-500/25", dot: "bg-fuchsia-400", hex: "#e879f9" },
  推销骚扰: { icon: PhoneOff, chip: "bg-rose-500/12 text-rose-600 dark:text-rose-300 ring-rose-500/25", dot: "bg-rose-400", hex: "#fb7185" },
  运营商: { icon: RadioTower, chip: "bg-sky-500/12 text-sky-600 dark:text-sky-300 ring-sky-500/25", dot: "bg-sky-400", hex: "#38bdf8" },
  通知提醒: { icon: Bell, chip: "bg-indigo-500/12 text-indigo-600 dark:text-indigo-300 ring-indigo-500/25", dot: "bg-indigo-400", hex: "#818cf8" },
  个人消息: { icon: MessageCircle, chip: "bg-violet-500/12 text-violet-600 dark:text-violet-300 ring-violet-500/25", dot: "bg-violet-400", hex: "#a78bfa" },
};

// Custom categories cycle through a neutral palette.
const extra: CategoryStyle[] = [
  { icon: Shapes, chip: "bg-teal-500/12 text-teal-600 dark:text-teal-300 ring-teal-500/25", dot: "bg-teal-400", hex: "#2dd4bf" },
  { icon: Shapes, chip: "bg-lime-500/12 text-lime-700 dark:text-lime-300 ring-lime-500/25", dot: "bg-lime-400", hex: "#a3e635" },
  { icon: Shapes, chip: "bg-orange-500/12 text-orange-600 dark:text-orange-300 ring-orange-500/25", dot: "bg-orange-400", hex: "#fb923c" },
];

const fallback: CategoryStyle = {
  icon: Shapes,
  chip: "bg-slate-500/12 text-slate-600 dark:text-slate-300 ring-slate-500/25",
  dot: "bg-slate-400",
  hex: "#94a3b8",
};

export function categoryStyle(name: string): CategoryStyle {
  if (!name || name === "其他") return fallback;
  if (styles[name]) return styles[name];
  let h = 0;
  for (const ch of name) h = (h * 31 + ch.charCodeAt(0)) >>> 0;
  return extra[h % extra.length];
}

export const sourceLabel: Record<string, string> = {
  rule: "固定规则",
  llm: "大模型",
  manual: "手动",
  "": "待分类",
};
