import clsx from "clsx";
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Bot, ChevronDown, Hand, ListChecks, ScrollText, XCircle } from "lucide-react";
import { api, type ClassifyLog } from "../lib/api";
import { dateTime } from "../lib/format";
import { Badge, Button, CopyButton, Empty, Modal, Spinner } from "./ui";
import { CategoryChip } from "./MessageParts";

const sourceMeta = {
  rule: { label: "固定规则", icon: ListChecks, tone: "info" as const },
  llm: { label: "大模型", icon: Bot, tone: "ok" as const },
  manual: { label: "手动", icon: Hand, tone: "muted" as const },
};

export function SourceBadge({ l }: { l: ClassifyLog }) {
  if (l.error) return <Badge tone="bad" icon={XCircle}>失败</Badge>;
  const m = sourceMeta[l.source] ?? sourceMeta.rule;
  return (
    <Badge tone={m.tone} icon={m.icon}>
      {m.label}
    </Badge>
  );
}

// ClassifyLogDetail explains one attempt: the rule that fired, or the model
// request exactly as sent and the raw answer.
export function ClassifyLogDetail({ l }: { l: ClassifyLog }) {
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <SourceBadge l={l} />
        {l.category && <CategoryChip name={l.category} />}
        <span className="text-xs text-subtle">{dateTime(l.at)}</span>
      </div>
      {l.rule && (
        <p className="rounded-xl bg-panel-2 px-3 py-2 text-[13px] ring-1 ring-line">
          <span className="text-muted">判定依据：</span>
          {l.rule}
        </p>
      )}
      {l.error && <p className="rounded-xl bg-bad/8 px-3 py-2 text-[13px] text-bad ring-1 ring-bad/20">{l.error}</p>}
      {l.source === "llm" && (
        <>
          <dl className="grid grid-cols-2 gap-2 text-[13px] sm:grid-cols-4">
            <Info label="服务" value={l.provider === "anthropic" ? "Anthropic" : "OpenAI 兼容"} />
            <Info label="模型" value={l.model} mono />
            <Info label="耗时" value={`${l.latencyMs} ms`} />
            <Info label="置信度" value={l.error ? "—" : `${Math.round(l.confidence * 100)}%`} />
          </dl>
          {l.endpoint && (
            <p className="truncate font-mono text-[11px] text-subtle" title={l.endpoint}>
              POST {l.endpoint}
            </p>
          )}
          <Block title="发送给模型的内容（已脱敏）" text={l.prompt} open />
          <Block title="模型原始回复" text={l.reply || "（没有回复）"} open />
          <Block title="系统提示词" text={l.system} />
        </>
      )}
    </div>
  );
}

function Info({ label, value, mono }: { label: string; value?: string; mono?: boolean }) {
  return (
    <div className="rounded-xl bg-panel-2 px-3 py-2 ring-1 ring-line">
      <dt className="text-[11px] text-muted">{label}</dt>
      <dd className={clsx("truncate font-medium", mono && "font-mono text-xs")} title={value}>
        {value || "—"}
      </dd>
    </div>
  );
}

function Block({ title, text, open: initial }: { title: string; text?: string; open?: boolean }) {
  const [open, setOpen] = useState(!!initial);
  if (!text) return null;
  return (
    <div className="rounded-xl ring-1 ring-line">
      <div className="flex items-center justify-between gap-2 px-3 py-2">
        <button type="button" onClick={() => setOpen(!open)} className="flex items-center gap-1.5 text-xs font-medium text-muted hover:text-fg">
          <ChevronDown className={clsx("size-3.5 transition", !open && "-rotate-90")} />
          {title}
        </button>
        <CopyButton text={text} label="复制" />
      </div>
      {open && <pre className="scroll-thin max-h-56 overflow-auto border-t border-line px-3 py-2 font-mono text-[11.5px] leading-relaxed break-all whitespace-pre-wrap">{text}</pre>}
    </div>
  );
}

// ClassificationModal lists every classification attempt of a message.
export function ClassificationModal({ messageId, onClose }: { messageId: number; onClose: () => void }) {
  const { data, isPending } = useQuery({
    queryKey: ["message", messageId, "classification"],
    queryFn: () => api.get<{ items: ClassifyLog[] }>(`/api/messages/${messageId}/classification`),
  });
  const items = data?.items ?? [];
  return (
    <Modal
      open
      size="lg"
      onClose={onClose}
      title="分类依据"
      description="这条短信每一次分类的过程：发给大模型的内容和它的回复，以及手动修改的记录。"
      footer={
        <Button variant="primary" onClick={onClose} data-autofocus>
          关闭
        </Button>
      }
    >
      <div className="scroll-thin -mx-1 max-h-[60vh] space-y-5 overflow-y-auto px-1">
        {isPending ? (
          <Spinner />
        ) : items.length === 0 ? (
          <Empty icon={ScrollText} title="没有分类记录">
            这条短信是在记录功能上线前分类的，或者还在等待分类。
          </Empty>
        ) : (
          items.map((l, i) => (
            <div key={l.id} className={clsx(i > 0 && "border-t border-line pt-5")}>
              {i === 0 && items.length > 1 && <p className="mb-2 text-[11px] font-medium text-accent">当前结果</p>}
              {i === 1 && <p className="mb-2 text-[11px] font-medium text-subtle">更早的记录</p>}
              <ClassifyLogDetail l={l} />
            </div>
          ))
        )}
      </div>
    </Modal>
  );
}
