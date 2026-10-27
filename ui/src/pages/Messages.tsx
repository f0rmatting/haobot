import clsx from "clsx";
import { Fragment, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useSearchParams } from "react-router-dom";
import { AlertCircle, ArrowLeft, Check, Copy, Inbox, Loader2, MessageSquarePlus, MessagesSquare, MoreHorizontal, Phone, RotateCw, ScrollText, Search, Send, ShieldCheck, Tags, Trash2, UserPlus, X } from "lucide-react";
import { api, type ClassifierSettings, type Conversation, type Finding, type Message, type Page, type Status } from "../lib/api";
import { useStatus } from "../lib/hooks";
import { bytes, dateTime, shortTime } from "../lib/format";
import { categoryStyle, sourceLabel } from "../lib/categories";
import { deviceName, knownDevices, sendState } from "../lib/devices";
import { isPersonalNumber, smsParts } from "../lib/sms";
import { usePrefs } from "../lib/prefs";
import { Badge, Button, Card, ConfirmModal, Empty, IconButton, Input, Select, Spinner, Textarea, useToast } from "../components/ui";
import { CategoryChip, CodePill, SenderAvatar, preview } from "../components/MessageParts";
import { NewMessageModal } from "../components/NewMessage";
import { ContactModal } from "../components/ContactModal";
import { Popover } from "../components/Popover";
import { ClassificationModal } from "../components/ClassifyLog";

const PAGE = 30;

export function Messages() {
  const [params, setParams] = useSearchParams();
  const navigate = useNavigate();
  const { data: status } = useStatus();
  const q = params.get("q") ?? "";
  const category = params.get("category") ?? "";
  const since = params.get("since") ?? "";
  const unread = params.get("unread") === "1";
  const device = params.get("device") ?? "";
  const openDevice = params.get("d");
  const openPeer = params.get("peer");
  const [search, setSearch] = useState(q);
  const [composing, setComposing] = useState(false);
  const devices = knownDevices(status);
  const multi = devices.length > 1;

  const update = (patch: Record<string, string | null>) => {
    const next = new URLSearchParams(params);
    for (const [k, v] of Object.entries(patch)) {
      if (v) next.set(k, v);
      else next.delete(k);
    }
    setParams(next, { replace: true });
  };

  useEffect(() => {
    const t = setTimeout(() => search !== q && update({ q: search }), 300);
    return () => clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [search]);

  const list = useInfiniteQuery({
    queryKey: ["conversations", q, category, since, unread, device],
    queryFn: ({ pageParam }) => {
      const p = new URLSearchParams({ page: String(pageParam), pageSize: String(PAGE) });
      if (q) p.set("q", q);
      if (category) p.set("category", category);
      if (since) p.set("since", since);
      if (unread) p.set("unread", "1");
      if (device) p.set("device", device);
      return api.get<Page<Conversation>>(`/api/conversations?${p}`);
    },
    initialPageParam: 1,
    getNextPageParam: (last) => (last.page * last.pageSize < last.total ? last.page + 1 : undefined),
  });
  const items = useMemo(() => list.data?.pages.flatMap((p) => p.items) ?? [], [list.data]);
  const total = list.data?.pages[0]?.total ?? 0;
  const categories = Object.entries(status?.messages.categories ?? {}).sort((a, b) => b[1] - a[1]);
  const pending = status?.messages.pending ?? 0;
  const isOpen = openDevice !== null && openPeer !== null;

  const open = (c: { deviceId: string; peer: string }) => update({ d: c.deviceId, peer: c.peer });
  const close = () => update({ d: null, peer: null });

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">短信</h1>
          <p className="mt-0.5 text-sm text-muted">
            {total} 个会话
            {device && ` · ${deviceName(status, device)}`}
            {since === "today" && " · 今天收到的"}
            {category && ` · ${category === "-" ? "待分类" : category.split(",").join("、")}`}
            {unread && " · 有未读"}
          </p>
        </div>
        <Button variant="primary" size="sm" icon={MessageSquarePlus} onClick={() => setComposing(true)} disabled={!devices.length}>
          新短信
        </Button>
      </div>

      <div className="grid gap-4 lg:grid-cols-[minmax(0,400px)_minmax(0,1fr)]">
        <Card className={clsx("flex flex-col lg:h-[calc(100vh-10.5rem)]", isOpen && "hidden lg:flex")}>
          <div className="space-y-3 border-b border-line p-3">
            <div className="flex gap-2">
              <div className="relative min-w-0 flex-1">
                <Search className="pointer-events-none absolute top-1/2 left-3.5 size-4 -translate-y-1/2 text-subtle" />
                <Input value={search} onChange={(e) => setSearch(e.target.value)} placeholder="搜索号码或内容" className="pl-10" />
                {search && <IconButton icon={X} label="清除搜索" onClick={() => setSearch("")} className="absolute top-0.5 right-0.5" />}
              </div>
              {multi && (
                <div className="w-32 shrink-0">
                  <Select value={device} onChange={(e) => update({ device: e.target.value })} aria-label="按设备筛选">
                    <option value="">全部设备</option>
                    {devices.map((d) => (
                      <option key={d.id} value={d.id}>
                        {d.name}
                      </option>
                    ))}
                  </Select>
                </div>
              )}
            </div>
            <div className="scroll-thin -mx-1 flex gap-1.5 overflow-x-auto px-1 pb-0.5">
              <FilterChip active={!category && !unread && !since} onClick={() => update({ category: null, unread: null, since: null })}>
                全部
              </FilterChip>
              <FilterChip active={unread} onClick={() => update({ unread: unread ? null : "1" })}>
                未读 {status?.messages.unread ? status.messages.unread : ""}
              </FilterChip>
              {categories.map(([name, n]) => (
                <FilterChip key={name} active={category === name} dot={categoryStyle(name).dot} onClick={() => update({ category: category === name ? null : name, since: null })}>
                  {name} <span className="text-subtle">{n}</span>
                </FilterChip>
              ))}
              {(category.includes(",") || since) && (
                <FilterChip active onClick={() => update({ category: null, since: null })}>
                  {since === "today" && "今天 · "}
                  {category ? category.split(",").join("、") : "全部"}
                  <X className="size-3" />
                </FilterChip>
              )}
              {pending > 0 && (
                <FilterChip active={category === "-"} onClick={() => update({ category: category === "-" ? null : "-" })}>
                  待分类 <span className="text-subtle">{pending}</span>
                </FilterChip>
              )}
            </div>
          </div>
          <div className="scroll-thin min-h-0 flex-1 overflow-y-auto p-2">
            {list.isPending ? (
              <div className="flex h-40 items-center justify-center">
                <Spinner />
              </div>
            ) : items.length === 0 ? (
              <Empty icon={Inbox} title={q || category || since || unread || device ? "没有符合条件的会话" : "还没有短信"}>
                {q || category || since || unread || device ? "换个关键词或筛选条件试试。" : "模组收到的短信会按号码汇总在这里。"}
              </Empty>
            ) : (
              <div className="space-y-0.5">
                {items.map((c) => (
                  <ConversationRow key={c.deviceId + "|" + c.peer} c={c} status={status} multi={multi} active={c.deviceId === openDevice && c.peer === openPeer} onClick={() => open(c)} />
                ))}
                {list.hasNextPage && (
                  <div className="py-3 text-center">
                    <Button size="sm" variant="ghost" loading={list.isFetchingNextPage} onClick={() => list.fetchNextPage()}>
                      加载更多
                    </Button>
                  </div>
                )}
              </div>
            )}
          </div>
        </Card>

        <div className={clsx(!isOpen && "hidden lg:block")}>
          {isOpen ? (
            <Thread key={openDevice + "|" + openPeer} deviceId={openDevice!} peer={openPeer!} status={status} multi={multi} onBack={close} />
          ) : (
            <Card className="flex h-[calc(100vh-10.5rem)] items-center justify-center">
              <Empty icon={MessagesSquare} title="选择一个会话">
                短信按“设备 + 号码”汇总成会话。打开会话可以查看往来记录并直接回复。
              </Empty>
            </Card>
          )}
        </div>
      </div>

      {composing && (
        <NewMessageModal
          status={status}
          defaultDevice={device || openDevice || ""}
          onClose={() => setComposing(false)}
          onSent={(m) => {
            setComposing(false);
            navigate(`/messages?d=${encodeURIComponent(m.deviceId)}&peer=${encodeURIComponent(m.peer)}`);
          }}
        />
      )}
    </div>
  );
}

function FilterChip({ active, onClick, children, dot }: { active: boolean; onClick: () => void; children: ReactNode; dot?: string }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={clsx(
        "inline-flex shrink-0 items-center gap-1.5 rounded-full px-3 py-1 text-xs font-medium whitespace-nowrap ring-1 transition ring-inset",
        active ? "bg-accent/12 text-fg ring-accent/40" : "bg-panel-2 text-muted ring-line hover:text-fg",
      )}
    >
      {dot && <span className={`size-1.5 rounded-full ${dot}`} />}
      {children}
    </button>
  );
}

function ConversationRow({ c, status, multi, active, onClick }: { c: Conversation; status?: Status; multi: boolean; active: boolean; onClick: () => void }) {
  const last = c.last;
  const outgoing = last.direction === "out";
  return (
    <button
      type="button"
      onClick={onClick}
      className={clsx("flex w-full gap-3 rounded-2xl px-3 py-3 text-left transition outline-none focus-visible:ring-2 focus-visible:ring-accent/60", active ? "bg-accent/10 ring-1 ring-accent/25" : "hover:bg-panel-2")}
    >
      <SenderAvatar message={{ ...last, category: outgoing ? "个人消息" : last.category, avatar: c.avatar }} />
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className={clsx("truncate text-sm", c.unread ? "font-semibold" : "font-medium text-fg/90")}>{displayPeer(c)}</span>
          {multi && <DeviceTag name={deviceName(status, c.deviceId)} />}
          <span className="ml-auto shrink-0 text-[11px] text-subtle tabular-nums">{shortTime(c.lastAt)}</span>
        </div>
        <div className="mt-0.5 flex items-start gap-2">
          <p className={clsx("line-clamp-2 flex-1 text-[13px] leading-relaxed", c.unread ? "text-fg/85" : "text-muted")}>
            {outgoing && last.status === "failed" && <AlertCircle className="mr-1 -mt-0.5 inline size-3.5 text-bad" />}
            {outgoing && <span className="text-subtle">我：</span>}
            {preview(last)}
          </p>
          {c.unread > 0 && <span className="bg-gradient-accent mt-0.5 min-w-5 shrink-0 rounded-full px-1.5 text-center text-[11px] leading-5 font-semibold text-white">{c.unread > 99 ? "99+" : c.unread}</span>}
        </div>
        <div className="mt-1.5 flex flex-wrap items-center gap-1.5">
          {!outgoing && last.category && <CategoryChip name={last.category} />}
          {!outgoing && last.code && <CodePill code={last.code} />}
          {c.total > 1 && <span className="text-[11px] text-subtle">{c.total} 条</span>}
        </div>
      </div>
    </button>
  );
}

function displayPeer(c: Conversation): string {
  return c.name || (c.last.direction === "in" ? c.last.sender || c.peer : c.peer);
}

function DeviceTag({ name }: { name: string }) {
  return <span className="max-w-24 shrink-0 truncate rounded-md bg-panel-2 px-1.5 py-0.5 text-[10px] font-medium text-muted ring-1 ring-line">{name}</span>;
}

// ---------------- Thread ----------------

function Thread({ deviceId, peer, status, multi, onBack }: { deviceId: string; peer: string; status?: Status; multi: boolean; onBack: () => void }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const toast = useToast();
  const [confirmDelete, setConfirmDelete] = useState(false);
  const scroller = useRef<HTMLDivElement>(null);
  const stick = useRef(true);
  const device = status?.devices.find((d) => d.id === deviceId);

  const thread = useInfiniteQuery({
    queryKey: ["thread", deviceId, peer],
    queryFn: ({ pageParam }) => api.get<Page<Message>>(`/api/conversations/messages?device=${encodeURIComponent(deviceId)}&peer=${encodeURIComponent(peer)}&page=${pageParam}&pageSize=${PAGE}`),
    initialPageParam: 1,
    getNextPageParam: (last) => (last.page * last.pageSize < last.total ? last.page + 1 : undefined),
  });
  // Pages arrive newest first; show oldest at the top.
  const messages = useMemo(() => (thread.data?.pages.flatMap((p) => p.items) ?? []).slice().reverse(), [thread.data]);
  const unread = messages.some((m) => m.direction === "in" && !m.read);
  const newestId = messages[messages.length - 1]?.id;
  const lastIncoming = [...messages].reverse().find((m) => m.direction === "in");

  useEffect(() => {
    if (!unread) return;
    api.post("/api/conversations/read", { deviceId, peer }).then(() => {
      qc.invalidateQueries({ queryKey: ["thread", deviceId, peer] });
      qc.invalidateQueries({ queryKey: ["conversations"] });
      qc.invalidateQueries({ queryKey: ["status"] });
    });
  }, [unread, deviceId, peer, qc]);

  // Keep the view pinned to the newest message unless the user scrolled up.
  useLayoutEffect(() => {
    const el = scroller.current;
    if (el && stick.current) el.scrollTop = el.scrollHeight;
  }, [newestId, messages.length]);

  const removeConversation = async () => {
    try {
      await api.post("/api/conversations/delete", { deviceId, peer });
      toast.ok("会话已删除");
      qc.invalidateQueries({ queryKey: ["conversations"] });
      qc.invalidateQueries({ queryKey: ["status"] });
      onBack();
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setConfirmDelete(false);
    }
  };

  const number = lastIncoming?.sender || peer;
  const named = messages[0];
  const [adding, setAdding] = useState(false);

  return (
    <Card className="animate-in flex h-[calc(100vh-10.5rem)] min-h-[480px] flex-col">
      <div className="flex items-center gap-3 border-b border-line px-4 py-3">
        <IconButton icon={ArrowLeft} label="返回会话列表" onClick={onBack} className="lg:hidden" />
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <h2 className="truncate text-[15px] font-semibold tracking-tight">{named?.name || number}</h2>
            {lastIncoming?.category && <CategoryChip name={lastIncoming.category} />}
          </div>
          <p className="truncate text-xs text-muted">
            {named?.name ? `${number} · ` : ""}
            {multi || device ? `经由 ${deviceName(status, deviceId)}` : ""}
            {device?.identity.number ? `（本机 ${device.identity.number}）` : ""}
            {thread.data ? ` · 共 ${thread.data.pages[0].total} 条` : ""}
          </p>
        </div>
        {isPersonalNumber(peer) && <IconButton icon={Phone} label="拨打电话" onClick={() => navigate(`/calls?to=${encodeURIComponent(peer)}&d=${encodeURIComponent(deviceId)}`)} />}
        {named && named.nameSource !== "contact" && <IconButton icon={UserPlus} label="加入通讯录" onClick={() => setAdding(true)} />}
        <IconButton icon={Trash2} label="删除会话" onClick={() => setConfirmDelete(true)} className="hover:text-bad" />
      </div>
      {adding && <ContactModal initial={{ name: named?.name ?? "", number, note: "" }} onClose={() => setAdding(false)} />}

      <div
        ref={scroller}
        onScroll={(e) => {
          const el = e.currentTarget;
          stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
        }}
        className="scroll-thin min-h-0 flex-1 space-y-1 overflow-y-auto px-4 py-4"
      >
        {thread.hasNextPage && (
          <div className="pb-2 text-center">
            <Button
              size="sm"
              variant="ghost"
              loading={thread.isFetchingNextPage}
              onClick={() => {
                stick.current = false;
                thread.fetchNextPage();
              }}
            >
              加载更早的消息
            </Button>
          </div>
        )}
        {thread.isPending ? (
          <div className="flex h-full items-center justify-center">
            <Spinner />
          </div>
        ) : messages.length === 0 ? (
          <Empty icon={Inbox} title="会话为空" />
        ) : (
          messages.map((m, i) => (
            <Fragment key={m.id}>
              {(i === 0 || !sameDay(messages[i - 1].sentAt, m.sentAt)) && <DayDivider at={m.sentAt} />}
              <Bubble m={m} />
            </Fragment>
          ))
        )}
      </div>

      <Composer
        deviceId={deviceId}
        peer={peer}
        status={status}
        onSent={() => {
          stick.current = true;
        }}
      />

      <ConfirmModal open={confirmDelete} onClose={() => setConfirmDelete(false)} onConfirm={removeConversation} danger title="删除整个会话？" description="与这个号码在这台设备上的全部往来短信都会被删除，无法恢复。" confirmLabel="删除" />
    </Card>
  );
}

function sameDay(a: string, b: string) {
  return new Date(a).toDateString() === new Date(b).toDateString();
}

function DayDivider({ at }: { at: string }) {
  const d = new Date(at);
  const today = new Date();
  const yesterday = new Date(Date.now() - 86400000);
  const label =
    d.toDateString() === today.toDateString()
      ? "今天"
      : d.toDateString() === yesterday.toDateString()
        ? "昨天"
        : d.getFullYear() === today.getFullYear()
          ? `${d.getMonth() + 1}月${d.getDate()}日`
          : `${d.getFullYear()}年${d.getMonth() + 1}月${d.getDate()}日`;
  return (
    <div className="flex items-center gap-3 py-3">
      <div className="h-px flex-1 bg-line" />
      <span className="text-[11px] font-medium text-subtle">{label}</span>
      <div className="h-px flex-1 bg-line" />
    </div>
  );
}

function Bubble({ m }: { m: Message }) {
  const out = m.direction === "out";
  const [menu, setMenu] = useState(false);
  const time = new Date(m.sentAt).toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" });
  return (
    <div className={clsx("group flex items-end gap-2", out ? "justify-end" : "justify-start")}>
      {out && <BubbleMenu m={m} open={menu} setOpen={setMenu} align="right" />}
      <div className={clsx("max-w-[85%] sm:max-w-[75%]", out && "items-end")}>
        <div
          className={clsx(
            "rounded-2xl px-3.5 py-2.5 text-[14px] leading-relaxed break-words whitespace-pre-wrap select-text",
            out ? "bg-gradient-accent rounded-br-md text-white shadow-lg shadow-cyan-500/10" : "rounded-bl-md bg-panel-2 ring-1 ring-line",
            out && m.status === "failed" && "opacity-70",
          )}
        >
          {m.binary ? <span className="font-mono text-xs">{m.body}</span> : m.kind === "mms" ? <MMSBody m={m} /> : m.body || <span className="text-subtle">（无内容）</span>}
        </div>
        <div className={clsx("mt-1 flex flex-wrap items-center gap-1.5 px-1 text-[11px] text-subtle", out && "justify-end")}>
          {!out && m.category && <CategoryChip name={m.category} />}
          {!out && m.code && <CodePill code={m.code} />}
          {!m.complete && <span className="text-warn">分段不完整</span>}
          <span title={dateTime(m.sentAt)}>{time}</span>
          {out && <SendStatus m={m} />}
        </div>
      </div>
      {!out && <BubbleMenu m={m} open={menu} setOpen={setMenu} align="left" />}
    </div>
  );
}

function MMSBody({ m }: { m: Message }) {
  return (
    <div>
      <Badge tone="info">彩信</Badge>
      <p className="mt-1">{m.body || "（无主题）"}</p>
      <p className="mt-1 text-xs text-muted">
        {m.mmsSize ? bytes(m.mmsSize) : ""}
        {m.mmsExpiry ? ` · 有效期至 ${dateTime(m.mmsExpiry)}` : ""}
      </p>
      <p className="mt-1 text-xs text-subtle">彩信的图片和文字存在运营商服务器上，这里只收到通知，看不到内容</p>
    </div>
  );
}

function SendStatus({ m }: { m: Message }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  if (m.status === "sending")
    return (
      <span className="flex items-center gap-1">
        <Loader2 className="size-3 animate-spin" />
        发送中
      </span>
    );
  if (m.status === "sent")
    return (
      <span className="flex items-center gap-0.5 text-ok" title="已提交给运营商">
        <Check className="size-3" />
        已发送
      </span>
    );
  return (
    <span className="flex items-center gap-1.5 text-bad" title={m.error}>
      <AlertCircle className="size-3" />
      发送失败{m.error ? `：${m.error}` : ""}
      <button
        type="button"
        disabled={busy}
        className="inline-flex items-center gap-0.5 rounded-md px-1 font-medium text-accent hover:bg-accent/10"
        onClick={async () => {
          setBusy(true);
          try {
            await api.post(`/api/messages/${m.id}/resend`);
            qc.invalidateQueries({ queryKey: ["thread"] });
          } catch (e) {
            toast.error((e as Error).message);
          } finally {
            setBusy(false);
          }
        }}
      >
        <RotateCw className={clsx("size-3", busy && "animate-spin")} />
        重发
      </button>
    </span>
  );
}

function BubbleMenu({ m, open, setOpen, align }: { m: Message; open: boolean; setOpen: (v: boolean) => void; align: "left" | "right" }) {
  const { pro } = usePrefs();
  const qc = useQueryClient();
  const toast = useToast();
  const anchor = useRef<HTMLButtonElement>(null);
  const [confirm, setConfirm] = useState(false);
  const [redacted, setRedacted] = useState(false);
  const [why, setWhy] = useState(false);
  const { data: classifier } = useQuery({ queryKey: ["classifier"], queryFn: () => api.get<ClassifierSettings>("/api/settings/classifier"), enabled: open });
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["thread"] });
    qc.invalidateQueries({ queryKey: ["conversations"] });
    qc.invalidateQueries({ queryKey: ["status"] });
  };
  const close = () => {
    setOpen(false);
    setRedacted(false);
  };
  return (
    <div className="mb-6 shrink-0">
      <IconButton ref={anchor} icon={MoreHorizontal} label="更多操作" onClick={() => setOpen(!open)} className={clsx("size-7 opacity-0 transition group-hover:opacity-100 focus:opacity-100", open && "opacity-100")} />
      <Popover anchor={anchor} open={open} onClose={close} width={260} align={align === "right" ? "end" : "start"}>
        <div className="space-y-1">
          {!m.binary && (
            <MenuItem
              icon={Copy}
              onClick={async () => {
                try {
                  await navigator.clipboard.writeText(m.body);
                  toast.ok("已复制");
                } catch {
                  toast.error("复制失败");
                }
                close();
              }}
            >
              复制内容
            </MenuItem>
          )}
          {m.direction === "in" && (
            <>
              <div className="px-2 py-1.5">
                <div className="mb-1 flex items-center gap-1.5 text-xs text-muted">
                  <Tags className="size-3.5" />
                  分类{m.category && `（${sourceLabel[m.categorySource] ?? ""}判定）`}
                </div>
                <Select
                  value={m.category}
                  onChange={async (e) => {
                    try {
                      await api.patch(`/api/messages/${m.id}`, { category: e.target.value });
                      toast.ok(`已改为“${e.target.value}”`);
                      refresh();
                    } catch (err) {
                      toast.error((err as Error).message);
                    }
                    close();
                  }}
                  className="h-8 text-xs"
                >
                  {!m.category && <option value="">（待分类）</option>}
                  {(classifier?.categories ?? []).map((c) => (
                    <option key={c.name} value={c.name}>
                      {c.name}
                    </option>
                  ))}
                </Select>
              </div>
              <MenuItem
                icon={ScrollText}
                onClick={() => {
                  setWhy(true);
                  close();
                }}
              >
                分类依据
              </MenuItem>
            </>
          )}
          {pro && m.direction === "in" && (
            <MenuItem icon={ShieldCheck} onClick={() => setRedacted(!redacted)}>
              {redacted ? "隐藏脱敏预览" : "脱敏预览"}
            </MenuItem>
          )}
          {redacted && <RedactedPreview id={m.id} />}
          {pro && <p className="px-2 text-[11px] text-subtle">编号 {m.id} · 入库 {dateTime(m.receivedAt)} · {m.parts} 段</p>}
          <MenuItem
            icon={Trash2}
            danger
            onClick={() => {
              setConfirm(true);
              close();
            }}
          >
            删除这条
          </MenuItem>
        </div>
      </Popover>
      {why && <ClassificationModal messageId={m.id} onClose={() => setWhy(false)} />}
      <ConfirmModal
        open={confirm}
        onClose={() => setConfirm(false)}
        danger
        title="删除这条短信？"
        description="删除后无法恢复。"
        confirmLabel="删除"
        onConfirm={async () => {
          try {
            await api.del(`/api/messages/${m.id}`);
            refresh();
          } catch (e) {
            toast.error((e as Error).message);
          }
          setConfirm(false);
        }}
      />
    </div>
  );
}

function MenuItem({ icon: Icon, onClick, children, danger }: { icon: typeof Copy; onClick: () => void; children: ReactNode; danger?: boolean }) {
  return (
    <button type="button" onClick={onClick} className={clsx("flex w-full items-center gap-2 rounded-xl px-2 py-1.5 text-left text-[13px] transition hover:bg-panel-2", danger ? "text-bad" : "text-fg")}>
      <Icon className="size-4" />
      {children}
    </button>
  );
}

function RedactedPreview({ id }: { id: number }) {
  const { data, isPending } = useQuery({
    queryKey: ["message", id, "redacted"],
    queryFn: () => api.get<{ sender: string; text: string; findings: Finding[] }>(`/api/messages/${id}/redacted`),
  });
  if (isPending) return <Spinner className="mx-2" />;
  if (!data) return null;
  return (
    <div className="space-y-1.5 px-2">
      <div className="flex flex-wrap gap-1">{data.findings.length === 0 ? <Badge tone="ok">无敏感信息</Badge> : data.findings.map((f) => <Badge key={f.kind} tone="warn">{`${f.kind} ×${f.count}`}</Badge>)}</div>
      <pre className="max-h-40 overflow-y-auto rounded-lg bg-panel-2 p-2 font-mono text-[11px] whitespace-pre-wrap ring-1 ring-line">{data.text}</pre>
    </div>
  );
}

// ---------------- Composer ----------------

function Composer({ deviceId, peer, status, onSent }: { deviceId: string; peer: string; status?: Status; onSent: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const device = status?.devices.find((d) => d.id === deviceId);
  const can = sendState(device);
  const est = smsParts(text);
  const tooLong = est.parts > 10;

  const send = async () => {
    if (!text.trim() || !can.ok || tooLong) return;
    setBusy(true);
    try {
      await api.post<Message>("/api/messages/send", { deviceId, to: peer, text });
      setText("");
      onSent();
      qc.invalidateQueries({ queryKey: ["thread", deviceId, peer] });
      qc.invalidateQueries({ queryKey: ["conversations"] });
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="border-t border-line p-3">
      {!isPersonalNumber(peer) && (
        <p className="mb-2 flex items-center gap-1.5 text-[11px] text-warn">
          <AlertCircle className="size-3.5 shrink-0" />
          这像是服务号码（短号或 106 开头），很多不接收回复，或会按增值业务收费。
        </p>
      )}
      <div className="flex items-end gap-2">
        <Textarea
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
              e.preventDefault();
              send();
            }
          }}
          disabled={!can.ok}
          rows={2}
          placeholder={can.ok ? "输入回复内容，⌘/Ctrl + Enter 发送" : `无法发送：${can.reason}`}
          className="max-h-40 min-h-11 flex-1 resize-none"
        />
        <Button variant="primary" icon={Send} loading={busy} disabled={!can.ok || !text.trim() || tooLong} onClick={send} className="h-11">
          发送
        </Button>
      </div>
      {text && (
        <p className={clsx("mt-1.5 text-[11px]", tooLong ? "text-bad" : "text-subtle")}>
          {est.chars} 字 · {est.encoding} · {tooLong ? "超过 10 条上限" : `将分 ${est.parts} 条发送`}
          {est.parts > 1 && !tooLong && "（对方手机会自动合并）"}
        </p>
      )}
    </div>
  );
}

