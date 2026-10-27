import { useMemo, useRef, useState } from "react";
import { useQuery, useQueryClient, type UseQueryResult } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import { BookOpen, BookUser, Download, MessageSquare, Pencil, Phone, Plus, RotateCcw, Search, Trash2, Upload, X } from "lucide-react";
import { api, type Contact, type ImportResult, type YellowOrg, type YellowPages } from "../lib/api";
import { useStatus } from "../lib/hooks";
import { knownDevices, sendState } from "../lib/devices";
import { Badge, Button, Card, CardBody, ConfirmModal, Empty, IconButton, Input, Modal, PeerAvatar, Segmented, Spinner, Switch, useToast } from "../components/ui";
import { ContactModal, type ContactDraft } from "../components/ContactModal";
import { OrgModal } from "../components/OrgModal";

type Tab = "mine" | "yellow";

export function Contacts() {
  const [tab, setTab] = useState<Tab>("mine");
  const [q, setQ] = useState("");
  const [editing, setEditing] = useState<ContactDraft | null>(null);
  const [editingOrg, setEditingOrg] = useState<YellowOrg | null>(null);
  const [importing, setImporting] = useState<string | null>(null);
  const picker = useRef<HTMLInputElement>(null);
  const yellow = useQuery({ queryKey: ["yellowpages"], queryFn: () => api.get<YellowPages>("/api/yellowpages") });
  const categories = useMemo(() => [...new Set(yellow.data?.orgs.map((o) => o.category))], [yellow.data]);

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">通讯录</h1>
          <p className="mt-0.5 text-sm text-muted">短信和来电会显示联系人的名字；不在通讯录里的号码会去内置黄页里查</p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button icon={Upload} onClick={() => picker.current?.click()}>
            导入
          </Button>
          {/* The server answers with a file to save, so the page stays. */}
          <Button icon={Download} onClick={() => (window.location.href = tab === "mine" ? "/api/contacts/export" : "/api/yellowpages/export")}>
            导出
          </Button>
          {tab === "mine" ? (
            <Button variant="primary" icon={Plus} onClick={() => setEditing({ name: "", number: "", note: "" })}>
              新建联系人
            </Button>
          ) : (
            <Button variant="primary" icon={Plus} onClick={() => setEditingOrg({ name: "", category: "", numbers: [] })}>
              新建机构
            </Button>
          )}
        </div>
      </div>
      <Card className="min-h-96">
        <div className="flex flex-wrap items-center gap-3 border-b border-line px-4 py-3">
          <Segmented
            size="sm"
            value={tab}
            onChange={setTab}
            options={[
              { value: "mine", label: "我的联系人", icon: BookUser },
              { value: "yellow", label: "黄页", icon: BookOpen },
            ]}
          />
          <div className="relative min-w-48 flex-1">
            <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-subtle" />
            <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder={tab === "mine" ? "搜索名字、号码或备注" : "搜索机构、分类或号码"} className="pl-9" />
            {q && <IconButton icon={X} label="清除" onClick={() => setQ("")} className="absolute top-1/2 right-1 -translate-y-1/2" />}
          </div>
        </div>
        <CardBody className="pt-2">{tab === "mine" ? <MyContacts q={q} onEdit={setEditing} /> : <Yellow q={q} pages={yellow} onSave={setEditing} onEdit={setEditingOrg} />}</CardBody>
      </Card>
      {editing && <ContactModal initial={editing} onClose={() => setEditing(null)} />}
      {editingOrg && <OrgModal initial={editingOrg} categories={categories} onClose={() => setEditingOrg(null)} />}
      {importing !== null && <ImportModal kind={tab} text={importing} onClose={() => setImporting(null)} />}
      <input
        ref={picker}
        type="file"
        accept={tab === "mine" ? ".vcf,text/vcard,text/x-vcard" : ".json,application/json"}
        hidden
        onChange={async (e) => {
          const file = e.target.files?.[0];
          e.target.value = "";
          if (file) setImporting(await file.text());
        }}
      />
    </div>
  );
}

// match compares loosely: case, spaces and dashes are ignored.
const fold = (s: string) => s.toLowerCase().replace(/[\s()-]/g, "");
const matches = (q: string, ...fields: string[]) => !q || fields.some((f) => fold(f).includes(fold(q)));

function useActions() {
  const navigate = useNavigate();
  const { data: status } = useStatus();
  const devices = knownDevices(status);
  const device = devices.find((d) => sendState(d).ok) ?? devices[0];
  return {
    call: (n: string) => navigate(`/calls?to=${encodeURIComponent(n.replace(/[^\d+*#]/g, ""))}${device ? `&d=${encodeURIComponent(device.id)}` : ""}`),
    message: device ? (n: string) => navigate(`/messages?d=${encodeURIComponent(device.id)}&peer=${encodeURIComponent(n.replace(/[\s()-]/g, ""))}`) : undefined,
  };
}

function MyContacts({ q, onEdit }: { q: string; onEdit: (c: ContactDraft) => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const actions = useActions();
  const [confirm, setConfirm] = useState<Contact | null>(null);
  const list = useQuery({ queryKey: ["contacts"], queryFn: () => api.get<{ items: Contact[] }>("/api/contacts") });
  const items = (list.data?.items ?? []).filter((c) => matches(q, c.name, c.number, c.note));

  if (list.isPending) return <Spinner />;
  if (!list.data?.items.length)
    return (
      <Empty icon={BookUser} title="通讯录还是空的">
        点右上角新建联系人，或在短信、通话记录里把号码加入通讯录。
      </Empty>
    );
  if (!items.length) return <Empty icon={Search} title="没有找到联系人" />;
  return (
    <div className="-mx-2">
      {items.map((c) => (
        <div key={c.id} className="group flex items-center gap-3 rounded-2xl px-3 py-2.5 transition hover:bg-panel-2">
          {c.avatar ? <PeerAvatar src={c.avatar} /> : <div className="bg-gradient-accent flex size-10 shrink-0 items-center justify-center rounded-2xl text-[15px] font-semibold text-white">{[...c.name][0]}</div>}
          <div className="min-w-0 flex-1">
            <p className="truncate text-sm font-medium">{c.name}</p>
            <p className="truncate text-xs text-muted">
              <span className="font-mono">{c.number}</span>
              {c.note && <span className="text-subtle"> · {c.note}</span>}
            </p>
          </div>
          <div className="flex items-center gap-0.5 opacity-70 transition group-hover:opacity-100">
            <IconButton icon={Phone} label="打电话" onClick={() => actions.call(c.number)} />
            {actions.message && <IconButton icon={MessageSquare} label="发短信" onClick={() => actions.message!(c.number)} />}
            <IconButton icon={Pencil} label="编辑" onClick={() => onEdit(c)} />
            <IconButton icon={Trash2} label="删除" onClick={() => setConfirm(c)} className="hover:text-bad" />
          </div>
        </div>
      ))}
      <ConfirmModal
        open={!!confirm}
        onClose={() => setConfirm(null)}
        danger
        title={`删除联系人「${confirm?.name}」？`}
        description="短信和通话记录不会删除，只是不再显示这个名字。"
        confirmLabel="删除"
        onConfirm={async () => {
          try {
            await api.del(`/api/contacts/${confirm!.id}`);
            qc.invalidateQueries();
          } catch (e) {
            toast.error((e as Error).message);
          }
          setConfirm(null);
        }}
      />
    </div>
  );
}

function Yellow({ q, pages, onSave, onEdit }: { q: string; pages: UseQueryResult<YellowPages>; onSave: (c: ContactDraft) => void; onEdit: (o: YellowOrg) => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const actions = useActions();
  const [confirm, setConfirm] = useState<{ org: YellowOrg; restore: boolean } | null>(null);
  const groups = useMemo(() => {
    const out = new Map<string, YellowOrg[]>();
    for (const o of pages.data?.orgs ?? []) {
      if (!matches(q, o.name, o.category, ...o.numbers.map((n) => n.number))) continue;
      out.set(o.category, [...(out.get(o.category) ?? []), o]);
    }
    return [...out];
  }, [pages.data, q]);

  const run = async (p: Promise<unknown>, done: string) => {
    try {
      await p;
      toast.ok(done);
      qc.invalidateQueries();
    } catch (e) {
      toast.error((e as Error).message);
    }
  };

  if (pages.isPending) return <Spinner />;
  const hidden = pages.data?.hidden.length ?? 0;
  return (
    <div className="space-y-5">
      {groups.length === 0 && <Empty icon={Search} title="黄页里没有找到" />}
      {groups.map(([category, orgs]) => (
        <section key={category}>
          <h3 className="mb-1 px-1 text-xs font-medium text-subtle">
            {category} · {orgs.length}
          </h3>
          <div className="-mx-2">
            {orgs.map((o) => (
              <div key={o.builtin ?? `own-${o.id}`} className="group flex flex-wrap items-center gap-x-3 gap-y-1.5 rounded-2xl px-3 py-2 transition hover:bg-panel-2">
                <span className="flex min-w-36 flex-1 items-center gap-3">
                  {o.logo ? <PeerAvatar src={o.logo} size="sm" /> : <span className="size-8 shrink-0 rounded-xl bg-panel-2 ring-1 ring-line" />}
                  <span className="truncate text-sm font-medium">{o.name}</span>
                  {o.edited && <Badge>已修改</Badge>}
                  {!o.builtin && <Badge tone="info">自建</Badge>}
                </span>
                <div className="flex flex-wrap gap-1.5">
                  {o.numbers.map((n) => (
                    <span key={n.number} className="group/n inline-flex items-center rounded-lg bg-panel-2 text-xs ring-1 ring-line">
                      <button type="button" title="打电话" onClick={() => actions.call(n.number)} className="px-2 py-1 font-mono hover:text-accent">
                        {n.number}
                        {n.label && <span className="ml-1 font-sans text-subtle">{n.label}</span>}
                      </button>
                      <button type="button" title="加入我的联系人，可以改名字" onClick={() => onSave({ name: n.label ? `${o.name} ${n.label}` : o.name, number: n.number, note: "", avatar: o.logo })} className="border-l border-line px-1.5 py-1 text-subtle hover:text-accent">
                        <Plus className="size-3" />
                      </button>
                    </span>
                  ))}
                </div>
                <div className="flex items-center gap-0.5 opacity-0 transition group-hover:opacity-100 focus-within:opacity-100">
                  <IconButton icon={Pencil} label="编辑" onClick={() => onEdit(o)} />
                  {o.edited && <IconButton icon={RotateCcw} label="恢复内置" onClick={() => setConfirm({ org: o, restore: true })} />}
                  <IconButton icon={Trash2} label="删除" onClick={() => setConfirm({ org: o, restore: false })} className="hover:text-bad" />
                </div>
              </div>
            ))}
          </div>
        </section>
      ))}
      {pages.data && (
        <p className="px-1 text-[11px] text-subtle">
          {hidden > 0 && (
            <>
              已删除 {hidden} 个内置机构，
              <button type="button" className="underline hover:text-muted" onClick={() => run(api.post("/api/yellowpages/unhide"), "已恢复")}>
                全部恢复
              </button>
              。{" "}
            </>
          )}
          黄页数据来自{" "}
          <a href={pages.data.source.split("@")[0]} target="_blank" rel="noreferrer" className="underline hover:text-muted">
            metowolf/vCards
          </a>
          （MIT 许可），随号小助版本更新；你的修改单独保存，不会被更新覆盖。
        </p>
      )}
      <ConfirmModal
        open={!!confirm}
        onClose={() => setConfirm(null)}
        danger={!confirm?.restore}
        title={confirm?.restore ? `把「${confirm.org.name}」恢复成内置的样子？` : `从黄页删除「${confirm?.org.name}」？`}
        description={confirm?.restore ? "你对名称、分类、号码和 logo 的修改都会丢掉。" : confirm?.org.builtin ? "这些号码不再显示机构名称。可以在列表底部恢复。" : "这些号码不再显示机构名称。"}
        confirmLabel={confirm?.restore ? "恢复" : "删除"}
        onConfirm={() => {
          const o = confirm!.org;
          setConfirm(null);
          void run(o.builtin && !confirm!.restore ? api.post("/api/yellowpages/hide", { builtin: o.builtin }) : api.del(`/api/yellowpages/orgs/${o.id}`), confirm!.restore ? "已恢复" : "已删除");
        }}
      />
    </div>
  );
}

// ImportModal confirms an import of a picked file: a vCard file into the
// contacts, or an exported yellow pages file.
function ImportModal({ kind, text, onClose }: { kind: Tab; text: string; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [overwrite, setOverwrite] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const yellow = useMemo(() => {
    if (kind !== "yellow") return null;
    try {
      return JSON.parse(text) as { orgs?: unknown[] };
    } catch {
      return {};
    }
  }, [kind, text]);
  const count = kind === "mine" ? (text.match(/^BEGIN:VCARD/gim) ?? []).length : (yellow?.orgs?.length ?? 0);

  const start = async () => {
    setBusy(true);
    setError("");
    try {
      const r = await (kind === "mine" ? api.post<ImportResult>("/api/contacts/import", { vcf: text, overwrite }) : api.post<ImportResult>("/api/yellowpages/import", yellow));
      toast.ok(`新增 ${r.added}，更新 ${r.updated}${r.skipped ? `，跳过 ${r.skipped}` : ""}`);
      qc.invalidateQueries();
      onClose();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      open
      onClose={onClose}
      title={kind === "mine" ? "导入联系人" : "导入黄页"}
      description={
        kind === "mine"
          ? "支持 iPhone、安卓和 Google 通讯录导出的 vCard（.vcf）文件。一个人有多个号码时，会按号码分成多个联系人。"
          : "导入号小助导出的黄页文件：文件里的机构会改成文件里的样子，文件里没有的机构保持不变。"
      }
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            取消
          </Button>
          <Button variant="primary" loading={busy} disabled={!count} onClick={start} data-autofocus>
            导入
          </Button>
        </>
      }
    >
      <p className="text-sm">{count ? `文件里有 ${count} 个${kind === "mine" ? "联系人" : "机构"}。` : kind === "mine" ? "文件里没有找到联系人，请选择 .vcf 文件。" : "这不是号小助导出的黄页文件。"}</p>
      {kind === "mine" && count > 0 && <Switch checked={overwrite} onChange={setOverwrite} label="号码已在通讯录里时，用文件里的名字、备注和头像覆盖" />}
      {error && <p className="text-sm text-bad">{error}</p>}
    </Modal>
  );
}
