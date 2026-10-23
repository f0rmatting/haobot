import clsx from "clsx";
import { useEffect, useState } from "react";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { AlertCircle, AlertTriangle, ChevronLeft, ChevronRight, ChevronsLeft, ChevronsRight, Info, ScrollText, Search, X } from "lucide-react";
import { api, type LogEvent } from "../lib/api";
import { dateTime } from "../lib/format";
import { useStatus } from "../lib/hooks";
import { knownDevices } from "../lib/devices";
import { Card, CopyButton, Empty, IconButton, Input, Select, Spinner } from "../components/ui";
import { DeviceMark } from "../components/DeviceIdentity";

type Level = "" | "warn" | "error";

interface EventPage {
  items: LogEvent[];
  total: number;
  page: number;
  pageSize: number;
  counts: Record<LogEvent["level"], number>;
}

const levelMeta = {
  info: { icon: Info, label: "信息", row: "", chip: "text-info bg-info/10 ring-info/20" },
  warn: { icon: AlertTriangle, label: "警告", row: "bg-warn/[0.04]", chip: "text-warn bg-warn/10 ring-warn/20" },
  error: { icon: AlertCircle, label: "错误", row: "bg-bad/[0.05]", chip: "text-bad bg-bad/10 ring-bad/20" },
};

const pad = (n: number) => String(n).padStart(2, "0");

function clock(iso: string): string {
  const d = new Date(iso);
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

// dayLabel names the day an event belongs to, for the group headers.
function dayLabel(iso: string): string {
  const d = new Date(iso);
  const today = new Date();
  const start = new Date(today.getFullYear(), today.getMonth(), today.getDate()).getTime();
  const t = new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
  const week = "周" + "日一二三四五六"[d.getDay()];
  if (t === start) return `今天 · ${week}`;
  if (t === start - 86400000) return `昨天 · ${week}`;
  return `${d.getMonth() + 1} 月 ${d.getDate()} 日 · ${week}`;
}

// splitDevice separates the 【device】 prefix that device events carry.
function splitDevice(text: string): { device: string; text: string } {
  const m = /^【([^】]+)】([\s\S]*)$/.exec(text);
  return m ? { device: m[1], text: m[2] } : { device: "", text };
}

export function Logs() {
  const { data: status } = useStatus();
  const [level, setLevel] = useState<Level>("");
  const [search, setSearch] = useState("");
  const [q, setQ] = useState("");
  const [page, setPage] = useState(1);
  const [size, setSize] = useState(50);
  const devices = knownDevices(status);

  useEffect(() => {
    const t = setTimeout(() => {
      setQ(search.trim());
      setPage(1);
    }, 300);
    return () => clearTimeout(t);
  }, [search]);

  const { data, isPending, isFetching } = useQuery({
    queryKey: ["events", "page", level, q, page, size],
    queryFn: () => {
      const p = new URLSearchParams({ page: String(page), pageSize: String(size) });
      if (level) p.set("level", level);
      if (q) p.set("q", q);
      return api.get<EventPage>(`/api/events?${p}`);
    },
    placeholderData: keepPreviousData,
  });
  const pages = data ? Math.max(1, Math.ceil(data.total / data.pageSize)) : 1;
  const counts = data?.counts ?? { info: 0, warn: 0, error: 0 };
  const all = counts.info + counts.warn + counts.error;
  const pick = (l: Level) => {
    setLevel(l);
    setPage(1);
  };
  const go = (p: number) => {
    setPage(p);
    window.scrollTo({ top: 0 });
  };

  // Group the page by day, keeping the order.
  const groups: { day: string; items: LogEvent[] }[] = [];
  for (const e of data?.items ?? []) {
    const day = dayLabel(e.at);
    if (groups.at(-1)?.day !== day) groups.push({ day, items: [] });
    groups.at(-1)!.items.push(e);
  }

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">日志</h1>
        <p className="mt-0.5 text-sm text-muted">连接、SIM 卡、短信同步、AI 接听、推送和后台操作的记录，保留最近 10000 条</p>
      </div>

      <div className="grid grid-cols-3 gap-3">
        {(
          [
            ["", "全部", all, ScrollText, "text-accent"],
            ["warn", "警告及错误", counts.warn + counts.error, AlertTriangle, "text-warn"],
            ["error", "错误", counts.error, AlertCircle, "text-bad"],
          ] as const
        ).map(([value, label, n, Icon, tone]) => (
          <button
            key={value}
            type="button"
            onClick={() => pick(value)}
            className={clsx("glass rounded-2xl px-4 py-3 text-left transition", level === value ? "ring-1 ring-accent/50" : "hover:ring-1 hover:ring-line-strong")}
          >
            <div className="flex items-center gap-1.5 text-[11px] text-muted">
              <Icon className={clsx("size-3.5", tone)} />
              {label}
            </div>
            <div className="mt-0.5 font-mono text-xl font-semibold tabular-nums">{n}</div>
          </button>
        ))}
      </div>

      <Card>
        <div className="flex flex-wrap items-center gap-2 border-b border-line p-3">
          <div className="relative min-w-48 flex-1">
            <Search className="pointer-events-none absolute top-1/2 left-3.5 size-4 -translate-y-1/2 text-subtle" />
            <Input value={search} onChange={(e) => setSearch(e.target.value)} placeholder="搜索日志内容" className="pl-10" />
            {search && <IconButton icon={X} label="清除搜索" onClick={() => setSearch("")} className="absolute top-0.5 right-0.5" />}
          </div>
          {devices.length > 1 && (
            <div className="w-40">
              <Select value={devices.find((d) => search === `【${d.name}】`)?.name ?? ""} onChange={(e) => setSearch(e.target.value ? `【${e.target.value}】` : "")} aria-label="按设备筛选">
                <option value="">全部设备</option>
                {devices.map((d) => (
                  <option key={d.id} value={d.name}>
                    {d.name}
                  </option>
                ))}
              </Select>
            </div>
          )}
          {isFetching && !isPending && <Spinner className="size-4" />}
        </div>

        {isPending ? (
          <div className="flex h-48 items-center justify-center">
            <Spinner />
          </div>
        ) : !data || data.items.length === 0 ? (
          <Empty icon={ScrollText} title={q || level ? "没有符合条件的日志" : "还没有日志"} />
        ) : (
          <div>
            {groups.map((g) => (
              <section key={g.day}>
                <h2 className="border-b border-line bg-panel-2/60 px-4 py-1.5 text-[11px] font-medium text-muted">{g.day}</h2>
                <ol className="divide-y divide-line">
                  {g.items.map((e) => {
                    const meta = levelMeta[e.level] ?? levelMeta.info;
                    const { device, text } = splitDevice(e.text);
                    const dev = device ? devices.find((d) => d.name === device) : undefined;
                    return (
                      <li key={e.id} className={clsx("group flex items-start gap-3 px-4 py-2", meta.row)}>
                        <span className="w-14 shrink-0 pt-[3px] font-mono text-[11px] text-subtle tabular-nums" title={dateTime(e.at)}>
                          {clock(e.at)}
                        </span>
                        <span className={clsx("mt-px inline-flex shrink-0 items-center gap-1 rounded-md px-1.5 py-0.5 text-[10px] font-medium ring-1", meta.chip)}>
                          <meta.icon className="size-3" />
                          <span className="hidden sm:inline">{meta.label}</span>
                        </span>
                        <div className="min-w-0 flex-1 text-[13px] leading-relaxed break-words">
                          {device && (
                            <button type="button" onClick={() => setSearch(`【${device}】`)} className="mr-1.5 rounded-md transition hover:opacity-80" title="只看这台设备">
                              <DeviceMark d={dev} name={device} />
                            </button>
                          )}
                          <span className={clsx(e.level === "error" && "text-bad")}>{text}</span>
                        </div>
                        <span className="hidden shrink-0 opacity-0 transition group-hover:opacity-100 sm:block">
                          <CopyButton text={`${dateTime(e.at)} [${e.level}] ${e.text}`} label="复制" />
                        </span>
                      </li>
                    );
                  })}
                </ol>
              </section>
            ))}
          </div>
        )}

        {data && data.total > 0 && (
          <div className="flex flex-wrap items-center justify-between gap-3 border-t border-line px-4 py-3 text-xs text-muted">
            <span>共 {data.total} 条</span>
            <div className="flex items-center gap-1">
              <IconButton icon={ChevronsLeft} label="第一页" disabled={page <= 1} onClick={() => go(1)} />
              <IconButton icon={ChevronLeft} label="上一页" disabled={page <= 1} onClick={() => go(page - 1)} />
              <span className="px-2 font-mono tabular-nums">
                {page} / {pages}
              </span>
              <IconButton icon={ChevronRight} label="下一页" disabled={page >= pages} onClick={() => go(page + 1)} />
              <IconButton icon={ChevronsRight} label="最后一页" disabled={page >= pages} onClick={() => go(pages)} />
              <div className="ml-2 w-28">
                <Select
                  value={String(size)}
                  onChange={(e) => {
                    setSize(Number(e.target.value));
                    setPage(1);
                  }}
                  aria-label="每页条数"
                >
                  {[50, 100, 200].map((n) => (
                    <option key={n} value={n}>
                      每页 {n} 条
                    </option>
                  ))}
                </Select>
              </div>
            </div>
          </div>
        )}
      </Card>
    </div>
  );
}
