import { useState } from "react";
import { AlertTriangle } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../lib/api";
import { Alert, Button, Field, Modal, PinInput, Switch, useToast } from "./ui";

export type SimDialog = "pin" | "puk" | "lock-on" | "lock-off" | "change" | null;

interface Props {
  deviceId: string;
  dialog: SimDialog;
  onClose: () => void;
  pinRetries: number;
  pukRetries: number;
}

// SimDialogs hosts every SIM PIN flow. A request that would spend the last
// PIN attempt comes back as 409 last_attempt and must be confirmed again.
export function SimDialogs({ deviceId, dialog, onClose, pinRetries, pukRetries }: Props) {
  const qc = useQueryClient();
  const toast = useToast();
  const [pin, setPin] = useState("");
  const [newPin, setNewPin] = useState("");
  const [confirmPin, setConfirmPin] = useState("");
  const [puk, setPuk] = useState("");
  const [save, setSave] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [lastAttempt, setLastAttempt] = useState(false);

  const reset = () => {
    setPin("");
    setNewPin("");
    setConfirmPin("");
    setPuk("");
    setError("");
    setLastAttempt(false);
    setBusy(false);
  };
  const close = () => {
    reset();
    onClose();
  };

  const run = async (path: string, body: Record<string, unknown>, success: string) => {
    setBusy(true);
    setError("");
    try {
      await api.post(`/api/devices/${encodeURIComponent(deviceId)}${path}`, { ...body, force: lastAttempt });
      toast.ok(success);
      qc.invalidateQueries({ queryKey: ["status"] });
      qc.invalidateQueries({ queryKey: ["sim-settings", deviceId] });
      close();
    } catch (err) {
      if (err instanceof ApiError && err.code === "last_attempt") {
        setLastAttempt(true);
        setError("");
      } else {
        setError(err instanceof Error ? err.message : "操作失败");
      }
      qc.invalidateQueries({ queryKey: ["status"] });
    } finally {
      setBusy(false);
    }
  };

  const retriesNote = pinRetries >= 0 && (
    <p className={pinRetries <= 1 ? "text-xs font-medium text-bad" : "text-xs text-muted"}>
      剩余尝试次数：{pinRetries} 次{pinRetries <= 1 && "，输错将锁卡"}
    </p>
  );

  const lastAttemptWarning = lastAttempt && (
    <Alert tone="bad" icon={AlertTriangle} title="这是最后一次机会">
      再输错一次 SIM 卡就会被锁定，只能用 PUK 码解锁（PUK 码印在 SIM 卡卡托上，或向运营商查询）。确认 PIN 无误后再点一次提交。
    </Alert>
  );

  const saveToggle = (
    <div className="flex items-center justify-between gap-4 rounded-xl bg-panel-2 px-3.5 py-3 ring-1 ring-line">
      <div>
        <p className="text-[13px] font-medium">保存 PIN，开机自动解锁</p>
        <p className="text-xs text-muted">PIN 会加密保存在本机，不会显示在页面上。</p>
      </div>
      <Switch checked={save} onChange={setSave} label="保存 PIN" />
    </div>
  );

  const footer = (submit: () => void, disabled: boolean, label: string, danger = false) => (
    <>
      <Button variant="ghost" onClick={close}>
        取消
      </Button>
      <Button variant={danger || lastAttempt ? "danger" : "primary"} onClick={submit} loading={busy} disabled={disabled}>
        {lastAttempt ? "我确认，继续提交" : label}
      </Button>
    </>
  );

  const validPin = (p: string) => p.length >= 4 && p.length <= 8;

  switch (dialog) {
    case "pin":
      return (
        <Modal open onClose={close} title="解锁 SIM 卡" description="SIM 卡开启了 PIN 锁，输入 PIN 后才能收短信。" footer={footer(() => run("/sim/pin", { pin, save }, "SIM 卡已解锁"), !validPin(pin), "解锁")}>
          <Field label="PIN 码" hint="4 到 8 位数字。不知道的话，初始 PIN 一般是 1234 或 0000，但不要盲目尝试。">
            <PinInput value={pin} onChange={setPin} placeholder="••••" />
          </Field>
          {retriesNote}
          {saveToggle}
          {lastAttemptWarning}
          {error && <p className="text-sm text-bad">{error}</p>}
        </Modal>
      );
    case "puk":
      return (
        <Modal
          open
          onClose={close}
          tone="danger"
          title="用 PUK 解锁"
          description="PIN 连续输错 3 次后 SIM 卡会被锁定。输入 PUK 码并设置一个新 PIN 即可解锁。"
          footer={footer(() => run("/sim/puk", { puk, newPin, save }, "SIM 卡已解锁，新 PIN 已生效"), puk.length !== 8 || !validPin(newPin) || newPin !== confirmPin, "解锁并设置新 PIN", true)}
        >
          <Alert tone="bad" icon={AlertTriangle} title={`PUK 剩余 ${pukRetries >= 0 ? pukRetries : "?"} 次`}>
            PUK 输错 10 次后 SIM 卡将永久报废，只能去营业厅补卡。请向运营商确认 PUK 码后再输入。
          </Alert>
          <Field label="PUK 码" hint="8 位数字">
            <PinInput value={puk} onChange={setPuk} maxLength={8} placeholder="••••••••" />
          </Field>
          <div className="grid grid-cols-2 gap-3">
            <Field label="新 PIN">
              <PinInput value={newPin} onChange={setNewPin} />
            </Field>
            <Field label="再输入一次" error={confirmPin && confirmPin !== newPin ? "两次不一致" : undefined}>
              <PinInput value={confirmPin} onChange={setConfirmPin} />
            </Field>
          </div>
          {saveToggle}
          {error && <p className="text-sm text-bad">{error}</p>}
        </Modal>
      );
    case "lock-on":
    case "lock-off": {
      const enabling = dialog === "lock-on";
      return (
        <Modal
          open
          onClose={close}
          title={enabling ? "开启 PIN 锁" : "关闭 PIN 锁"}
          description={enabling ? "开启后，SIM 卡每次上电都需要输入 PIN，可防止卡被拔走后滥用。" : "关闭后，SIM 卡上电即可使用，不再需要 PIN。"}
          footer={footer(() => run("/sim/lock", { enabled: enabling, pin, save: enabling && save }, enabling ? "PIN 锁已开启" : "PIN 锁已关闭"), !validPin(pin), enabling ? "开启" : "关闭")}
        >
          <Field label="当前 PIN 码">
            <PinInput value={pin} onChange={setPin} />
          </Field>
          {retriesNote}
          {enabling && saveToggle}
          {lastAttemptWarning}
          {error && <p className="text-sm text-bad">{error}</p>}
        </Modal>
      );
    }
    case "change":
      return (
        <Modal
          open
          onClose={close}
          title="修改 PIN 码"
          description="需要先开启 PIN 锁。已保存的 PIN 会同步更新。"
          footer={footer(() => run("/sim/change-pin", { oldPin: pin, newPin, save }, "PIN 码已修改"), !validPin(pin) || !validPin(newPin) || newPin !== confirmPin, "修改")}
        >
          <Field label="当前 PIN">
            <PinInput value={pin} onChange={setPin} />
          </Field>
          <div className="grid grid-cols-2 gap-3">
            <Field label="新 PIN">
              <PinInput value={newPin} onChange={setNewPin} />
            </Field>
            <Field label="再输入一次" error={confirmPin && confirmPin !== newPin ? "两次不一致" : undefined}>
              <PinInput value={confirmPin} onChange={setConfirmPin} />
            </Field>
          </div>
          {retriesNote}
          {saveToggle}
          {lastAttemptWarning}
          {error && <p className="text-sm text-bad">{error}</p>}
        </Modal>
      );
    default:
      return null;
  }
}
