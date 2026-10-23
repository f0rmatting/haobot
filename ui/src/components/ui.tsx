import clsx from "clsx";
import { Check, ChevronDown, Copy, Loader2, X, type LucideIcon } from "lucide-react";
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useId,
  useRef,
  useState,
  type ButtonHTMLAttributes,
  type InputHTMLAttributes,
  type Ref,
  type SelectHTMLAttributes,
  type ReactNode,
  type TextareaHTMLAttributes,
} from "react";
import type { Tone } from "../lib/format";

// ---------- Button ----------

type Variant = "primary" | "secondary" | "ghost" | "danger" | "outline" | "success" | "hangup";

interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant;
  size?: "sm" | "md" | "lg";
  icon?: LucideIcon;
  loading?: boolean;
}

const variants: Record<Variant, string> = {
  primary: "bg-gradient-accent text-white shadow-lg shadow-cyan-500/20 hover:brightness-110",
  secondary: "bg-panel-2 text-fg ring-1 ring-line hover:ring-line-strong",
  outline: "bg-transparent text-fg ring-1 ring-line-strong hover:bg-panel-2",
  ghost: "bg-transparent text-muted hover:text-fg hover:bg-panel-2",
  danger: "bg-bad/12 text-bad ring-1 ring-bad/30 hover:bg-bad/20",
  success: "bg-ok text-white shadow-lg shadow-emerald-500/20 hover:brightness-110",
  hangup: "bg-bad text-white shadow-lg shadow-rose-500/20 hover:brightness-110",
};

const sizes = {
  sm: "h-8 px-3 text-xs gap-1.5 rounded-lg",
  md: "h-10 px-4 text-sm gap-2 rounded-xl",
  lg: "h-12 px-5 text-base gap-2 rounded-xl",
};

export function Button({ variant = "secondary", size = "md", icon: Icon, loading, className, children, disabled, ...rest }: ButtonProps) {
  return (
    <button
      {...rest}
      disabled={disabled || loading}
      className={clsx(
        "inline-flex select-none items-center justify-center font-medium whitespace-nowrap transition duration-150 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:opacity-50",
        variants[variant],
        sizes[size],
        className,
      )}
    >
      {loading ? <Loader2 className="size-4 animate-spin" /> : Icon ? <Icon className="size-4" /> : null}
      {children}
    </button>
  );
}

export function IconButton({ icon: Icon, label, className, ref, ...rest }: ButtonHTMLAttributes<HTMLButtonElement> & { icon: LucideIcon; label: string; ref?: Ref<HTMLButtonElement> }) {
  return (
    <button
      {...rest}
      ref={ref}
      aria-label={label}
      title={label}
      className={clsx(
        "inline-flex size-9 items-center justify-center rounded-xl text-muted transition hover:bg-panel-2 hover:text-fg focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-40",
        className,
      )}
    >
      <Icon className="size-[18px]" />
    </button>
  );
}

// ---------- Card ----------

export function Card({ className, children, glow }: { className?: string; children: ReactNode; glow?: boolean }) {
  return (
    <section className={clsx("glass relative overflow-hidden rounded-2xl", glow && "ring-1 ring-accent/20", className)}>
      {glow && <div className="bg-gradient-accent pointer-events-none absolute -top-24 -right-24 size-48 rounded-full opacity-20 blur-3xl" />}
      {children}
    </section>
  );
}

export function CardHeader({ title, subtitle, icon: Icon, action }: { title: ReactNode; subtitle?: ReactNode; icon?: LucideIcon; action?: ReactNode }) {
  return (
    <div className="flex items-start justify-between gap-3 px-5 pt-5">
      <div className="flex min-w-0 items-start gap-3">
        {Icon && (
          <div className="mt-0.5 flex size-9 shrink-0 items-center justify-center rounded-xl bg-accent/10 text-accent ring-1 ring-accent/20">
            <Icon className="size-[18px]" />
          </div>
        )}
        <div className="min-w-0">
          <h2 className="text-[15px] font-semibold tracking-tight">{title}</h2>
          {subtitle && <p className="mt-0.5 text-[13px] text-muted">{subtitle}</p>}
        </div>
      </div>
      {action && <div className="shrink-0">{action}</div>}
    </div>
  );
}

export function CardBody({ className, children }: { className?: string; children: ReactNode }) {
  return <div className={clsx("px-5 pt-4 pb-5", className)}>{children}</div>;
}

// Collapsible is a section that opens on demand, for settings and details
// that are rarely needed.
export function Collapsible({ title, subtitle, icon: Icon, badge, defaultOpen, children, className }: { title: ReactNode; subtitle?: ReactNode; icon?: LucideIcon; badge?: ReactNode; defaultOpen?: boolean; children: ReactNode; className?: string }) {
  const [open, setOpen] = useState(!!defaultOpen);
  const id = useId();
  return (
    <div className={clsx("rounded-2xl ring-1 ring-line", open && "bg-panel-2/40", className)}>
      <button type="button" aria-expanded={open} aria-controls={id} onClick={() => setOpen(!open)} className="flex w-full items-center gap-3 rounded-2xl px-4 py-3 text-left transition hover:bg-panel-2">
        {Icon && <Icon className="size-4 shrink-0 text-muted" />}
        <div className="min-w-0 flex-1">
          <div className="text-[13px] font-medium">{title}</div>
          {subtitle && <div className="truncate text-[11px] text-muted">{subtitle}</div>}
        </div>
        {badge}
        <ChevronDown className={clsx("size-4 shrink-0 text-muted transition", !open && "-rotate-90")} />
      </button>
      {open && (
        <div id={id} className="space-y-4 px-4 pt-1 pb-4">
          {children}
        </div>
      )}
    </div>
  );
}

// ---------- Badge / status ----------

const toneClass: Record<Tone, string> = {
  ok: "bg-ok/12 text-ok ring-ok/25",
  warn: "bg-warn/12 text-warn ring-warn/25",
  bad: "bg-bad/12 text-bad ring-bad/25",
  info: "bg-info/12 text-info ring-info/25",
  muted: "bg-panel-2 text-muted ring-line",
};

export function Badge({ tone = "muted", children, className, icon: Icon }: { tone?: Tone; children: ReactNode; className?: string; icon?: LucideIcon }) {
  return (
    <span className={clsx("inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset", toneClass[tone], className)}>
      {Icon && <Icon className="size-3" />}
      {children}
    </span>
  );
}

const dotTone: Record<Tone, string> = { ok: "bg-ok text-ok", warn: "bg-warn text-warn", bad: "bg-bad text-bad", info: "bg-info text-info", muted: "bg-subtle text-subtle" };

export function Dot({ tone, pulse }: { tone: Tone; pulse?: boolean }) {
  return <span className={clsx("inline-block size-2 shrink-0 rounded-full", dotTone[tone], pulse && "pulse-dot")} />;
}

// ---------- Form controls ----------

export function Field({ label, hint, error, children, htmlFor }: { label: ReactNode; hint?: ReactNode; error?: string; children: ReactNode; htmlFor?: string }) {
  return (
    <div className="space-y-1.5">
      <label htmlFor={htmlFor} className="block text-[13px] font-medium text-fg">
        {label}
      </label>
      {children}
      {error ? <p className="text-xs text-bad">{error}</p> : hint ? <p className="text-xs text-muted">{hint}</p> : null}
    </div>
  );
}

const inputClass =
  "w-full rounded-xl bg-panel-2 px-3.5 text-sm text-fg ring-1 ring-line outline-none placeholder:text-subtle transition focus:ring-2 focus:ring-accent/60 disabled:opacity-60";

export function Input({ className, ...rest }: InputHTMLAttributes<HTMLInputElement>) {
  return <input {...rest} className={clsx(inputClass, "h-10", className)} />;
}

export function Textarea({ className, ...rest }: TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return <textarea {...rest} className={clsx(inputClass, "min-h-24 py-2.5 leading-relaxed", className)} />;
}

export function Select({ className, children, ...rest }: SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select {...rest} className={clsx(inputClass, "h-10 appearance-none pr-8", className)}>
      {children}
    </select>
  );
}

export function Switch({ checked, onChange, disabled, label }: { checked: boolean; onChange: (v: boolean) => void; disabled?: boolean; label?: string }) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className={clsx(
        "relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:opacity-50",
        checked ? "bg-gradient-accent" : "bg-line-strong",
      )}
    >
      <span className={clsx("inline-block size-5 rounded-full bg-white shadow transition", checked ? "translate-x-5" : "translate-x-0.5")} />
    </button>
  );
}

export function Segmented<T extends string>({ value, onChange, options, size = "md" }: { value: T; onChange: (v: T) => void; options: { value: T; label: ReactNode; icon?: LucideIcon }[]; size?: "sm" | "md" }) {
  return (
    <div className="inline-flex max-w-full flex-wrap rounded-xl bg-panel-2 p-1 ring-1 ring-line">
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          onClick={() => onChange(o.value)}
          className={clsx(
            "inline-flex shrink-0 items-center gap-1.5 rounded-lg font-medium whitespace-nowrap transition",
            size === "sm" ? "px-2.5 py-1 text-xs" : "px-3 py-1.5 text-sm",
            value === o.value ? "bg-panel-solid text-fg shadow-sm ring-1 ring-line" : "text-muted hover:text-fg",
          )}
        >
          {o.icon && <o.icon className="size-3.5" />}
          {o.label}
        </button>
      ))}
    </div>
  );
}

// PinInput keeps only digits.
export function PinInput({ value, onChange, maxLength = 8, ...rest }: Omit<InputHTMLAttributes<HTMLInputElement>, "onChange" | "value"> & { value: string; onChange: (v: string) => void }) {
  return (
    <Input
      {...rest}
      type="password"
      inputMode="numeric"
      autoComplete="off"
      value={value}
      maxLength={maxLength}
      onChange={(e) => onChange(e.target.value.replace(/\D/g, ""))}
      className="font-mono tracking-[0.4em]"
    />
  );
}

// ---------- Misc ----------

export function Spinner({ className }: { className?: string }) {
  return <Loader2 className={clsx("size-5 animate-spin text-muted", className)} />;
}

export function Empty({ icon: Icon, title, children }: { icon: LucideIcon; title: string; children?: ReactNode }) {
  return (
    <div className="flex flex-col items-center justify-center px-6 py-14 text-center">
      <div className="mb-4 flex size-14 items-center justify-center rounded-2xl bg-panel-2 ring-1 ring-line">
        <Icon className="size-6 text-subtle" />
      </div>
      <p className="font-medium">{title}</p>
      {children && <div className="mt-1 max-w-sm text-sm text-muted">{children}</div>}
    </div>
  );
}

export function KV({ label, value, mono, hint }: { label: ReactNode; value: ReactNode; mono?: boolean; hint?: ReactNode }) {
  return (
    <div className="flex items-start justify-between gap-4 py-2.5">
      <dt className="shrink-0 text-[13px] text-muted">{label}</dt>
      <dd className={clsx("min-w-0 text-right text-[13px] break-all", mono && "font-mono")}>
        {value === "" || value === null || value === undefined ? <span className="text-subtle">—</span> : value}
        {hint && <div className="mt-0.5 text-xs text-subtle">{hint}</div>}
      </dd>
    </div>
  );
}

export function CopyButton({ text, label = "复制", className, size = "sm" }: { text: string; label?: string; className?: string; size?: "sm" | "md" }) {
  const [done, setDone] = useState(false);
  const toast = useToast();
  return (
    <Button
      size={size}
      variant="secondary"
      className={className}
      icon={done ? Check : Copy}
      onClick={async (e) => {
        e.stopPropagation();
        try {
          await navigator.clipboard.writeText(text);
          setDone(true);
          setTimeout(() => setDone(false), 1500);
        } catch {
          toast.error("复制失败，请手动选择文字复制");
        }
      }}
    >
      {done ? "已复制" : label}
    </Button>
  );
}

export function Alert({ tone, title, children, action, icon: Icon }: { tone: Tone; title: ReactNode; children?: ReactNode; action?: ReactNode; icon?: LucideIcon }) {
  return (
    <div className={clsx("flex flex-col gap-3 rounded-2xl px-4 py-3.5 ring-1 ring-inset sm:flex-row sm:items-center", toneClass[tone])}>
      <div className="flex min-w-0 flex-1 items-start gap-3">
        {Icon && <Icon className="mt-0.5 size-5 shrink-0" />}
        <div className="min-w-0">
          <p className="text-sm font-semibold">{title}</p>
          {children && <div className="mt-0.5 text-[13px] text-fg/80">{children}</div>}
        </div>
      </div>
      {action && <div className="shrink-0">{action}</div>}
    </div>
  );
}

// ---------- Modal ----------

export function Modal({ open, onClose, title, description, children, footer, tone, size = "md" }: { open: boolean; onClose: () => void; title: ReactNode; description?: ReactNode; children?: ReactNode; footer?: ReactNode; tone?: "danger"; size?: "md" | "lg" }) {
  const titleId = useId();
  const panel = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    const first = panel.current?.querySelector<HTMLElement>("input, select, textarea, button[data-autofocus]");
    first?.focus();
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);
  if (!open) return null;
  return (
    <div className="fixed inset-0 z-50 flex items-end justify-center p-0 sm:items-center sm:p-4" role="dialog" aria-modal="true" aria-labelledby={titleId}>
      <div className="absolute inset-0 bg-black/50 backdrop-blur-sm" onClick={onClose} />
      <div ref={panel} className={clsx("animate-in relative w-full rounded-t-3xl bg-panel-solid shadow-2xl ring-1 ring-line sm:rounded-3xl", size === "lg" ? "max-w-2xl" : "max-w-md")}>
        {tone === "danger" && <div className="h-1 rounded-t-3xl bg-bad" />}
        <div className="flex items-start justify-between gap-4 px-6 pt-6">
          <div>
            <h3 id={titleId} className="text-lg font-semibold tracking-tight">
              {title}
            </h3>
            {description && <div className="mt-1.5 text-sm text-muted">{description}</div>}
          </div>
          <IconButton icon={X} label="关闭" onClick={onClose} className="-mt-1 -mr-2" />
        </div>
        {children && <div className="space-y-4 px-6 pt-5">{children}</div>}
        <div className="flex flex-col-reverse gap-2 px-6 pt-6 pb-6 sm:flex-row sm:justify-end">{footer}</div>
      </div>
    </div>
  );
}

// ConfirmModal asks before a dangerous or irreversible action.
export function ConfirmModal({ open, onClose, onConfirm, title, description, confirmLabel = "确认", danger, loading }: { open: boolean; onClose: () => void; onConfirm: () => void; title: ReactNode; description?: ReactNode; confirmLabel?: string; danger?: boolean; loading?: boolean }) {
  return (
    <Modal
      open={open}
      onClose={onClose}
      title={title}
      description={description}
      tone={danger ? "danger" : undefined}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            取消
          </Button>
          <Button variant={danger ? "danger" : "primary"} onClick={onConfirm} loading={loading} data-autofocus>
            {confirmLabel}
          </Button>
        </>
      }
    />
  );
}

// ---------- Toasts ----------

interface ToastItem {
  id: number;
  tone: "ok" | "bad" | "info";
  text: string;
}

interface ToastApi {
  ok: (text: string) => void;
  error: (text: string) => void;
  info: (text: string) => void;
}

const ToastContext = createContext<ToastApi | null>(null);

export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<ToastItem[]>([]);
  const push = useCallback((tone: ToastItem["tone"], text: string) => {
    const id = Date.now() + Math.random();
    setItems((xs) => [...xs.slice(-3), { id, tone, text }]);
    setTimeout(() => setItems((xs) => xs.filter((x) => x.id !== id)), tone === "bad" ? 6000 : 3500);
  }, []);
  const apiRef = useRef<ToastApi>({ ok: (t) => push("ok", t), error: (t) => push("bad", t), info: (t) => push("info", t) });
  return (
    <ToastContext.Provider value={apiRef.current}>
      {children}
      <div className="pointer-events-none fixed inset-x-0 bottom-20 z-[60] flex flex-col items-center gap-2 px-4 lg:bottom-6" aria-live="polite">
        {items.map((t) => (
          <div
            key={t.id}
            className={clsx(
              "animate-in pointer-events-auto flex max-w-md items-center gap-2.5 rounded-2xl bg-panel-solid px-4 py-3 text-sm shadow-2xl ring-1",
              t.tone === "ok" && "ring-ok/30",
              t.tone === "bad" && "ring-bad/30",
              t.tone === "info" && "ring-line",
            )}
          >
            <Dot tone={t.tone === "bad" ? "bad" : t.tone === "ok" ? "ok" : "info"} />
            <span>{t.text}</span>
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}

export function useToast(): ToastApi {
  const ctx = useContext(ToastContext);
  if (!ctx) throw new Error("useToast outside ToastProvider");
  return ctx;
}

// ProBadge marks features only shown in professional mode.
export function ProBadge() {
  return <span className="rounded-md bg-accent-2/15 px-1.5 py-0.5 text-[10px] font-semibold tracking-wide text-accent-2 uppercase ring-1 ring-accent-2/25">Pro</span>;
}
