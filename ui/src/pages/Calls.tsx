import clsx from "clsx";
import { useEffect, useMemo, useState } from "react";
import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useSearchParams } from "react-router-dom";
import { Bot, ChevronDown, Delete, MessageSquare, Mic, Phone, PhoneIncoming, PhoneMissed, PhoneOff, PhoneOutgoing, Trash2, UserPlus, X } from "lucide-react";
import { api, type CallRecord, type CallSummary, type Page, type TranscriptLine } from "../lib/api";
import { useStatus } from "../lib/hooks";
import { dateTime, shortTime } from "../lib/format";
import { deviceHealth, deviceName, knownDevices } from "../lib/devices";
import { callAudio } from "../lib/callAudio";
import { Badge, Button, Card, CardBody, CardHeader, Collapsible, ConfirmModal, Dot, Empty, IconButton, Input, PeerAvatar, Segmented, Select, Spinner, useToast } from "../components/ui";
import { callAction } from "../components/CallCenter";
import { MicCheck } from "../components/MicCheck";
import { ContactModal } from "../components/ContactModal";

type Filter = "all" | "assistant" | "missed";

export function Calls() {
  const { data: status } = useStatus();
  const [params, setParams] = useSearchParams();
  const qc = useQueryClient();
  const [filter, setFilter] = useState<Filter>(() => (["assistant", "missed"].includes(params.get("kind") ?? "") ? (params.get("kind") as Filter) : "all"));
  const devices = knownDevices(status);
  const inCall = devices.some((d) => d.calls.length > 0);
  // The dialer opens on demand, or straight away when calling back.
  const [dialing, setDialing] = useState(() => params.has("to"));
  useEffect(() => {
    if (params.has("to")) setDialing(true);
  }, [params]);
  const closeDialer = () => {
    setDialing(false);
    if (params.has("to") || params.has("d")) setParams({}, { replace: true });
  };

  // Opening the page clears the missed-call badge.
  useEffect(() => {
    if (status?.missedCalls) api.post("/api/calls/seen").then(() => qc.invalidateQueries({ queryKey: ["status"] }));
  }, [status?.missedCalls, qc]);

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">电话</h1>
          <p className="mt-0.5 text-sm text-muted">通话记录和 AI 接听小结；也可以用模组里的 SIM 卡在网页上拨打电话</p>
        </div>
        <Button variant={dialing ? "secondary" : "success"} icon={dialing ? X : Phone} disabled={!devices.length} onClick={() => (dialing ? closeDialer() : setDialing(true))}>
          {dialing ? "收起拨号" : inCall ? "通话中" : "拨号"}
        </Button>
      </div>
      <div className={clsx("grid gap-5", dialing && "lg:grid-cols-[360px_minmax(0,1fr)] lg:items-start")}>
        {dialing && (
          <div className="animate-in space-y-5 lg:sticky lg:top-6">
            <Dialer initial={params.get("to") ?? ""} initialDevice={params.get("d") ?? ""} />
            <Collapsible icon={Mic} title="麦克风和耳机检测" subtitle="第一次打电话前建议试一下">
              <MicCheck bare />
            </Collapsible>
          </div>
        )}
        <Card className="min-h-96">
          <CardHeader
            icon={PhoneIncoming}
            title="通话记录"
            action={
              <Segmented
                size="sm"
                value={filter}
                onChange={setFilter}
                options={[
                  { value: "all", label: "全部" },
                  { value: "assistant", label: "AI 接听" },
                  { value: "missed", label: "未接" },
                ]}
              />
            }
          />
          <CardBody className="pt-2">
            <CallLog filter={filter} multi={devices.length > 1} />
          </CardBody>
        </Card>
      </div>
    </div>
  );
}

const keys = ["1", "2", "3", "4", "5", "6", "7", "8", "9", "*", "0", "#"];

function Dialer({ initial, initialDevice }: { initial: string; initialDevice: string }) {
  const { data: status } = useStatus();
  const toast = useToast();
  const qc = useQueryClient();
  const devices = knownDevices(status);
  const [to, setTo] = useState(initial);
  const [deviceId, setDeviceId] = useState(initialDevice);
  const [busy, setBusy] = useState(false);
  useEffect(() => setTo(initial), [initial]);
  const device = devices.find((d) => d.id === deviceId) ?? devices.find((d) => deviceHealth(d).tone === "ok") ?? devices[0];
  const inCall = devices.some((d) => d.calls.length > 0);
  const ready = device && device.connected && device.modem.sim === "READY" && device.modem.registered && device.enabled;

  const dial = async () => {
    if (!device) return;
    setBusy(true);
    // Audio starts inside the click so the browser lets it play.
    void callAudio.start(device.id);
    try {
      await callAction(device.id, "dial", { to });
      qc.invalidateQueries({ queryKey: ["status"] });
    } catch (e) {
      toast.error((e as Error).message);
      callAudio.stop();
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card>
      <CardHeader icon={Phone} title="拨号" subtitle={device ? `用「${device.name}」${device.identity.number ? `（${device.identity.number}）` : ""}拨出` : "没有可用的设备"} />
      <CardBody className="space-y-4">
        {devices.length > 1 && (
          <Select value={device?.id ?? ""} onChange={(e) => setDeviceId(e.target.value)}>
            {devices.map((d) => (
              <option key={d.id} value={d.id}>
                {d.name} · {deviceHealth(d).label}
              </option>
            ))}
          </Select>
        )}
        <div className="relative">
          <Input value={to} onChange={(e) => setTo(e.target.value.replace(/[^\d+*#]/g, ""))} inputMode="tel" placeholder="输入号码" className="h-14 pr-12 text-center font-mono text-2xl tracking-wider" onKeyDown={(e) => e.key === "Enter" && to && ready && dial()} />
          {to && <IconButton icon={Delete} label="删除" onClick={() => setTo(to.slice(0, -1))} className="absolute top-2.5 right-1.5" />}
        </div>
        <div className="grid grid-cols-3 gap-2.5">
          {keys.map((k) => (
            <button key={k} type="button" onClick={() => setTo(to + k)} className="h-14 rounded-2xl bg-panel-2 text-xl font-semibold ring-1 ring-line transition hover:bg-accent/10 active:scale-95">
              {k}
            </button>
          ))}
        </div>
        <Button variant="success" size="lg" icon={Phone} className="w-full" loading={busy} disabled={!to || !ready || inCall} onClick={dial}>
          {inCall ? "正在通话中" : "拨打"}
        </Button>
        {device && !ready && (
          <p className="flex items-center gap-2 text-xs text-warn">
            <Dot tone="warn" />
            {!device.connected ? "设备未连接" : device.modem.sim !== "READY" ? "SIM 卡未就绪" : !device.enabled ? "设备已停用" : "还没有注册到网络"}
          </p>
        )}
        <p className="text-[11px] leading-relaxed text-subtle">第一次拨打时浏览器会询问麦克风权限。只能在 localhost 或 HTTPS 下使用麦克风，否则只能听不能说。通话按运营商标准资费计费。</p>
      </CardBody>
    </Card>
  );
}

const statusMeta: Record<CallRecord["status"], { label: string; tone: string }> = {
  completed: { label: "", tone: "" },
  missed: { label: "未接", tone: "text-bad" },
  rejected: { label: "已拒接", tone: "text-warn" },
  canceled: { label: "未接通", tone: "text-subtle" },
  ringing: { label: "响铃中", tone: "text-ok" },
  dialing: { label: "呼叫中", tone: "text-ok" },
  active: { label: "通话中", tone: "text-ok" },
};

function talkTime(sec: number) {
  if (sec < 60) return `${sec} 秒`;
  return `${Math.floor(sec / 60)} 分 ${sec % 60} 秒`;
}

function CallLog({ filter, multi }: { filter: Filter; multi: boolean }) {
  const { data: status } = useStatus();
  const navigate = useNavigate();
  const toast = useToast();
  const qc = useQueryClient();
  const [confirm, setConfirm] = useState<CallRecord | null>(null);
  const [adding, setAdding] = useState<CallRecord | null>(null);
  const list = useInfiniteQuery({
    queryKey: ["calls", filter],
    queryFn: ({ pageParam }) => api.get<Page<CallRecord>>(`/api/calls?page=${pageParam}&pageSize=40${filter === "all" ? "" : `&kind=${filter}`}`),
    initialPageParam: 1,
    getNextPageParam: (last) => (last.page * last.pageSize < last.total ? last.page + 1 : undefined),
  });
  const items = useMemo(() => list.data?.pages.flatMap((p) => p.items) ?? [], [list.data]);

  if (list.isPending) return <Spinner />;
  if (items.length === 0)
    return (
      <Empty icon={PhoneIncoming} title={filter === "missed" ? "没有未接来电" : filter === "assistant" ? "AI 还没有接过电话" : "还没有通话记录"}>
        拨出、接听和未接的电话都会记录在这里。
      </Empty>
    );
  return (
    <div className="-mx-2">
      {items.map((c) => {
        const Icon = c.status === "missed" ? PhoneMissed : c.status === "rejected" ? PhoneOff : c.direction === "in" ? PhoneIncoming : PhoneOutgoing;
        const meta = statusMeta[c.status];
        return (
          <div key={c.id} className="group rounded-2xl px-3 py-2.5 transition hover:bg-panel-2">
          <div className="flex items-center gap-3">
            {c.avatar ? (
              <div className="relative shrink-0">
                <PeerAvatar src={c.avatar} />
                <span className={clsx("absolute -right-1 -bottom-1 flex size-5 items-center justify-center rounded-full ring-2 ring-panel-solid", c.status === "missed" ? "bg-bad text-white" : "bg-panel-2 text-muted")}>
                  <Icon className="size-3" />
                </span>
              </div>
            ) : (
              <div className={clsx("flex size-10 shrink-0 items-center justify-center rounded-2xl ring-1 ring-line", c.status === "missed" ? "bg-bad/10 text-bad" : "bg-panel-2 text-muted")}>
                <Icon className="size-[18px]" />
              </div>
            )}
            <div className="min-w-0 flex-1">
              <p className={clsx("truncate text-sm font-medium tabular-nums", c.status === "missed" && "text-bad")}>
                {c.name || c.number || "未知号码"}
                {c.handledBy === "assistant" && (
                  <Badge tone="info" icon={Bot} className="ml-2 align-middle">
                    AI 接听
                  </Badge>
                )}
                {multi && <span className="ml-2 text-[11px] font-normal text-subtle">{deviceName(status, c.deviceId)}</span>}
              </p>
              <p className="truncate text-xs text-muted" title={dateTime(c.startedAt)}>
                {c.name && `${c.number} · `}
                {c.direction === "in" ? "来电" : "去电"} · {shortTime(c.startedAt)}
                {c.status === "completed" ? ` · 通话 ${talkTime(c.durationSec)}` : <span className={meta.tone}> · {meta.label}</span>}
              </p>
            </div>
            <div className="flex items-center gap-0.5 opacity-70 transition group-hover:opacity-100">
              <IconButton icon={Phone} label="回拨" onClick={() => navigate(`/calls?to=${encodeURIComponent(c.number)}&d=${encodeURIComponent(c.deviceId)}`)} />
              <IconButton icon={MessageSquare} label="发短信" onClick={() => navigate(`/messages?d=${encodeURIComponent(c.deviceId)}&peer=${encodeURIComponent(c.peer)}`)} />
              {c.number && c.nameSource !== "contact" && <IconButton icon={UserPlus} label="加入通讯录" onClick={() => setAdding(c)} />}
              <IconButton icon={Trash2} label="删除记录" onClick={() => setConfirm(c)} className="hover:text-bad" />
            </div>
          </div>
          {c.handledBy === "assistant" && <AssistantNote summary={c.summary} transcript={c.transcript} pending={!c.summary && !!c.endedAt} />}
          {c.hasRecording && <Recording id={c.id} />}
          </div>
        );
      })}
      {list.hasNextPage && (
        <div className="pt-2 text-center">
          <Button size="sm" variant="ghost" loading={list.isFetchingNextPage} onClick={() => list.fetchNextPage()}>
            加载更多
          </Button>
        </div>
      )}
      {adding && <ContactModal initial={{ name: adding.name ?? "", number: adding.number, note: "" }} onClose={() => setAdding(null)} />}
      <ConfirmModal
        open={!!confirm}
        onClose={() => setConfirm(null)}
        danger
        title="删除这条通话记录？"
        confirmLabel="删除"
        onConfirm={async () => {
          try {
            await api.post("/api/calls/delete", { ids: [confirm!.id] });
            qc.invalidateQueries({ queryKey: ["calls"] });
          } catch (e) {
            toast.error((e as Error).message);
          }
          setConfirm(null);
        }}
      />
    </div>
  );
}

const urgencyTone: Record<string, string> = { 高: "text-bad", 中: "text-warn", 低: "text-subtle" };

// AssistantNote is what the AI assistant learned on a call.
export function AssistantNote({ summary, transcript, pending, className = "mt-2 ml-13" }: { summary: CallSummary | null; transcript: TranscriptLine[] | null; pending?: boolean; className?: string }) {
  const [open, setOpen] = useState(false);
  if (!summary)
    return <p className={clsx(className, "text-xs text-subtle")}>{pending ? "正在生成通话小结…" : "通话进行中"}</p>;
  const rows: [string, string | undefined][] = [
    ["来电者", summary.caller],
    ["来意", summary.purpose],
    ["回电", summary.callback],
  ];
  return (
    <div className={clsx(className, "rounded-2xl bg-accent/5 p-3 text-sm ring-1 ring-accent/20")}>
      <div className="flex flex-wrap items-center gap-2">
        <p className="min-w-0 flex-1 font-medium">{summary.brief || summary.purpose || "（没有小结）"}</p>
        {summary.category && <Badge tone="muted">{summary.category}</Badge>}
        {summary.urgency && <span className={clsx("text-xs font-medium", urgencyTone[summary.urgency] ?? "text-muted")}>紧急程度：{summary.urgency}</span>}
      </div>
      <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
        {rows
          .filter(([, v]) => v)
          .map(([k, v]) => (
            <div key={k} className="contents">
              <dt className="text-subtle">{k}</dt>
              <dd className="text-muted">{v}</dd>
            </div>
          ))}
      </dl>
      {summary.error && <p className="mt-2 text-xs text-warn">{summary.error}</p>}
      {transcript && transcript.length > 0 && (
        <>
          <button type="button" onClick={() => setOpen(!open)} className="mt-2 flex items-center gap-1 text-xs text-accent hover:underline">
            <ChevronDown className={clsx("size-3.5 transition", open && "rotate-180")} />
            {open ? "收起通话记录" : `查看通话记录（${transcript.length} 句）`}
          </button>
          {open && (
            <div className="mt-2 space-y-1.5 border-t border-line pt-2">
              {transcript.map((l, i) => (
                <p key={i} className="text-xs leading-relaxed">
                  <span className={clsx("mr-1.5 font-semibold", l.role === "caller" ? "text-ok" : "text-accent")}>{l.role === "caller" ? "对方" : "AI"}</span>
                  {l.text}
                </p>
              ))}
              {summary.usage && (summary.usage.inputTokens > 0 || summary.usage.outputTokens > 0) && (
                <p className="pt-1 text-[10px] text-subtle">
                  实时模型用量：输入 {summary.usage.inputTokens} tokens，输出 {summary.usage.outputTokens} tokens
                </p>
              )}
            </div>
          )}
        </>
      )}
    </div>
  );
}

// Recording plays a call's recording; it loads only when played.
export function Recording({ id }: { id: number }) {
  return (
    <div className="mt-2 ml-13 flex items-center gap-2">
      <audio controls preload="none" src={`/api/calls/${id}/recording`} className="h-9 w-full max-w-md" />
    </div>
  );
}
