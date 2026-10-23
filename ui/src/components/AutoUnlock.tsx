import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ShieldCheck, Trash2 } from "lucide-react";
import { api, type DeviceStatus, type SIMSettings } from "../lib/api";
import { Alert, Button, Field, PinInput, Spinner, Switch, useToast } from "./ui";

// AutoUnlock edits one device's saved SIM PIN and automatic unlocking.
export function AutoUnlock({ d }: { d: DeviceStatus }) {
  const toast = useToast();
  const qc = useQueryClient();
  const key = ["sim-settings", d.id];
  const { data: sim } = useQuery({ queryKey: key, queryFn: () => api.get<SIMSettings>(`/api/devices/${encodeURIComponent(d.id)}/sim-settings`) });
  const [pin, setPin] = useState("");
  const [busy, setBusy] = useState(false);

  const save = async (body: { autoUnlock: boolean; pin?: string | null }, done: string) => {
    setBusy(true);
    try {
      const next = await api.put<SIMSettings>(`/api/devices/${encodeURIComponent(d.id)}/sim-settings`, body);
      qc.setQueryData(key, next);
      qc.invalidateQueries({ queryKey: ["status"] });
      toast.ok(`${d.name}：${done}`);
      setPin("");
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  if (!sim) return <Spinner />;
  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between gap-4 rounded-xl bg-panel-2 px-3.5 py-3 ring-1 ring-line">
        <div>
          <p className="text-sm font-medium">开机自动解锁</p>
          <p className="text-xs text-muted">{sim.hasPin ? "已保存 PIN（加密存储，不会显示）" : "还没有保存 PIN"}</p>
        </div>
        <Switch checked={sim.autoUnlock} disabled={busy || (!sim.hasPin && !sim.autoUnlock)} onChange={(v) => save({ autoUnlock: v }, v ? "已开启自动解锁" : "已关闭自动解锁")} label={`${d.name} 自动解锁`} />
      </div>
      <Field label={sim.hasPin ? "更换保存的 PIN" : "保存 PIN"} hint="这里只保存，不会立刻发送给 SIM 卡。">
        <div className="flex gap-2">
          <PinInput value={pin} onChange={setPin} placeholder="4～8 位数字" />
          <Button variant="primary" disabled={pin.length < 4 || busy} onClick={() => save({ autoUnlock: true, pin }, "PIN 已保存，自动解锁已开启")}>
            保存
          </Button>
          {sim.hasPin && (
            <Button variant="ghost" icon={Trash2} disabled={busy} onClick={() => save({ autoUnlock: false, pin: "" }, "已删除保存的 PIN")} aria-label="删除保存的 PIN" />
          )}
        </div>
      </Field>
      {d.pinBlocked && (
        <Alert tone="warn" title="自动解锁当前已暂停">
          上次自动输入失败或剩余次数不足。保存新的 PIN 或手动解锁后会恢复。
        </Alert>
      )}
    </div>
  );
}

export function PinProtectionNote() {
  return (
    <div className="space-y-1 rounded-xl bg-ok/8 px-3.5 py-3 text-xs leading-relaxed text-muted ring-1 ring-ok/20">
      <p className="flex items-center gap-1.5 font-medium text-ok">
        <ShieldCheck className="size-3.5" /> 防锁卡保护
      </p>
      <p>· 只有 PIN 剩余次数为满额 3 次时才会自动尝试；失败一次就停止，直到你手动解锁或更新 PIN。</p>
      <p>· 命令行参数 -sim-pin-file / -sim-pin-keychain 或环境变量 HAOBOT_SIM_PIN 对所有设备生效，并优先于这里的设置。</p>
    </div>
  );
}
