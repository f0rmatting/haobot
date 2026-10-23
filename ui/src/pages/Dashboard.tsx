import clsx from "clsx";
import { useState, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import {
  Activity as ActivityIcon,
  AlertTriangle,
  ArrowRight,
  BellRing,
  Bot,
  Brain,
  Check,
  ChevronDown,
  Database,
  FlaskConical,
  HeartPulse,
  Inbox,
  KeyRound,
  Loader2,
  MailOpen,
  MessageSquareText,
  MessageSquare,
  MessagesSquare,
  Phone,
  PhoneIncoming,
  PhoneMissed,
  PhoneOutgoing,
  PlaneTakeoff,
  RotateCcw,
  ShieldAlert,
  ShieldBan,
  SignalHigh,
  Sparkles,
  Terminal,
  Usb,
  type LucideIcon,
} from "lucide-react";
import { api, type Activity, type ActivityBucket, type AssistantSettings, type Callback, type CallRecord, type Conversation, type DeviceStatus, type LogEvent, type Message, type Page, type Status } from "../lib/api";
import { AssistantNote, Recording } from "./Calls";
import { useNow, useStatus } from "../lib/hooks";
import { ago, generation, shortTime, signal, type Tone } from "../lib/format";
import { categoryStyle } from "../lib/categories";
import { deviceHealth, deviceName, knownDevices } from "../lib/devices";
import { Button, Card, Empty, IconButton, Spinner, useToast } from "../components/ui";
import { Donut, Meter, SignalBars } from "../components/Visuals";
import { CategoryChip, SenderAvatar, preview } from "../components/MessageParts";
import { SimDialogs, type SimDialog } from "../components/SimDialogs";
import { activeChannels, useNotify } from "../components/NotifySettings";
import { DeviceAvatar } from "../components/DeviceIdentity";

interface Issue {
  key: string;
  tone: Tone;
  icon: typeof AlertTriangle;
  title: string;
  detail: string;
  action?: { label: string; run: () => void };
}

const toneText: Record<Tone, string> = { ok: "text-ok", warn: "text-warn", bad: "text-bad", info: "text-info", muted: "text-subtle" };
const toneBg: Record<Tone, string> = { ok: "bg-ok", warn: "bg-warn", bad: "bg-bad", info: "bg-info", muted: "bg-subtle" };

export function Dashboard() {
  const { data: status, isPending } = useStatus();
  const { data: activity } = useQuery({ queryKey: ["activity"], queryFn: () => api.get<Activity>("/api/activity"), refetchInterval: 60000 });
  const { data: callbacks } = useQuery({ queryKey: ["callbacks"], queryFn: () => api.get<{ items: Callback[] }>("/api/calls/callbacks"), refetchInterval: 60000 });
  const [dialog, setDialog] = useState<{ device: DeviceStatus; kind: SimDialog } | null>(null);
  const navigate = useNavigate();
  const toast = useToast();
  const qc = useQueryClient();
  useNow(15000);

  if (isPending || !status) {
    return (
      <div className="flex h-64 items-center justify-center">
        <Spinner />
      </div>
    );
  }

  const radioOn = async (d: DeviceStatus) => {
    try {
      await api.post(`/api/devices/${encodeURIComponent(d.id)}/radio`, { on: true });
      toast.ok("已关闭飞行模式");
      qc.invalidateQueries({ queryKey: ["status"] });
    } catch (e) {
      toast.error((e as Error).message);
    }
  };

  const issues = findIssues(status, {
    sim: (device, kind) => setDialog({ device, kind }),
    open: (d) => navigate(`/device?d=${encodeURIComponent(d.id)}`),
    settings: () => navigate("/settings#classifier"),
    radioOn,
  });

  return (
    <div className="space-y-4">
      <CommandBar status={status} issues={issues} brief={briefing(status, activity, callbacks?.items)} />
      <KpiStrip status={status} activity={activity} />
      {issues.length > 0 && <IssuesCard issues={issues} />}
      {!!callbacks?.items.length && <CallbacksCard items={callbacks.items} status={status} />}
      <div className="grid gap-4 lg:grid-cols-3">
        <div className="min-w-0 space-y-4 lg:col-span-2">
          <ActivityCard activity={activity} />
          <RecentCard status={status} />
          <CategoriesCard status={status} />
        </div>
        <div className="min-w-0 space-y-4">
          <DevicesPanel status={status} />
          <AssistantNotesCard />
          <FeedCard status={status} />
        </div>
      </div>
      {dialog && <SimDialogs deviceId={dialog.device.id} dialog={dialog.kind} onClose={() => setDialog(null)} pinRetries={dialog.device.modem.pinRetries} pukRetries={dialog.device.modem.pukRetries} />}
    </div>
  );
}

function findIssues(
  s: Status,
  h: { sim: (d: DeviceStatus, kind: SimDialog) => void; open: (d: DeviceStatus) => void; settings: () => void; radioOn: (d: DeviceStatus) => void },
): Issue[] {
  const out: Issue[] = [];
  const devices = s.devices.filter((d) => d.id && d.enabled);
  if (s.devices.filter((d) => d.online).length === 0) {
    out.push({
      key: "none",
      tone: "bad",
      icon: Usb,
      title: devices.length ? "所有模组都未连接" : "还没有找到模组",
      detail: "请检查 USB 线和模组供电。程序每隔几秒自动查找新插入的模组，无需重启。",
    });
  }
  const multi = devices.length > 1;
  for (const d of devices) {
    if (!d.online) continue;
    const who = multi ? `${d.name}：` : "";
    if (!d.connected) {
      out.push({ key: d.id + "conn", tone: "bad", icon: Usb, title: `${who}模组连接中断`, detail: d.lastError || "正在自动重连。" });
      continue;
    }
    switch (d.modem.sim) {
      case "SIM PIN":
        out.push({
          key: d.id + "pin",
          tone: "warn",
          icon: KeyRound,
          title: `${who}SIM 卡需要输入 PIN`,
          detail: d.pinBlocked ? "自动解锁已暂停（上次尝试失败或剩余次数不足），请手动输入 PIN。" : "解锁后才能收发短信。可以保存 PIN，以后开机自动解锁。",
          action: { label: "输入 PIN", run: () => h.sim(d, "pin") },
        });
        break;
      case "SIM PUK":
        out.push({ key: d.id + "puk", tone: "bad", icon: KeyRound, title: `${who}SIM 卡已被锁定`, detail: "PIN 连续输错，需要 PUK 码解锁。", action: { label: "用 PUK 解锁", run: () => h.sim(d, "puk") } });
        break;
      case "NOT INSERTED":
        out.push({ key: d.id + "nosim", tone: "bad", icon: AlertTriangle, title: `${who}未检测到 SIM 卡`, detail: "请断电后检查 SIM 卡是否插好、方向是否正确。" });
        break;
    }
    if (d.modem.functionality === 4) {
      out.push({ key: d.id + "air", tone: "warn", icon: PlaneTakeoff, title: `${who}飞行模式已开启`, detail: "射频已关闭，收不到也发不出短信。", action: { label: "关闭飞行模式", run: () => h.radioOn(d) } });
    } else if (d.modem.sim === "READY" && !d.modem.registered) {
      out.push({ key: d.id + "reg", tone: "warn", icon: SignalHigh, title: `${who}还没有注册到网络`, detail: `当前状态：${d.modem.registration || "未知"}。长时间不恢复请检查天线或重启模组。`, action: { label: "查看设备", run: () => h.open(d) } });
    }
    const health = d.health;
    if (health.suspended) {
      out.push({ key: d.id + "suspend", tone: "bad", icon: ShieldAlert, title: `${who}多次自动重启仍未恢复`, detail: "已暂停自动处理。请检查 SIM 卡是否欠费、天线和供电。", action: { label: "查看设备", run: () => h.open(d) } });
    } else if (health.pingFailures >= 2) {
      out.push({ key: d.id + "hang", tone: "bad", icon: HeartPulse, title: `${who}模组响应异常，可能卡死`, detail: health.autoRecover ? "连续 3 次不响应会自动重新连接，必要时重启模组。" : "已关闭自动恢复，需要手动重启模组。", action: { label: "查看设备", run: () => h.open(d) } });
    } else if (health.level === "bad" && health.selfTest?.state === "failed") {
      out.push({ key: d.id + "selftest", tone: "bad", icon: FlaskConical, title: `${who}收发自检未通过`, detail: health.selfTest.detail, action: { label: "查看设备", run: () => h.open(d) } });
    } else if (health.recoveries[0] && Date.now() - new Date(health.recoveries[0].at).getTime() < 3600000) {
      const r = health.recoveries[0];
      out.push({ key: d.id + "recovered", tone: "warn", icon: RotateCcw, title: `${who}1 小时内自动${r.action}过`, detail: `原因：${r.reason}${r.error ? `；失败：${r.error}` : ""}。如果频繁出现，建议检查供电和 USB 线。`, action: { label: "查看设备", run: () => h.open(d) } });
    }
    const { storageUsed: used, storageTotal: total } = d.modem;
    if (total > 0 && used / total >= 0.8) {
      out.push({ key: d.id + "store", tone: "warn", icon: Database, title: `${who}模组短信存储快满了（${used}/${total}）`, detail: "存满后将收不到新短信。建议开启“入库后从模组删除”（默认开启）。" });
    }
  }
  if (!s.classifier.ready && s.messages.pending > 0) {
    out.push({ key: "nollm", tone: "warn", icon: Brain, title: `${s.messages.pending} 条短信等待分类`, detail: "短信分类由大模型完成，请先在设置里启用并配置大模型。", action: { label: "去设置", run: h.settings } });
  } else if (s.classifier.enabled && s.classifier.lastError) {
    out.push({ key: "llm", tone: "warn", icon: Brain, title: "智能分类暂时不可用", detail: s.classifier.lastError, action: { label: "检查设置", run: h.settings } });
  }
  return out;
}

// ---------------- Panel ----------------

// Panel is the dashboard's card: a compact header with a mono tag, and the
// content below.
function Panel({ icon: Icon, title, tag, action, children, className, bodyClass }: { icon: LucideIcon; title: string; tag?: string; action?: ReactNode; children: ReactNode; className?: string; bodyClass?: string }) {
  return (
    <Card className={className}>
      <div className="flex items-center gap-2.5 border-b border-line px-4 py-3">
        <Icon className="size-4 shrink-0 text-accent" />
        <h2 className="min-w-0 truncate text-sm font-semibold tracking-tight">{title}</h2>
        {tag && <span className="font-mono text-[10px] tracking-wider text-subtle uppercase">{tag}</span>}
        <div className="ml-auto shrink-0">{action}</div>
      </div>
      <div className={clsx("p-4", bodyClass)}>{children}</div>
    </Card>
  );
}

function Led({ tone, pulse }: { tone: Tone; pulse?: boolean }) {
  return <span className={clsx("inline-block size-2 shrink-0 rounded-full shadow-[0_0_8px_currentColor]", toneBg[tone], toneText[tone], pulse && "pulse-dot")} />;
}

// ---------------- Command bar ----------------

function Clock() {
  const now = useNow(1000);
  const d = new Date(now);
  const p = (n: number) => String(n).padStart(2, "0");
  return (
    <div className="text-right">
      <div className="font-mono text-2xl font-semibold tracking-tight tabular-nums sm:text-3xl">
        {p(d.getHours())}:{p(d.getMinutes())}
        <span className="text-subtle">:{p(d.getSeconds())}</span>
      </div>
      <div className="font-mono text-[11px] text-muted">
        {d.getFullYear()}-{p(d.getMonth() + 1)}-{p(d.getDate())} · 周{"日一二三四五六"[d.getDay()]}
      </div>
    </div>
  );
}

// briefing sums up the day in one sentence.
function briefing(status: Status, activity: Activity | undefined, callbacks: Callback[] | undefined): string {
  const t = activity?.days[activity.days.length - 1];
  if (!t) return "";
  const parts: string[] = [];
  const smsExtra = [t.codes && `${t.codes} 条验证码`, t.spam && `${t.spam} 条营销骚扰`].filter(Boolean).join("、");
  parts.push(t.sms ? `今天收到 ${t.sms} 条短信${smsExtra ? `（${smsExtra}）` : ""}` : "今天还没有短信");
  if (t.calls === 0) parts.push("没有来电");
  else {
    let calls = `${t.calls} 个来电`;
    if (t.ai === t.calls) calls += t.calls > 1 ? "都由 AI 接了" : "由 AI 接了";
    else if (t.ai) calls += `，AI 接了 ${t.ai} 个`;
    if (t.missed) calls += `，${t.missed} 个未接`;
    parts.push(calls);
  }
  const waiting = callbacks?.length ?? 0;
  if (waiting) parts.push(`${waiting} 个等你回电`);
  if (status.messages.unread) parts.push(`${status.messages.unread} 条短信未读`);
  return parts.join("；") + "。";
}

function CommandBar({ status, issues, brief }: { status: Status; issues: Issue[]; brief: string }) {
  const navigate = useNavigate();
  const { data: notify } = useNotify();
  const { data: assistant } = useQuery({ queryKey: ["assistant"], queryFn: () => api.get<AssistantSettings>("/api/settings/assistant") });
  const devices = knownDevices(status).filter((d) => d.enabled);
  const online = devices.filter((d) => d.online && d.connected);
  const bad = issues.some((i) => i.tone === "bad");
  const tone: Tone = online.length === 0 || bad ? "bad" : issues.length ? "warn" : "ok";
  const headline = online.length === 0 ? "等待连接模组" : bad ? "有设备需要你处理" : issues.length ? `运行中，${issues.length} 项提醒` : "号小助正在守护";
  const lastSync = online.map((d) => d.lastSync).filter(Boolean).sort().pop() ?? null;
  const channels = activeChannels(notify);
  const modules: { label: string; on: boolean | undefined; detail: string; to: string; icon: LucideIcon }[] = [
    { label: "智能分类", on: status.classifier.ready, detail: status.classifier.ready ? "运行中" : status.classifier.enabled ? "配置不完整" : "未开启", to: "/settings#classifier", icon: Brain },
    { label: "AI 接听", on: assistant?.enabled && !assistant.problem, detail: assistant ? (assistant.enabled ? (assistant.problem ? "有问题" : assistant.ringDelaySec ? `${assistant.ringDelaySec} 秒后接听` : "立即接听") : "未开启") : "…", to: "/settings#assistant", icon: Bot },
    { label: "消息推送", on: channels > 0, detail: channels > 0 ? `${channels} 个渠道` : "未开启", to: "/settings#notify", icon: BellRing },
  ];
  return (
    <Card glow className="grid-lines relative">
      <div className="pointer-events-none absolute inset-0 bg-gradient-to-r from-panel via-panel/85 to-panel/40" />
      <div className="relative flex flex-col gap-4 p-4 sm:p-5 lg:flex-row lg:items-center">
        <div className="flex min-w-0 flex-1 items-center gap-4">
          <div className={clsx("relative flex size-12 shrink-0 items-center justify-center rounded-2xl ring-1", tone === "ok" ? "bg-ok/12 text-ok ring-ok/30" : tone === "warn" ? "bg-warn/12 text-warn ring-warn/30" : "bg-bad/12 text-bad ring-bad/30")}>
            <ShieldBan className="size-6" />
            <span className="absolute -top-0.5 -right-0.5">
              <Led tone={tone} pulse />
            </span>
          </div>
          <div className="min-w-0">
            <div className="font-mono text-[10px] tracking-[0.2em] text-subtle uppercase">System status</div>
            <h1 className="truncate text-lg font-semibold tracking-tight sm:text-xl">{tone === "ok" ? <span className="text-gradient">{headline}</span> : headline}</h1>
            <p className="truncate font-mono text-[11px] text-muted">
              {online.length}/{devices.length} 在线
              {online[0] && ` · ${online[0].modem.operator || "未注册"} ${generation(online[0].modem.accessTech)}`}
              {online.length > 0 && ` · 同步 ${ago(lastSync)}`}
            </p>
          </div>
        </div>
        <div className="flex flex-wrap gap-2">
          {modules.map((m) => (
            <button
              key={m.label}
              type="button"
              onClick={() => navigate(m.to)}
              className="flex items-center gap-2 rounded-xl bg-panel-2/80 px-3 py-2 text-left ring-1 ring-line transition hover:ring-accent/40"
            >
              <m.icon className={clsx("size-4", m.on ? "text-accent" : "text-subtle")} />
              <span>
                <span className="block text-[12px] font-medium">{m.label}</span>
                <span className={clsx("block font-mono text-[10px]", m.on ? "text-ok" : "text-subtle")}>{m.detail}</span>
              </span>
              <Led tone={m.on ? "ok" : "muted"} />
            </button>
          ))}
        </div>
        <div className="hidden lg:block lg:border-l lg:border-line lg:pl-5">
          <Clock />
        </div>
      </div>
      {brief && (
        <p className="relative flex items-start gap-2 border-t border-line px-4 py-2.5 text-[13px] text-muted sm:px-5">
          <Sparkles className="mt-0.5 size-3.5 shrink-0 text-accent" />
          {brief}
        </p>
      )}
    </Card>
  );
}

// ---------------- KPI ----------------

function Sparkline({ values, className }: { values: number[]; className?: string }) {
  const max = Math.max(1, ...values);
  const w = 100;
  const h = 28;
  const step = w / Math.max(1, values.length - 1);
  const pts = values.map((v, i) => [i * step, h - 2 - (v / max) * (h - 4)] as const);
  const line = pts.map(([x, y], i) => `${i ? "L" : "M"}${x.toFixed(1)} ${y.toFixed(1)}`).join(" ");
  return (
    <svg viewBox={`0 0 ${w} ${h}`} preserveAspectRatio="none" className={clsx("h-7 w-full overflow-visible", className)} aria-hidden>
      <path d={`${line} L${w} ${h} L0 ${h} Z`} fill="currentColor" opacity="0.12" />
      <path d={line} fill="none" stroke="currentColor" strokeWidth="1.5" vectorEffect="non-scaling-stroke" strokeLinejoin="round" />
    </svg>
  );
}

function Kpi({ icon: Icon, label, value, hint, spark, tone = "info", onClick, highlight }: { icon: LucideIcon; label: string; value: number | string; hint?: string; spark?: number[]; tone?: Tone; onClick?: () => void; highlight?: boolean }) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={!onClick}
      className={clsx("glass group relative overflow-hidden rounded-2xl px-4 pt-3 pb-2 text-left transition enabled:hover:ring-1 enabled:hover:ring-accent/40", highlight && "ring-1 ring-accent/40")}
    >
      <div className="flex items-center gap-1.5 text-[11px] text-muted">
        <Icon className={clsx("size-3.5", toneText[tone])} />
        {label}
        {onClick && <ArrowRight className="ml-auto size-3 opacity-0 transition group-hover:opacity-100" />}
      </div>
      <div className={clsx("mt-1 font-mono text-2xl font-semibold tracking-tight tabular-nums", highlight && "text-gradient")}>{value}</div>
      <div className="h-4 truncate text-[10px] text-subtle">{hint}</div>
      <div className={clsx("-mx-4 mt-0.5", toneText[tone])}>{spark ? <Sparkline values={spark} /> : <div className="h-7" />}</div>
    </button>
  );
}

// spamCategories are what /api/activity counts as spam.
const spamCategories = ["营销推广", "推销骚扰"];

function KpiStrip({ status, activity }: { status: Status; activity?: Activity }) {
  const navigate = useNavigate();
  const { data: notify } = useNotify();
  const rules = notify?.rules;
  const spamMuted = !!rules && activeChannels(notify) > 0 && (!rules.sms || spamCategories.every((c) => rules.skipCategories.includes(c)));
  const today = activity?.days[activity.days.length - 1];
  const yesterday = activity?.days[activity.days.length - 2];
  const hours = activity?.hours ?? [];
  const delta = (t?: number, y?: number) => (t === undefined || y === undefined ? undefined : y === 0 ? (t ? "昨天 0" : "与昨天持平") : `较昨天 ${t >= y ? "+" : ""}${t - y}`);
  const v = (n?: number) => (n === undefined ? "—" : n);
  return (
    <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 xl:grid-cols-6">
      <Kpi icon={MessageSquareText} label="今日短信" value={v(today?.sms)} hint={delta(today?.sms, yesterday?.sms)} spark={hours.map((h) => h.sms)} tone="info" onClick={() => navigate("/messages")} />
      <Kpi icon={MailOpen} label="未读短信" value={status.messages.unread} hint={status.messages.pending ? `${status.messages.pending} 条待分类` : "全部已分类"} tone="info" highlight={status.messages.unread > 0} onClick={() => navigate("/messages?unread=1")} />
      <Kpi icon={PhoneIncoming} label="今日来电" value={v(today?.calls)} hint={delta(today?.calls, yesterday?.calls)} spark={hours.map((h) => h.calls)} tone="ok" onClick={() => navigate("/calls")} />
      <Kpi icon={Bot} label="AI 代接" value={v(today?.ai)} hint={today?.calls ? `占今日来电 ${Math.round(((today?.ai ?? 0) / today.calls) * 100)}%` : "今日"} spark={hours.map((h) => h.ai)} tone="ok" onClick={() => navigate("/calls")} />
      <Kpi icon={PhoneMissed} label="未接来电" value={v(today?.missed)} hint={status.missedCalls ? `${status.missedCalls} 个还没看` : "今日"} spark={hours.map((h) => h.missed)} tone={status.missedCalls ? "warn" : "muted"} highlight={status.missedCalls > 0} onClick={() => navigate("/calls")} />
      <Kpi
        icon={ShieldBan}
        label="营销和骚扰"
        value={v(today?.spam)}
        hint={spamMuted ? "今日 · 没有推送给你" : "今日收到的营销、推销短信"}
        spark={hours.map((h) => h.spam)}
        tone="warn"
        onClick={() => navigate(`/messages?category=${encodeURIComponent(spamCategories.join(","))}&since=today`)}
      />
    </div>
  );
}

// ---------------- Issues ----------------

function IssuesCard({ issues }: { issues: Issue[] }) {
  const bad = issues.some((i) => i.tone === "bad");
  return (
    <Card className={clsx("ring-1", bad ? "ring-bad/30" : "ring-warn/30")}>
      <div className="flex items-center gap-2 border-b border-line px-4 py-2.5">
        <AlertTriangle className={clsx("size-4", bad ? "text-bad" : "text-warn")} />
        <span className="text-sm font-semibold">需要处理</span>
        <span className="font-mono text-[11px] text-subtle">{issues.length}</span>
      </div>
      <ul className="divide-y divide-line">
        {issues.map((i) => (
          <li key={i.key} className="flex flex-col gap-2 px-4 py-2.5 sm:flex-row sm:items-center">
            <div className="flex min-w-0 flex-1 items-start gap-2.5">
              <i.icon className={clsx("mt-0.5 size-4 shrink-0", toneText[i.tone])} />
              <div className="min-w-0">
                <p className="text-[13px] font-medium">{i.title}</p>
                <p className="text-xs text-muted">{i.detail}</p>
              </div>
            </div>
            {i.action && (
              <Button size="sm" variant={i.tone === "bad" ? "danger" : "secondary"} onClick={i.action.run} className="self-start sm:self-center">
                {i.action.label}
              </Button>
            )}
          </li>
        ))}
      </ul>
    </Card>
  );
}

// ---------------- Activity chart ----------------

function label(b: ActivityBucket, hourly: boolean): string {
  const d = new Date(b.start);
  if (hourly) return `${String(d.getHours()).padStart(2, "0")}:00`;
  return `${d.getMonth() + 1}/${d.getDate()}`;
}

function ActivityCard({ activity }: { activity?: Activity }) {
  const [range, setRange] = useState<"hours" | "days">("hours");
  const [hover, setHover] = useState<number | null>(null);
  const hourly = range === "hours";
  const data = activity?.[range] ?? [];
  const peak = Math.max(...data.map((b) => Math.max(b.sms, b.calls)), 0);
  // A round scale with a tick in the middle.
  const step = peak <= 4 ? 2 : peak <= 10 ? 5 : peak <= 20 ? 10 : Math.ceil(peak / 20) * 10;
  const max = Math.max(step * 2, Math.ceil(peak / step) * step);
  const sum = data.reduce((acc, b) => ({ sms: acc.sms + b.sms, spam: acc.spam + b.spam, calls: acc.calls + b.calls, ai: acc.ai + b.ai, missed: acc.missed + b.missed }), { sms: 0, spam: 0, calls: 0, ai: 0, missed: 0 });
  const shown = hover !== null ? data[hover] : null;
  return (
    <Panel
      icon={ActivityIcon}
      title="活动趋势"
      tag={hourly ? "24H" : "7D"}
      action={
        <div className="inline-flex rounded-lg bg-panel-2 p-0.5 ring-1 ring-line">
          {(["hours", "days"] as const).map((r) => (
            <button key={r} type="button" onClick={() => setRange(r)} className={clsx("rounded-md px-2.5 py-1 font-mono text-[11px] transition", range === r ? "bg-panel-solid text-fg shadow-sm ring-1 ring-line" : "text-muted hover:text-fg")}>
              {r === "hours" ? "24 小时" : "7 天"}
            </button>
          ))}
        </div>
      }
    >
      <div className="mb-3 flex flex-wrap items-baseline gap-x-5 gap-y-1 font-mono text-[11px]">
        <span className="flex items-center gap-1.5">
          <span className="size-2 rounded-sm bg-accent" />
          短信 <b className="text-sm text-fg">{shown ? shown.sms : sum.sms}</b>
          <span className="text-subtle">骚扰 {shown ? shown.spam : sum.spam}</span>
        </span>
        <span className="flex items-center gap-1.5">
          <span className="size-2 rounded-sm bg-accent-2" />
          来电 <b className="text-sm text-fg">{shown ? shown.calls : sum.calls}</b>
          <span className="text-subtle">
            AI {shown ? shown.ai : sum.ai} · 未接 {shown ? shown.missed : sum.missed}
          </span>
        </span>
        <span className="ml-auto text-subtle">{shown ? label(shown, hourly) + (hourly ? " 这一小时" : " 全天") : hourly ? "最近 24 小时合计" : "最近 7 天合计"}</span>
      </div>
      {!activity ? (
        <div className="flex h-40 items-center justify-center">
          <Spinner />
        </div>
      ) : (
        <div onMouseLeave={() => setHover(null)} className="pl-7">
          <div className="relative flex h-40 items-end gap-[3px] border-b border-line sm:gap-1">
            <div className="pointer-events-none absolute inset-0">
              {[max, max / 2].map((v) => (
                <div key={v} className="absolute inset-x-0 border-t border-dashed border-line" style={{ top: `${(1 - v / max) * 100}%` }}>
                  <span className="absolute -top-2 -left-7 w-6 text-right font-mono text-[10px] text-subtle tabular-nums">{v}</span>
                </div>
              ))}
              <span className="absolute -bottom-1.5 -left-7 w-6 text-right font-mono text-[10px] text-subtle">0</span>
            </div>
            {data.map((b, i) => (
              <div
                key={b.start}
                onMouseEnter={() => setHover(i)}
                onClick={() => setHover(i)}
                className={clsx("relative flex h-full min-w-0 flex-1 cursor-default items-end justify-center gap-px rounded-t-md transition", hover === i && "bg-accent/8")}
              >
                {!hourly && (b.sms > 0 || b.calls > 0) && (
                  <span className="absolute inset-x-0 text-center font-mono text-[10px] text-muted tabular-nums" style={{ bottom: `calc(${(Math.max(b.sms, b.calls) / max) * 100}% + 2px)` }}>
                    {b.sms}
                    {b.calls > 0 && <span className="text-accent-2">/{b.calls}</span>}
                  </span>
                )}
                <div className="w-1/2 max-w-3 rounded-t-sm bg-gradient-to-t from-accent/40 to-accent transition-all duration-500" style={{ height: `${(b.sms / max) * 100}%`, minHeight: b.sms ? 3 : 0 }} />
                <div className="w-1/2 max-w-3 rounded-t-sm bg-gradient-to-t from-accent-2/40 to-accent-2 transition-all duration-500" style={{ height: `${(b.calls / max) * 100}%`, minHeight: b.calls ? 3 : 0 }} />
              </div>
            ))}
          </div>
          <div className="mt-1.5 flex gap-[3px] font-mono text-[10px] text-subtle sm:gap-1">
            {data.map((b, i) => (
              <span key={b.start} className="min-w-0 flex-1 text-center">
                {!hourly || i % 6 === 0 || i === data.length - 1 ? (i === data.length - 1 ? (hourly ? "现在" : "今天") : label(b, hourly)) : ""}
              </span>
            ))}
          </div>
        </div>
      )}
    </Panel>
  );
}

// ---------------- Devices ----------------

function Metric({ label, value, tone }: { label: string; value: ReactNode; tone?: Tone }) {
  return (
    <div className="min-w-0">
      <div className="text-[10px] text-subtle">{label}</div>
      <div className={clsx("truncate font-mono text-[12px] tabular-nums", tone && toneText[tone])}>{value}</div>
    </div>
  );
}

function DevicesPanel({ status }: { status: Status }) {
  const navigate = useNavigate();
  if (status.devices.length === 0) return null;
  return (
    <Panel
      icon={SignalHigh}
      title="设备"
      tag={`${status.devices.filter((d) => d.online && d.connected).length}/${status.devices.length} online`}
      action={
        <Button size="sm" variant="ghost" onClick={() => navigate("/device")}>
          管理
        </Button>
      }
      bodyClass="space-y-2.5 p-3"
    >
      {status.devices.map((d, i) => (
        <DeviceRow key={d.id || "pending" + i} d={d} onClick={() => d.id && navigate(`/device?d=${encodeURIComponent(d.id)}`)} />
      ))}
    </Panel>
  );
}

// signalAdvice explains a weak signal in plain words, or returns "".
function signalAdvice(bars: number, sinr: number | null): string {
  const noisy = sinr !== null && sinr < 0;
  if (bars >= 3 && !noisy) return "";
  if (bars <= 1) return "信号很弱，来电和短信可能收不到。建议把天线移到窗边、远离金属和其他电器。";
  if (noisy) return "信号一般，干扰较大（SINR 低于 0），通话可能断续。可以调整天线方向或换个位置。";
  return "信号一般，可以调整天线位置让信号更稳定。";
}

function DeviceRow({ d, onClick }: { d: DeviceStatus; onClick: () => void }) {
  const health = deviceHealth(d);
  const sig = signal(d.modem);
  const live = d.online && d.connected;
  const cell = d.modem.cell;
  const weak = live ? signalAdvice(sig.bars, cell?.valid ? cell.sinr : null) : "";
  return (
    <button type="button" onClick={onClick} className={clsx("w-full rounded-xl bg-panel-2/60 p-3 text-left ring-1 ring-line transition hover:ring-accent/40", !d.enabled && "opacity-60")}>
      <div className="flex items-center gap-2">
        {d.id ? <DeviceAvatar d={d} size="sm" /> : <Loader2 className="size-3.5 animate-spin text-muted" />}
        <span className="min-w-0 truncate text-[13px] font-semibold">{d.name}</span>
        <span className={clsx("text-[11px]", toneText[health.tone])}>{health.label}</span>
        <span className="ml-auto flex items-center gap-2">
          {d.unread > 0 && <span className="bg-gradient-accent rounded-full px-1.5 font-mono text-[10px] leading-4 font-semibold text-white">{d.unread}</span>}
          <SignalBars bars={live ? sig.bars : 0} tone={live ? sig.tone : "muted"} className="h-4" />
        </span>
      </div>
      <p className="mt-0.5 truncate font-mono text-[11px] text-muted">
        {live ? `${d.modem.operator || "未注册"} · ${generation(d.modem.accessTech)}${cell?.valid && cell.band ? ` · B${cell.band}` : ""}` : d.lastSeen ? `最后在线 ${ago(d.lastSeen)}` : d.endpoint}
        {d.identity.number && ` · ${d.identity.number}`}
      </p>
      {live && (
        <>
          <div className="mt-2 grid grid-cols-4 gap-2">
            <Metric label={cell?.valid ? "RSRP" : "信号"} value={sig.dbm !== null ? `${sig.dbm}` : "—"} tone={sig.tone} />
            <Metric label="SINR" value={cell?.valid ? `${cell.sinr}` : "—"} />
            <Metric label="延迟" value={d.health.pingLatencyMs ? `${d.health.pingLatencyMs}ms` : "—"} />
            <Metric label="存储" value={d.modem.storageTotal > 0 ? `${Math.max(0, d.modem.storageUsed)}/${d.modem.storageTotal}` : "—"} />
          </div>
          {d.modem.storageTotal > 0 && (
            <div className="mt-2">
              <Meter value={Math.max(0, d.modem.storageUsed)} max={d.modem.storageTotal} />
            </div>
          )}
          {weak && (
            <p className="mt-2 flex items-start gap-1.5 text-[11px] leading-relaxed text-warn">
              <SignalHigh className="mt-0.5 size-3 shrink-0" />
              {weak}
            </p>
          )}
        </>
      )}
    </button>
  );
}

// ---------------- Categories ----------------

function CategoriesCard({ status }: { status: Status }) {
  const navigate = useNavigate();
  const entries = Object.entries(status.messages.categories).sort((a, b) => b[1] - a[1]);
  const data = entries.map(([label, value]) => ({ label, value, color: categoryStyle(label).hex }));
  const total = data.reduce((s, d) => s + d.value, 0);
  return (
    <Panel icon={Sparkles} title="分类分布" tag={status.classifier.ready ? "LLM" : "OFF"}>
      {total === 0 ? (
        <p className="py-4 text-center text-xs text-muted">{status.classifier.ready ? "收到短信后会自动分类。" : "智能分类未开启，短信暂不分类。"}</p>
      ) : (
        <div className="flex items-center gap-4">
          <div className="relative">
            <Donut data={data} size={104} />
            <div className="absolute inset-0 flex flex-col items-center justify-center">
              <span className="font-mono text-base font-semibold tabular-nums">{total}</span>
              <span className="text-[10px] text-muted">已分类</span>
            </div>
          </div>
          <ul className="min-w-0 flex-1 space-y-0.5">
            {entries.slice(0, 6).map(([name, n]) => (
              <li key={name}>
                <button type="button" onClick={() => navigate(`/messages?category=${encodeURIComponent(name)}`)} className="flex w-full items-center gap-2 rounded-md px-1.5 py-0.5 text-left text-[12px] hover:bg-panel-2">
                  <span className={`size-2 rounded-full ${categoryStyle(name).dot}`} />
                  <span className="min-w-0 flex-1 truncate">{name}</span>
                  <span className="font-mono text-muted tabular-nums">{n}</span>
                  <span className="w-9 text-right font-mono text-[10px] text-subtle tabular-nums">{Math.round((n / total) * 100)}%</span>
                </button>
              </li>
            ))}
          </ul>
        </div>
      )}
    </Panel>
  );
}

// ---------------- Live feed ----------------

const levelTone: Record<LogEvent["level"], Tone> = { info: "info", warn: "warn", error: "bad" };

function clock(iso: string): string {
  const d = new Date(iso);
  const p = (n: number) => String(n).padStart(2, "0");
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
}

// routine are log lines the feed leaves to the 日志 page: reconnects, and
// notes the feed already shows as calls.
const routine = /^【[^】]+】(已连接模组|AI 语音：|AI 助手通话小结)/;

interface FeedItem {
  key: string;
  at: string;
  icon: LucideIcon;
  tone: Tone;
  title: ReactNode;
  text: string;
  to?: string;
}

function FeedCard({ status }: { status: Status }) {
  const navigate = useNavigate();
  const events = useQuery({ queryKey: ["events", "feed"], queryFn: () => api.get<LogEvent[]>("/api/events?limit=40") });
  const messages = useQuery({ queryKey: ["messages", "feed"], queryFn: () => api.get<Page<Message>>("/api/messages?pageSize=10") });
  const calls = useQuery({ queryKey: ["calls", "dashboard"], queryFn: () => api.get<Page<CallRecord>>("/api/calls?page=1&pageSize=30") });
  const multi = knownDevices(status).length > 1;
  const where = (id: string) => (multi ? `${deviceName(status, id)} · ` : "");

  const items: FeedItem[] = [];
  for (const m of messages.data?.items ?? []) {
    if (m.direction !== "in") continue;
    items.push({
      key: "m" + m.id,
      at: m.receivedAt,
      icon: categoryStyle(m.category).icon,
      tone: "info",
      title: (
        <>
          短信 · <span className="text-fg">{m.sender}</span>
          {m.category && <span className="ml-1.5 text-subtle">{m.category}</span>}
        </>
      ),
      text: where(m.deviceId) + preview(m),
      to: `/messages?d=${encodeURIComponent(m.deviceId)}&peer=${encodeURIComponent(m.peer)}`,
    });
  }
  for (const c of (calls.data?.items ?? []).slice(0, 10)) {
    const ai = c.handledBy === "assistant";
    const missed = c.status === "missed" || c.status === "rejected";
    items.push({
      key: "c" + c.id,
      at: c.startedAt,
      icon: ai ? Bot : missed ? PhoneMissed : c.direction === "in" ? PhoneIncoming : PhoneOutgoing,
      tone: missed ? "warn" : "ok",
      title: (
        <>
          {ai ? "AI 接听" : missed ? "未接来电" : c.direction === "in" ? "来电" : "去电"} · <span className="text-fg">{c.number || "未知号码"}</span>
        </>
      ),
      text: where(c.deviceId) + (ai ? c.summary?.brief || "正在生成小结…" : c.status === "completed" ? `通话 ${c.durationSec} 秒` : ""),
      to: "/calls",
    });
  }
  for (const e of events.data ?? []) {
    if (e.level === "info" && routine.test(e.text)) continue;
    items.push({ key: "e" + e.id, at: e.at, icon: e.level === "info" ? Terminal : AlertTriangle, tone: levelTone[e.level] ?? "info", title: e.level === "info" ? "系统" : e.level === "warn" ? "警告" : "错误", text: e.text, to: "/logs" });
  }
  items.sort((x, y) => y.at.localeCompare(x.at));
  const shown = items.slice(0, 14);
  const loading = events.isPending || messages.isPending || calls.isPending;

  return (
    <Panel
      icon={Terminal}
      title="实时动态"
      tag="live"
      action={
        <Button size="sm" variant="ghost" onClick={() => navigate("/logs")}>
          日志
        </Button>
      }
      bodyClass="p-0"
    >
      {loading ? (
        <div className="flex h-24 items-center justify-center">
          <Spinner />
        </div>
      ) : shown.length === 0 ? (
        <p className="py-6 text-center text-xs text-muted">还没有动态</p>
      ) : (
        <ol className="scroll-thin max-h-[26rem] overflow-y-auto py-1">
          {shown.map((it) => (
            <li key={it.key}>
              <button type="button" disabled={!it.to} onClick={() => it.to && navigate(it.to)} className="flex w-full gap-2.5 px-4 py-1.5 text-left transition hover:bg-panel-2/60">
                <span className="w-12 shrink-0 pt-px font-mono text-[10px] text-subtle tabular-nums">{clock(it.at).slice(0, 5)}</span>
                <it.icon className={clsx("mt-0.5 size-3.5 shrink-0", toneText[it.tone])} />
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-[12px] font-medium text-muted">{it.title}</span>
                  {it.text && <span className={clsx("line-clamp-2 text-[12px]", it.tone === "bad" ? "text-bad" : "text-subtle")}>{it.text}</span>}
                </span>
              </button>
            </li>
          ))}
        </ol>
      )}
    </Panel>
  );
}

// ---------------- Recent conversations ----------------

function RecentCard({ status }: { status: Status }) {
  const navigate = useNavigate();
  const multi = knownDevices(status).length > 1;
  const { data, isPending } = useQuery({ queryKey: ["conversations", "recent"], queryFn: () => api.get<Page<Conversation>>("/api/conversations?pageSize=6") });
  return (
    <Panel
      icon={MessagesSquare}
      title="最近会话"
      tag={`${status.messages.total} total`}
      action={
        <Button size="sm" variant="ghost" onClick={() => navigate("/messages")}>
          全部
        </Button>
      }
      bodyClass="p-2"
    >
      {isPending ? (
        <div className="flex h-40 items-center justify-center">
          <Spinner />
        </div>
      ) : !data?.items.length ? (
        <Empty icon={Inbox} title="还没有短信">
          收到的短信会按号码汇总在这里。
        </Empty>
      ) : (
        <div className="grid gap-0.5 xl:grid-cols-2">
          {data.items.map((c) => (
            <button
              key={c.deviceId + c.peer}
              type="button"
              onClick={() => navigate(`/messages?d=${encodeURIComponent(c.deviceId)}&peer=${encodeURIComponent(c.peer)}`)}
              className="flex min-w-0 items-center gap-3 rounded-xl px-2.5 py-2 text-left transition hover:bg-panel-2"
            >
              <SenderAvatar message={c.last.direction === "out" ? { ...c.last, category: "个人消息" } : c.last} />
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <span className={clsx("truncate text-[13px]", c.unread ? "font-semibold" : "font-medium")}>{c.last.direction === "in" ? c.last.sender || c.peer : c.peer}</span>
                  {c.last.direction === "in" && c.last.category && <CategoryChip name={c.last.category} className="hidden shrink-0 sm:inline-flex" />}
                  <span className="ml-auto shrink-0 font-mono text-[10px] text-subtle">{shortTime(c.lastAt)}</span>
                </div>
                <p className="truncate text-[12px] text-muted">
                  {multi && <span className="text-subtle">{deviceName(status, c.deviceId)} · </span>}
                  {c.last.direction === "out" && "我："}
                  {preview(c.last)}
                </p>
              </div>
              {c.unread > 0 && <span className="bg-gradient-accent min-w-5 rounded-full px-1.5 text-center font-mono text-[10px] leading-5 font-semibold text-white">{c.unread}</span>}
            </button>
          ))}
        </div>
      )}
    </Panel>
  );
}

// ---------------- AI notes ----------------

const urgencyTone: Record<string, Tone> = { 高: "bad", 中: "warn", 低: "muted" };

// AssistantNotesCard lists the calls the AI assistant took lately, one line
// each; a row opens to the full note.
function AssistantNotesCard() {
  const navigate = useNavigate();
  const [open, setOpen] = useState<number | null>(null);
  const { data } = useQuery({ queryKey: ["calls", "dashboard"], queryFn: () => api.get<Page<CallRecord>>("/api/calls?page=1&pageSize=30") });
  // Only notes from the last three days; older ones live on the 电话 page.
  const since = Date.now() - 3 * 86400_000;
  const notes = (data?.items ?? []).filter((c) => c.handledBy === "assistant" && new Date(c.startedAt).getTime() > since).slice(0, 5);
  if (notes.length === 0) return null;
  return (
    <Panel
      icon={Bot}
      title="AI 代接小结"
      tag="3D"
      action={
        <Button size="sm" variant="ghost" onClick={() => navigate("/calls?kind=assistant")}>
          全部
        </Button>
      }
      bodyClass="divide-y divide-line p-0"
    >
      {notes.map((c) => {
        const s = c.summary;
        const expanded = open === c.id;
        return (
          <div key={c.id}>
            <button type="button" aria-expanded={expanded} onClick={() => setOpen(expanded ? null : c.id)} className="flex w-full items-start gap-2.5 px-4 py-2.5 text-left transition hover:bg-panel-2/60">
              <span className="mt-[5px]">
                <Led tone={urgencyTone[s?.urgency ?? ""] ?? "muted"} />
              </span>
              <span className="min-w-0 flex-1">
                <span className="flex items-center gap-2 font-mono text-[11px] text-subtle">
                  <span className="text-fg">{c.number || "未知号码"}</span>
                  {shortTime(c.startedAt)}
                  {s?.category && <span className="truncate font-sans">· {s.category}</span>}
                </span>
                <span className="line-clamp-2 text-[13px]">{s?.brief || s?.purpose || (c.endedAt ? "正在生成小结…" : "通话进行中")}</span>
              </span>
              <ChevronDown className={clsx("mt-1 size-3.5 shrink-0 text-subtle transition", expanded && "rotate-180")} />
            </button>
            {expanded && (
              <div className="px-4 pb-3">
                <AssistantNote summary={c.summary} transcript={c.transcript} pending={!c.summary && !!c.endedAt} className="" />
                {c.hasRecording && <Recording id={c.id} />}
              </div>
            )}
          </div>
        );
      })}
    </Panel>
  );
}

// ---------------- Call backs ----------------

// CallbacksCard lists calls still waiting for a call back, so nothing the
// assistant took down gets forgotten.
function CallbacksCard({ items, status }: { items: Callback[]; status: Status }) {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();
  const multi = knownDevices(status).length > 1;
  const done = async (c: Callback) => {
    try {
      await api.post(`/api/calls/${c.id}/callback-done`);
      qc.invalidateQueries({ queryKey: ["callbacks"] });
    } catch (e) {
      toast.error((e as Error).message);
    }
  };
  return (
    <Card className="ring-1 ring-accent/30">
      <div className="flex items-center gap-2 border-b border-line px-4 py-2.5">
        <PhoneOutgoing className="size-4 text-accent" />
        <span className="text-sm font-semibold">待回电</span>
        <span className="font-mono text-[11px] text-subtle">{items.length}</span>
        <span className="ml-auto text-[11px] text-subtle">回拨过或点“已处理”后会消失</span>
      </div>
      <ul className="grid divide-y divide-line lg:grid-cols-2 lg:divide-y-0">
        {items.map((c) => (
          <li key={c.id} className="flex items-start gap-3 px-4 py-2.5 lg:border-b lg:border-line lg:odd:border-r">
            <span className="mt-[5px]">
              <Led tone={c.urgency === "高" ? "bad" : c.handledBy === "assistant" ? "warn" : "muted"} />
            </span>
            <div className="min-w-0 flex-1">
              <p className="flex items-center gap-2 font-mono text-[12px]">
                <span className="font-semibold">{c.number}</span>
                <span className="text-subtle">{shortTime(c.startedAt)}</span>
                {multi && <span className="truncate font-sans text-[11px] text-subtle">{deviceName(status, c.deviceId)}</span>}
              </p>
              <p className="line-clamp-2 text-[13px]">{c.reason}</p>
              {c.callback && <p className="truncate text-[11px] text-muted">回电：{c.callback}</p>}
            </div>
            <div className="flex shrink-0 items-center gap-0.5">
              <Button size="sm" variant="success" icon={Phone} onClick={() => navigate(`/calls?to=${encodeURIComponent(c.number)}&d=${encodeURIComponent(c.deviceId)}`)}>
                回拨
              </Button>
              <IconButton icon={MessageSquare} label="发短信" onClick={() => navigate(`/messages?d=${encodeURIComponent(c.deviceId)}&peer=${encodeURIComponent(c.peer)}`)} />
              <IconButton icon={Check} label="已处理" onClick={() => done(c)} />
            </div>
          </li>
        ))}
      </ul>
    </Card>
  );
}
