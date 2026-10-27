import { useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { ImagePlus, X } from "lucide-react";
import { api, type Contact } from "../lib/api";
import { Button, Field, IconButton, Input, Modal, PeerAvatar, Textarea, useToast } from "./ui";

export type ContactDraft = Pick<Contact, "name" | "number" | "note" | "avatar"> & { id?: number };

// AVATAR_PX is the side of the square JPEG a picked photo becomes: sharp at
// the 56px it is shown at, and a few KB, well under the server's limit.
const AVATAR_PX = 192;

// squareJPEG crops a picture to its centre square and shrinks it.
export async function squareJPEG(file: File): Promise<string> {
  const bitmap = await createImageBitmap(file);
  const side = Math.min(bitmap.width, bitmap.height);
  const canvas = document.createElement("canvas");
  canvas.width = canvas.height = AVATAR_PX;
  const ctx = canvas.getContext("2d")!;
  // JPEG has no transparency; a transparent PNG gets a white background.
  ctx.fillStyle = "#fff";
  ctx.fillRect(0, 0, AVATAR_PX, AVATAR_PX);
  ctx.imageSmoothingQuality = "high";
  ctx.drawImage(bitmap, (bitmap.width - side) / 2, (bitmap.height - side) / 2, side, side, 0, 0, AVATAR_PX, AVATAR_PX);
  bitmap.close();
  return canvas.toDataURL("image/jpeg", 0.85);
}

// ContactModal adds a contact to the private phone book, or edits one.
// Names show up across messages, calls and notifications, so every list is
// refreshed after a save.
export function ContactModal({ initial, onClose }: { initial: ContactDraft; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [draft, setDraft] = useState(initial);
  // avatar is undefined while the picture is unchanged, a data: URL for a
  // new one and "" to remove it.
  const [avatar, setAvatar] = useState<string | undefined>(undefined);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const picker = useRef<HTMLInputElement>(null);
  const set = (k: "name" | "number" | "note") => (e: { target: { value: string } }) => setDraft({ ...draft, [k]: e.target.value });
  const shown = avatar ?? draft.avatar;

  const pick = async (file: File | undefined) => {
    if (!file) return;
    try {
      setAvatar(await squareJPEG(file));
    } catch {
      toast.error("这张图片打不开，换一张试试");
    }
  };

  const save = async () => {
    setBusy(true);
    setError("");
    try {
      const body = { name: draft.name, number: draft.number, note: draft.note, ...(avatar !== undefined && { avatar }) };
      await (draft.id ? api.put(`/api/contacts/${draft.id}`, body) : api.post("/api/contacts", body));
      toast.ok(draft.id ? "联系人已保存" : "已加入通讯录");
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
      title={draft.id ? "编辑联系人" : "加入通讯录"}
      description="通讯录只保存在这台号小助上。短信、通话记录和推送通知里会显示这个名字。"
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            取消
          </Button>
          <Button variant="primary" loading={busy} disabled={!draft.name.trim() || !draft.number.trim()} onClick={save}>
            保存
          </Button>
        </>
      }
    >
      <div className="flex items-center gap-4">
        <button type="button" onClick={() => picker.current?.click()} className="group relative rounded-2xl outline-none focus-visible:ring-2 focus-visible:ring-accent/60" aria-label="选择头像">
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
              {shown ? "换一张" : "上传头像"}
            </Button>
            {shown && <IconButton icon={X} label="移除头像" onClick={() => setAvatar("")} />}
          </div>
          <p className="mt-1 text-xs text-subtle">可选，会裁成正方形</p>
        </div>
      </div>
      <Field label="名字" error={error}>
        <Input value={draft.name} onChange={set("name")} maxLength={64} placeholder="例如：妈妈、房东王先生" onKeyDown={(e) => e.key === "Enter" && draft.name.trim() && draft.number.trim() && save()} />
      </Field>
      <Field label="号码">
        <Input value={draft.number} onChange={set("number")} maxLength={32} inputMode="tel" placeholder="手机号、座机或服务号码" className="font-mono" />
      </Field>
      <Field label="备注">
        <Textarea value={draft.note} onChange={set("note")} maxLength={500} rows={2} placeholder="可选" />
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
