import { useMemo, useState } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";
import { ScrollText } from "lucide-react";
import { api, type ClassifyLog, type Page } from "../lib/api";
import { shortTime } from "../lib/format";
import { Button, Empty, Modal, Segmented, Spinner } from "./ui";
import { CategoryChip } from "./MessageParts";
import { ClassifyLogDetail, SourceBadge } from "./ClassifyLog";

type Filter = "all" | "llm" | "manual" | "failed";

// ClassifyLogList pages through classification attempts, so every model call
// and rule decision can be inspected. It is shown inside a collapsed section,
// so it only loads and refreshes while open.
export function ClassifyLogList() {
  const [filter, setFilter] = useState<Filter>("all");
  const [open, setOpen] = useState<ClassifyLog | null>(null);
  const list = useInfiniteQuery({
    queryKey: ["classify-logs", filter],
    queryFn: ({ pageParam }) => {
      const p = new URLSearchParams({ page: String(pageParam), pageSize: "30" });
      if (filter === "failed") p.set("failed", "1");
      else if (filter !== "all") p.set("source", filter);
      return api.get<Page<ClassifyLog>>(`/api/classify/logs?${p}`);
    },
    initialPageParam: 1,
    getNextPageParam: (last) => (last.page * last.pageSize < last.total ? last.page + 1 : undefined),
    refetchInterval: 15000,
  });
  const items = useMemo(() => list.data?.pages.flatMap((p) => p.items) ?? [], [list.data]);
  const total = list.data?.pages[0]?.total ?? 0;

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <Segmented
          size="sm"
          value={filter}
          onChange={setFilter}
          options={[
            { value: "all", label: "全部" },
            { value: "llm", label: "大模型" },
            { value: "manual", label: "手动" },
            { value: "failed", label: "失败" },
          ]}
        />
        <span className="text-[11px] text-subtle">共 {total} 条</span>
      </div>
      <div>
        {list.isPending ? (
          <Spinner />
        ) : items.length === 0 ? (
          <Empty icon={ScrollText} title="还没有记录">
            新短信分类后，这里会显示发给大模型的内容和它的回复。
          </Empty>
        ) : (
          <div className="scroll-thin -mx-2 max-h-[28rem] overflow-y-auto">
            {items.map((l) => (
              <button key={l.id} type="button" onClick={() => setOpen(l)} className="flex w-full items-center gap-3 rounded-xl px-2 py-2 text-left transition hover:bg-panel-2">
                <span className="w-14 shrink-0 text-[11px] text-subtle tabular-nums">{shortTime(l.at)}</span>
                <SourceBadge l={l} />
                <div className="min-w-0 flex-1">
                  <p className="truncate text-[13px]">
                    <span className="font-medium">{l.sender || `#${l.messageId}`}</span>
                    <span className="text-muted"> {l.body ? l.body.replace(/\s+/g, " ") : "（短信已删除）"}</span>
                  </p>
                  <p className="truncate text-[11px] text-subtle">{l.error || l.rule || (l.model ? `${l.model} · ${l.latencyMs} ms · 置信度 ${Math.round(l.confidence * 100)}%` : "")}</p>
                </div>
                {l.category && <CategoryChip name={l.category} className="hidden shrink-0 sm:inline-flex" />}
              </button>
            ))}
            {list.hasNextPage && (
              <div className="pt-2 text-center">
                <Button size="sm" variant="ghost" loading={list.isFetchingNextPage} onClick={() => list.fetchNextPage()}>
                  加载更多
                </Button>
              </div>
            )}
          </div>
        )}
      </div>
      {open && (
        <Modal
          open
          size="lg"
          onClose={() => setOpen(null)}
          title="分类记录"
          description={open.body ? `${open.sender}：${open.body}` : "对应的短信已删除"}
          footer={
            <Button variant="primary" onClick={() => setOpen(null)} data-autofocus>
              关闭
            </Button>
          }
        >
          <div className="scroll-thin -mx-1 max-h-[60vh] overflow-y-auto px-1">
            <ClassifyLogDetail l={open} />
          </div>
        </Modal>
      )}
    </div>
  );
}
