import clsx from "clsx";
import { Binary, Check, Copy, ImageIcon, Loader2, Mail } from "lucide-react";
import { useState } from "react";
import type { Message } from "../lib/api";
import { categoryStyle } from "../lib/categories";
import { shortTime } from "../lib/format";

export function CategoryChip({ name, className }: { name: string; className?: string }) {
  if (!name) {
    return (
      <span className={clsx("inline-flex items-center gap-1 rounded-full bg-panel-2 px-2 py-0.5 text-[11px] font-medium text-subtle ring-1 ring-line ring-inset", className)}>
        <Loader2 className="size-3 animate-spin" />
        分类中
      </span>
    );
  }
  const s = categoryStyle(name);
  return (
    <span className={clsx("inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[11px] font-medium ring-1 ring-inset", s.chip, className)}>
      <s.icon className="size-3" />
      {name}
    </span>
  );
}

export function SenderAvatar({ message, size = "md" }: { message: Pick<Message, "category" | "kind" | "binary">; size?: "md" | "lg" }) {
  const s = categoryStyle(message.category);
  const Icon = message.kind === "mms" ? ImageIcon : message.binary ? Binary : message.category ? s.icon : Mail;
  return (
    <div className={clsx("flex shrink-0 items-center justify-center rounded-2xl ring-1 ring-inset", s.chip, size === "lg" ? "size-12" : "size-10")}>
      <Icon className={size === "lg" ? "size-5" : "size-[18px]"} />
    </div>
  );
}

// preview flattens a body to one line for the list.
export function preview(m: Message): string {
  if (m.kind === "mms") return m.body ? `[彩信] ${m.body}` : "[彩信通知]";
  if (m.binary) return "[二进制数据]";
  return m.body.replace(/\s+/g, " ").trim();
}

// CodePill shows the verification code with one-tap copy.
export function CodePill({ code }: { code: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <button
      type="button"
      onClick={async (e) => {
        e.stopPropagation();
        try {
          await navigator.clipboard.writeText(code);
          setCopied(true);
          setTimeout(() => setCopied(false), 1500);
        } catch {
          /* clipboard unavailable (e.g. plain HTTP on a LAN address) */
        }
      }}
      title="复制验证码"
      className="inline-flex items-center gap-1.5 rounded-lg bg-cyan-500/12 px-2 py-0.5 font-mono text-[13px] font-semibold tracking-wider text-cyan-600 ring-1 ring-cyan-500/30 transition ring-inset hover:bg-cyan-500/20 dark:text-cyan-300"
    >
      {code}
      {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5 opacity-70" />}
    </button>
  );
}

export function MessageRow({ m, active, selected, selecting, onClick, onToggle }: { m: Message; active?: boolean; selected?: boolean; selecting?: boolean; onClick: () => void; onToggle?: () => void }) {
  return (
    <div
      role="button"
      tabIndex={0}
      onClick={onClick}
      onKeyDown={(e) => (e.key === "Enter" || e.key === " ") && (e.preventDefault(), onClick())}
      className={clsx(
        "group relative flex cursor-pointer gap-3 rounded-2xl px-3 py-3 transition outline-none focus-visible:ring-2 focus-visible:ring-accent/60",
        active ? "bg-accent/10 ring-1 ring-accent/25" : "hover:bg-panel-2",
      )}
    >
      {onToggle && (selecting || selected) ? (
        <div className="flex size-10 shrink-0 items-center justify-center">
          <input
            type="checkbox"
            checked={!!selected}
            onChange={onToggle}
            onClick={(e) => e.stopPropagation()}
            className="size-[18px] cursor-pointer accent-cyan-500"
            aria-label="选择这条短信"
          />
        </div>
      ) : (
        <div
          onClick={(e) => {
            if (onToggle) {
              e.stopPropagation();
              onToggle();
            }
          }}
        >
          <SenderAvatar message={m} />
        </div>
      )}
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          {!m.read && <span className="bg-gradient-accent size-2 shrink-0 rounded-full" aria-label="未读" />}
          <span className={clsx("truncate text-sm", m.read ? "font-medium text-fg/90" : "font-semibold")}>{m.sender || "未知号码"}</span>
          <span className="ml-auto shrink-0 text-[11px] text-subtle tabular-nums">{shortTime(m.sentAt)}</span>
        </div>
        <p className={clsx("mt-0.5 line-clamp-2 text-[13px] leading-relaxed", m.read ? "text-muted" : "text-fg/85")}>{preview(m)}</p>
        <div className="mt-1.5 flex flex-wrap items-center gap-1.5">
          <CategoryChip name={m.category} />
          {m.code && <CodePill code={m.code} />}
          {!m.complete && <span className="text-[11px] text-warn">分段不完整</span>}
        </div>
      </div>
    </div>
  );
}
