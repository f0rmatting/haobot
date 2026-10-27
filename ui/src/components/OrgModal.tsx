import { useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { ImagePlus, Plus, X } from "lucide-react";
import { api, type YellowNumber, type YellowOrg } from "../lib/api";
import { squareJPEG } from "./ContactModal";
import { Button, Field, IconButton, Input, Modal, PeerAvatar, useToast } from "./ui";

// OrgModal edits an organisation of the yellow pages: a built-in one (the
// change is kept apart and can be undone) or one of the user's own.
export function OrgModal({ initial, categories, onClose }: { initial: YellowOrg; categories: string[]; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [name, setName] = useState(initial.name);
  const [category, setCategory] = useState(initial.category);
  const [numbers, setNumbers] = useState<YellowNumber[]>(initial.numbers.length ? initial.numbers : [{ number: "" }]);
  // logo is undefined while unchanged, a data: URL for a new one and "" to
  // drop the user's (a built-in organisation then shows its own again).
  const [logo, setLogo] = useState<string | undefined>(undefined);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const picker = useRef<HTMLInputElement>(null);
  const shown = logo === "" ? undefined : (logo ?? initial.logo);
  const isNew = !initial.id && !initial.builtin;

  const setNumber = (i: number, patch: Partial<YellowNumber>) => setNumbers(numbers.map((n, j) => (j === i ? { ...n, ...patch } : n)));

  const pick = async (file: File | undefined) => {
    if (!file) return;
    try {
      setLogo(await squareJPEG(file));
    } catch {
      toast.error("这张图片打不开，换一张试试");
    }
  };

  const save = async () => {
    setBusy(true);
    setError("");
    try {
      await api.post("/api/yellowpages/orgs", {
        id: initial.id,
        builtin: initial.builtin,
        name,
        category,
        numbers: numbers.filter((n) => n.number.trim()),
        ...(logo !== undefined && { logo }),
      });
      toast.ok(isNew ? "已加入黄页" : "已保存");
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
      title={isNew ? "新建黄页机构" : `编辑「${initial.name}」`}
      description={initial.builtin ? "修改只保存在这台号小助上，随时可以恢复成内置的样子。" : "不在通讯录里的号码会用黄页里的名字和 logo 显示，短信签名也会按机构名称匹配 logo。"}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            取消
          </Button>
          <Button variant="primary" loading={busy} disabled={!name.trim()} onClick={save}>
            保存
          </Button>
        </>
      }
    >
      <div className="flex items-center gap-4">
        <button type="button" onClick={() => picker.current?.click()} className="group relative rounded-2xl outline-none focus-visible:ring-2 focus-visible:ring-accent/60" aria-label="选择 logo">
          {shown ? (
            <PeerAvatar src={shown} size="xl" />
          ) : (
            <span className="flex size-14 items-center justify-center rounded-2xl bg-panel-2 text-subtle ring-1 ring-line group-hover:text-accent">
              <ImagePlus className="size-6" />
            </span>
          )}
        </button>
        <div className="min-w-0 flex-1 text-sm">
          <div className="flex items-center gap-1">
            <Button size="sm" variant="secondary" onClick={() => picker.current?.click()}>
              {shown ? "换一张" : "上传 logo"}
            </Button>
            {(logo || (logo === undefined && initial.logo && !initial.logo.startsWith("/api/yellowpages/logos/"))) && <IconButton icon={X} label={initial.builtin ? "用回内置 logo" : "移除 logo"} onClick={() => setLogo("")} />}
          </div>
          <p className="mt-1 text-xs text-subtle">可选，会裁成正方形</p>
        </div>
      </div>
      <Field label="名称" error={error} hint="短信开头或结尾的【签名】和名称一致时，也会显示这个 logo">
        <Input value={name} onChange={(e) => setName(e.target.value)} maxLength={64} placeholder="例如：招商银行、小区物业" />
      </Field>
      <Field label="分类">
        <Input value={category} onChange={(e) => setCategory(e.target.value)} maxLength={32} list="yellow-categories" placeholder="其它" />
        <datalist id="yellow-categories">
          {categories.map((c) => (
            <option key={c} value={c} />
          ))}
        </datalist>
      </Field>
      <Field label="号码">
        <div className="space-y-2">
          {numbers.map((n, i) => (
            <div key={i} className="flex items-center gap-2">
              <Input value={n.number} onChange={(e) => setNumber(i, { number: e.target.value })} maxLength={32} inputMode="tel" placeholder="95555、1069…" className="flex-[3] font-mono" />
              <Input value={n.label ?? ""} onChange={(e) => setNumber(i, { label: e.target.value })} maxLength={32} placeholder="说明，可选" className="flex-[2]" />
              <IconButton icon={X} label="删除号码" onClick={() => setNumbers(numbers.filter((_, j) => j !== i))} />
            </div>
          ))}
          <Button size="sm" variant="ghost" icon={Plus} onClick={() => setNumbers([...numbers, { number: "" }])} disabled={numbers.length >= 50}>
            添加号码
          </Button>
        </div>
      </Field>
      {/* Last, so the modal focuses the name field rather than this. */}
      <input
        ref={picker}
        type="file"
        accept="image/*"
        hidden
        onChange={(e) => {
          void pick(e.target.files?.[0]);
          e.target.value = "";
        }}
      />
    </Modal>
  );
}
