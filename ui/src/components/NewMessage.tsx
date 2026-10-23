import clsx from "clsx";
import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Send } from "lucide-react";
import { api, type Message, type Status } from "../lib/api";
import { deviceHealth, knownDevices, sendState } from "../lib/devices";
import { smsParts } from "../lib/sms";
import { Button, Dot, Field, Input, Modal, Select, Textarea } from "./ui";

// NewMessageModal starts a conversation: pick the device (SIM) to send from,
// the number, and the text.
export function NewMessageModal({ status, defaultDevice, onClose, onSent }: { status?: Status; defaultDevice: string; onClose: () => void; onSent: (m: Message) => void }) {
  const qc = useQueryClient();
  const devices = knownDevices(status);
  const firstReady = devices.find((d) => sendState(d).ok)?.id ?? devices[0]?.id ?? "";
  const [deviceId, setDeviceId] = useState(devices.some((d) => d.id === defaultDevice) ? defaultDevice : firstReady);
  const [to, setTo] = useState("");
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const device = devices.find((d) => d.id === deviceId);
  const can = sendState(device);
  const est = smsParts(text);
  const validNumber = /^\+?[0-9 -]{3,24}$/.test(to.trim());

  const send = async () => {
    setBusy(true);
    setError("");
    try {
      const m = await api.post<Message>("/api/messages/send", { deviceId, to, text });
      qc.invalidateQueries({ queryKey: ["conversations"] });
      onSent(m);
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
      title="新短信"
      description="选择用哪张卡发送，填写对方号码。"
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            取消
          </Button>
          <Button variant="primary" icon={Send} loading={busy} disabled={!can.ok || !validNumber || !text.trim() || est.parts > 10} onClick={send}>
            发送
          </Button>
        </>
      }
    >
      {devices.length > 1 ? (
        <Field label="发送设备" error={device && !can.ok ? can.reason : undefined}>
          <Select value={deviceId} onChange={(e) => setDeviceId(e.target.value)}>
            {devices.map((d) => (
              <option key={d.id} value={d.id}>
                {d.name}
                {d.identity.number ? `（${d.identity.number}）` : ""} · {deviceHealth(d).label}
              </option>
            ))}
          </Select>
        </Field>
      ) : (
        device && (
          <div className="flex items-center gap-2 rounded-xl bg-panel-2 px-3 py-2 text-[13px] ring-1 ring-line">
            <Dot tone={deviceHealth(device).tone} />
            <span className="font-medium">{device.name}</span>
            <span className="text-muted">{device.identity.number}</span>
            {!can.ok && <span className="ml-auto text-xs text-bad">{can.reason}</span>}
          </div>
        )
      )}
      <Field label="对方号码" hint="国内手机号直接填写；国际号码以 + 和国家码开头。">
        <Input value={to} onChange={(e) => setTo(e.target.value)} inputMode="tel" placeholder="13800138000" className="font-mono" autoComplete="off" />
      </Field>
      <Field label="内容">
        <Textarea value={text} onChange={(e) => setText(e.target.value)} rows={4} placeholder="输入短信内容" />
        {text && (
          <p className={clsx("mt-1 text-[11px]", est.parts > 10 ? "text-bad" : "text-subtle")}>
            {est.chars} 字 · {est.encoding} · {est.parts > 10 ? "超过 10 条上限" : `将分 ${est.parts} 条发送`}
          </p>
        )}
      </Field>
      {error && <p className="text-sm text-bad">{error}</p>}
    </Modal>
  );
}
