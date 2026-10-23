import { useState } from "react";
import { PhoneForwarded, RefreshCw } from "lucide-react";
import { api, type DeviceStatus, type Forwarding } from "../lib/api";
import { Alert, Badge, Button, Card, CardBody, CardHeader, Input, Switch, useToast } from "./ui";

// ForwardingCard reads and sets call forwarding on the network. Queries go
// to the carrier and take a few seconds, so they only run on request.
export function ForwardingCard({ d }: { d: DeviceStatus }) {
  const toast = useToast();
  const [items, setItems] = useState<Forwarding[] | null>(null);
  const [numbers, setNumbers] = useState<Record<number, string>>({});
  const [busy, setBusy] = useState<number | "load" | null>(null);
  const [error, setError] = useState("");
  const base = `/api/devices/${encodeURIComponent(d.id)}/forwarding`;

  const load = async () => {
    setBusy("load");
    setError("");
    try {
      const r = await api.get<{ items: Forwarding[] }>(base);
      setItems(r.items);
      setNumbers(Object.fromEntries(r.items.map((f) => [f.reason, f.number])));
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(null);
    }
  };

  const save = async (f: Forwarding, enabled: boolean) => {
    setBusy(f.reason);
    try {
      await api.put(base, { reason: f.reason, enabled, number: numbers[f.reason] ?? "" });
      toast.ok(enabled ? `${f.name}已设置` : `${f.name}已关闭`);
      await load();
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(null);
    }
  };

  return (
    <Card>
      <CardHeader
        icon={PhoneForwarded}
        title="呼叫转移"
        subtitle="让运营商把打到这张卡的电话转到别的号码，不需要网页在线"
        action={
          <Button size="sm" icon={RefreshCw} loading={busy === "load"} disabled={!d.connected} onClick={load}>
            {items ? "刷新" : "查询"}
          </Button>
        }
      />
      <CardBody className="space-y-3 pt-3">
        {error && (
          <Alert tone="warn" title="查询失败">
            {error}。部分运营商的 VoLTE 网络不支持用 AT 指令设置呼叫转移，可以拨打运营商客服或用手机设置（如 **21*号码#）。
          </Alert>
        )}
        {!items && !error && <p className="text-[13px] text-muted">点击“查询”向运营商读取当前设置，大约需要几秒钟。</p>}
        {items?.map((f) => (
          <div key={f.reason} className="flex flex-wrap items-center gap-3 rounded-2xl bg-panel-2 px-4 py-3 ring-1 ring-line">
            <div className="w-28 shrink-0">
              <p className="text-sm font-medium">{f.name}</p>
              {f.enabled ? <Badge tone="ok">已开启</Badge> : <Badge>未开启</Badge>}
            </div>
            <Input value={numbers[f.reason] ?? ""} onChange={(e) => setNumbers({ ...numbers, [f.reason]: e.target.value })} placeholder="转到的号码" inputMode="tel" className="min-w-36 flex-1 font-mono" />
            <Switch checked={f.enabled} disabled={busy !== null || (!f.enabled && !(numbers[f.reason] ?? "").trim())} onChange={(v) => save(f, v)} label={f.name} />
          </div>
        ))}
      </CardBody>
    </Card>
  );
}
