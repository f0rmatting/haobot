import clsx from "clsx";
import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Activity, AlertTriangle, CheckCircle2, Clock, FlaskConical, HeartPulse, Loader2, RotateCcw, ShieldAlert, Wrench, XCircle } from "lucide-react";
import { api, type DeviceStatus, type Status } from "../lib/api";
import { ago, dateTime, type Tone } from "../lib/format";
import { knownDevices } from "../lib/devices";
import { Alert, Badge, Button, Card, CardBody, CardHeader, Field, Input, Modal, Switch, useToast } from "./ui";

const levelTone: Record<string, Tone> = { ok: "ok", warn: "warn", bad: "bad" };
const levelLabel: Record<string, string> = { ok: "健康", warn: "留意", bad: "异常" };

// HealthCard shows heartbeat, network watchdog, automatic recovery and the
// manual send-and-receive self-test of one device.
export function HealthCard({ d, status }: { d: DeviceStatus; status: Status }) {
  const h = d.health;
  const toast = useToast();
  const qc = useQueryClient();
  const [testing, setTesting] = useState(false);
  const [busy, setBusy] = useState(false);
  const live = d.online && d.connected;

  const setRecovery = async (enabled: boolean) => {
    setBusy(true);
    try {
      await api.put(`/api/devices/${encodeURIComponent(d.id)}/recovery`, { enabled });
      toast.ok(enabled ? "已开启自动恢复" : "已关闭自动恢复");
      qc.invalidateQueries({ queryKey: ["status"] });
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const latencyTone: Tone = !live ? "muted" : h.pingFailures > 0 ? "bad" : h.pingLatencyMs > 2000 ? "warn" : "ok";
  const st = h.selfTest;

  return (
    <Card glow={h.level !== "ok" && live}>
      <CardHeader
        icon={HeartPulse}
        title="健康状态"
        subtitle="每 15 秒检查一次模组是否响应，并自动处理卡死和掉网"
        action={<Badge tone={live ? levelTone[h.level] : "muted"}>{live ? levelLabel[h.level] : "离线"}</Badge>}
      />
      <CardBody className="space-y-4">
        <div className={clsx("flex items-center gap-3 rounded-2xl px-4 py-3 ring-1", h.level === "ok" ? "bg-ok/8 ring-ok/20" : h.level === "warn" ? "bg-warn/8 ring-warn/20" : "bg-bad/8 ring-bad/20")}>
          {h.level === "ok" ? <CheckCircle2 className="size-5 shrink-0 text-ok" /> : <AlertTriangle className={clsx("size-5 shrink-0", h.level === "warn" ? "text-warn" : "text-bad")} />}
          <p className="text-sm font-medium">{h.summary}</p>
        </div>

        {h.suspended && (
          <Alert
            tone="bad"
            icon={ShieldAlert}
            title="自动处理已暂停"
            action={
              <Button size="sm" variant="danger" loading={busy} onClick={() => setRecovery(true)}>
                恢复自动处理
              </Button>
            }
          >
            1 小时内已自动重启 3 次仍未恢复。为避免反复重启，程序只会继续尝试重新连接。请检查 SIM 卡是否欠费、天线和供电，处理后点击恢复。
          </Alert>
        )}

        <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
          <Metric icon={Activity} label="心跳响应" tone={latencyTone} value={live && h.lastPing ? `${h.pingLatencyMs} ms` : "—"} hint={h.lastPing ? `${ago(h.lastPing)}检查` : "尚未检查"} />
          <Metric icon={XCircle} label="连续未响应" tone={h.pingFailures ? "bad" : "ok"} value={`${h.pingFailures} 次`} hint="连续 3 次判定为卡死" />
          <Metric
            icon={Clock}
            label="网络"
            tone={h.unregisteredSince ? "warn" : live && d.modem.registered ? "ok" : "muted"}
            value={h.unregisteredSince ? `掉网 ${ago(h.unregisteredSince).replace("前", "")}` : live && d.modem.registered ? "已注册" : "—"}
            hint="掉网 10 分钟自动重新搜网"
          />
          <div className="rounded-2xl bg-panel-2 px-3.5 py-3 ring-1 ring-line">
            <div className="flex items-center justify-between gap-2">
              <span className="flex items-center gap-1.5 text-[11px] text-muted">
                <Wrench className="size-3.5" />
                自动恢复
              </span>
              <Switch checked={h.autoRecover} disabled={busy || !d.id} onChange={setRecovery} label="自动恢复" />
            </div>
            <p className="mt-1.5 text-[11px] leading-snug text-subtle">{h.autoRecover ? "卡死时逐级重连 → 重启 → USB 复位，每小时最多 3 次" : "只告警，不自动重启"}</p>
          </div>
        </div>

        <div className="rounded-2xl p-4 ring-1 ring-line">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div className="flex items-start gap-3">
              <FlaskConical className="mt-0.5 size-5 text-accent" />
              <div>
                <p className="text-sm font-medium">收发自检</p>
                <p className="text-xs text-muted">真的发一条短信并确认能收到，用来检查欠费、停机、短信中心异常等模组本身看不出的问题。</p>
              </div>
            </div>
            <Button size="sm" variant="primary" icon={FlaskConical} disabled={!live || st?.state === "running"} onClick={() => setTesting(true)}>
              立即自检
            </Button>
          </div>
          {st && (
            <div className="mt-3 flex items-start gap-2.5 rounded-xl bg-panel-2 px-3.5 py-2.5 text-[13px] ring-1 ring-line">
              {st.state === "running" ? <Loader2 className="mt-0.5 size-4 shrink-0 animate-spin text-accent" /> : st.state === "passed" ? <CheckCircle2 className="mt-0.5 size-4 shrink-0 text-ok" /> : <XCircle className="mt-0.5 size-4 shrink-0 text-bad" />}
              <div className="min-w-0">
                <p className="font-medium">{st.state === "running" ? `正在自检：已发送到 ${st.to}，等待收到（最多 3 分钟）…` : st.detail}</p>
                <p className="mt-0.5 text-xs text-subtle">
                  {dateTime(st.startedAt)} · 发往 {st.to}
                </p>
              </div>
            </div>
          )}
        </div>

        {h.recoveries.length > 0 && (
          <div>
            <p className="mb-2 flex items-center gap-1.5 text-[13px] font-medium">
              <RotateCcw className="size-4 text-subtle" />
              最近的自动恢复
            </p>
            <ol className="divide-y divide-line rounded-2xl ring-1 ring-line">
              {h.recoveries.map((r, i) => (
                <li key={i} className="flex items-start justify-between gap-3 px-4 py-2.5 text-[13px]">
                  <div className="min-w-0">
                    <span className="font-medium">{r.action}</span>
                    <span className="text-muted"> · {r.reason}</span>
                    {r.error && <p className="mt-0.5 text-xs text-bad">{r.error}</p>}
                  </div>
                  <span className="shrink-0 text-xs text-subtle" title={dateTime(r.at)}>
                    {ago(r.at)}
                  </span>
                </li>
              ))}
            </ol>
          </div>
        )}
      </CardBody>
      {testing && <SelfTestModal d={d} status={status} onClose={() => setTesting(false)} />}
    </Card>
  );
}

function Metric({ icon: Icon, label, value, hint, tone }: { icon: typeof Activity; label: string; value: string; hint: string; tone: Tone }) {
  const color = { ok: "text-ok", warn: "text-warn", bad: "text-bad", info: "text-info", muted: "text-fg" }[tone];
  return (
    <div className="rounded-2xl bg-panel-2 px-3.5 py-3 ring-1 ring-line">
      <div className="flex items-center gap-1.5 text-[11px] text-muted">
        <Icon className="size-3.5" />
        {label}
      </div>
      <div className={clsx("mt-1 text-lg font-semibold tabular-nums", color)}>{value}</div>
      <div className="text-[11px] text-subtle">{hint}</div>
    </div>
  );
}

function SelfTestModal({ d, status, onClose }: { d: DeviceStatus; status: Status; onClose: () => void }) {
  const toast = useToast();
  const qc = useQueryClient();
  const [to, setTo] = useState(d.health.selfTestTarget || d.identity.number);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const others = knownDevices(status).filter((x) => x.id !== d.id && x.identity.number);

  const start = async () => {
    setBusy(true);
    setError("");
    try {
      await api.post(`/api/devices/${encodeURIComponent(d.id)}/selftest`, { to });
      toast.info("自检已开始，结果会显示在健康状态里");
      qc.invalidateQueries({ queryKey: ["status"] });
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
      title="收发自检"
      description={`用「${d.name}」发一条带随机编号的短信，确认在 3 分钟内收到。测试短信收到后会自动删除，不会参与分类。`}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            取消
          </Button>
          <Button variant="primary" icon={FlaskConical} loading={busy} disabled={!/^\+?[0-9 -]{5,24}$/.test(to.trim())} onClick={start}>
            发送测试短信
          </Button>
        </>
      }
    >
      <Field label="发送到" hint={d.identity.number ? "默认发给这张卡自己，同时检查发送和接收。" : "这张卡没有存本机号码，请填写它的号码（或另一台设备的号码）。"}>
        <Input value={to} onChange={(e) => setTo(e.target.value)} inputMode="tel" placeholder="这张卡的手机号" className="font-mono" />
      </Field>
      {others.length > 0 && (
        <div className="flex flex-wrap gap-1.5">
          <span className="text-xs text-muted">发到其他设备（交叉检查）：</span>
          {others.map((x) => (
            <button key={x.id} type="button" onClick={() => setTo(x.identity.number)} className="rounded-lg bg-panel-2 px-2 py-0.5 text-xs ring-1 ring-line hover:ring-line-strong">
              {x.name} {x.identity.number}
            </button>
          ))}
        </div>
      )}
      <p className="rounded-xl bg-warn/8 px-3 py-2 text-xs text-muted ring-1 ring-warn/20">会产生一条普通短信费用（一般约 0.1 元，套餐内可能免费）。只在你点击时发送，不会自动定时发送。</p>
      {error && <p className="text-sm text-bad">{error}</p>}
    </Modal>
  );
}
