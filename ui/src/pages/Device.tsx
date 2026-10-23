import { useRef, useState, type FormEvent } from "react";
import { useSearchParams } from "react-router-dom";
import clsx from "clsx";
import { useQueryClient } from "@tanstack/react-query";
import { Activity, AlertTriangle, Antenna, Cpu, CreditCard, Inbox, KeyRound, Loader2, Lock, Pencil, PhoneForwarded, Plane, Power, RefreshCw, RotateCcw, SignalHigh, Terminal, Trash2, Unlock, Usb, Wifi, Wrench, type LucideIcon } from "lucide-react";
import { api, type DeviceStatus } from "../lib/api";
import { deviceColor, deviceHealth } from "../lib/devices";
import { useStatus } from "../lib/hooks";
import { ago, generation, radioLabel, signal, simLabel } from "../lib/format";
import { usePrefs } from "../lib/prefs";
import { Alert, Badge, Button, Card, CardBody, CardHeader, ConfirmModal, Dot, Empty, IconButton, Input, KV, ProBadge, Select, Spinner, Switch, useToast } from "../components/ui";
import { SignalBars } from "../components/Visuals";
import { SimDialogs, type SimDialog } from "../components/SimDialogs";
import { HealthCard } from "../components/HealthCard";
import { ForwardingCard } from "../components/ForwardingCard";
import { DeviceAvatar, DeviceIdentity } from "../components/DeviceIdentity";
import { AutoUnlock, PinProtectionNote } from "../components/AutoUnlock";

type DeviceTab = "status" | "sim" | "calls" | "maintain";

const deviceTabs: { id: DeviceTab; label: string; icon: LucideIcon }[] = [
  { id: "status", label: "状态", icon: Activity },
  { id: "sim", label: "SIM 卡", icon: CreditCard },
  { id: "calls", label: "通话", icon: PhoneForwarded },
  { id: "maintain", label: "维护", icon: Wrench },
];

export function Device() {
  const { data: status } = useStatus();
  const { pro } = usePrefs();
  const [params, setParams] = useSearchParams();
  if (!status) {
    return (
      <div className="flex h-64 items-center justify-center">
        <Spinner />
      </div>
    );
  }
  const devices = status.devices.filter((x) => x.id);
  const pending = status.devices.filter((x) => !x.id);
  const d = devices.find((x) => x.id === params.get("d")) ?? devices[0];
  const tab = deviceTabs.find((t) => t.id === params.get("tab"))?.id ?? "status";
  const select = (id: string, next: DeviceTab = tab) => setParams({ d: id, tab: next }, { replace: true });
  const multi = devices.length > 1 || pending.length > 0;

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">设备</h1>
          <p className="mt-0.5 text-sm text-muted">{devices.length > 1 ? `共 ${devices.length} 台，选择一台查看和设置，插入新模组会自动识别` : "插入新模组会自动识别"}</p>
        </div>
        {d && <RefreshButton d={d} />}
      </div>
      {!d ? (
        <>
          {pending.length > 0 && (
            <Alert tone="info" icon={Loader2} title="正在连接新发现的模组">
              {pending.map((x) => x.endpoint).join("、")}
            </Alert>
          )}
          <Card>
            <Empty icon={Usb} title="还没有找到模组">
              用 USB 连接模组后会自动出现在这里。macOS 通过 libusb 直接访问，Linux 使用内核驱动生成的 /dev/ttyUSB*。
            </Empty>
          </Card>
        </>
      ) : (
        <div className={clsx("grid gap-5", multi && "lg:grid-cols-[248px_minmax(0,1fr)] lg:items-start")}>
          {multi && <DeviceRail devices={devices} pending={pending} selected={d.id} onSelect={(id) => select(id)} />}
          <div key={d.id} className="animate-in min-w-0 space-y-5">
            <DeviceHeader d={d} />
            {status.orphans > 0 && <OrphansAlert d={d} count={status.orphans} />}
            {!d.online && (
              <Alert tone="muted" icon={AlertTriangle} title="设备离线">
                最后在线 {ago(d.lastSeen)}。插回模组后会自动重新连接，下面显示的是历史信息。
              </Alert>
            )}
            {d.online && !d.connected && (
              <Alert tone="bad" icon={AlertTriangle} title="模组连接中断">
                以下信息可能已过期，程序会自动重连。{d.lastError && `（${d.lastError}）`}
              </Alert>
            )}
            <nav aria-label="设备设置" className="flex gap-1 border-b border-line">
              {deviceTabs.map((t) => (
                <button
                  key={t.id}
                  type="button"
                  aria-current={tab === t.id ? "page" : undefined}
                  onClick={() => select(d.id, t.id)}
                  className={clsx(
                    "-mb-px flex items-center gap-1.5 border-b-2 px-3 py-2 text-sm transition",
                    tab === t.id ? "border-accent font-medium text-fg" : "border-transparent text-muted hover:text-fg",
                  )}
                >
                  <t.icon className={clsx("size-4", tab === t.id && "text-accent")} />
                  {t.label}
                  {t.id === "sim" && d.modem.sim !== "READY" && d.online && <Dot tone={simLabel(d.modem.sim).tone} />}
                </button>
              ))}
            </nav>
            {tab === "status" && (
              <>
                {d.online && <HealthCard d={d} status={status} />}
                <div className="grid gap-5 lg:grid-cols-2">
                  <NetworkCard d={d} />
                  <InfoCard d={d} />
                  {pro && <LteCard d={d} />}
                </div>
              </>
            )}
            {tab === "sim" && (
              <div className="grid gap-5 lg:grid-cols-2">
                <SimCard d={d} />
                <Card>
                  <CardHeader icon={KeyRound} title="开机自动解锁" subtitle={`只对“${d.name}”这张 SIM 卡生效`} />
                  <CardBody className="space-y-4 pt-3">
                    <AutoUnlock d={d} />
                    <PinProtectionNote />
                  </CardBody>
                </Card>
              </div>
            )}
            {tab === "calls" &&
              (d.online ? (
                <div className="grid gap-5 lg:grid-cols-2">
                  <ForwardingCard d={d} />
                </div>
              ) : (
                <Card>
                  <Empty icon={PhoneForwarded} title="设备离线">
                    呼叫转移需要从模组读取，设备连接后再来设置。
                  </Empty>
                </Card>
              ))}
            {tab === "maintain" && (
              <>
                <div className="grid gap-5 lg:grid-cols-2">
                  <PowerCard d={d} />
                  {pro && <ScanModeCard d={d} scanModes={status.scanModes} />}
                </div>
                {pro ? <ConsoleCard d={d} /> : <p className="text-xs text-subtle">切换到专业模式后，这里还有网络制式锁定、服务小区参数和 AT 控制台。</p>}
              </>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

// DeviceRail lists the devices to pick from: a column beside the details on
// wide screens, a scrolling row on phones.
function DeviceRail({ devices, pending, selected, onSelect }: { devices: DeviceStatus[]; pending: DeviceStatus[]; selected: string; onSelect: (id: string) => void }) {
  return (
    <nav aria-label="选择设备" className="scroll-thin -mx-4 flex gap-2 overflow-x-auto px-4 pb-1 lg:sticky lg:top-6 lg:mx-0 lg:flex-col lg:overflow-visible lg:px-0 lg:pb-0">
      {devices.map((x) => {
        const active = x.id === selected;
        const sig = signal(x.modem);
        const live = x.online && x.connected;
        return (
          <button
            key={x.id}
            type="button"
            aria-current={active ? "true" : undefined}
            onClick={() => onSelect(x.id)}
            className={clsx(
              "flex w-60 shrink-0 items-center gap-2 rounded-2xl p-2.5 text-left ring-1 transition lg:w-auto",
              active ? clsx("bg-panel-solid shadow-sm", deviceColor(x.id).ring) : "bg-panel-2/60 ring-line hover:ring-line-strong",
            )}
          >
            <div className="min-w-0 flex-1">
              <DeviceIdentity d={x} size="sm" />
            </div>
            <div className="flex shrink-0 flex-col items-end gap-1">
              <SignalBars bars={live ? sig.bars : 0} tone={live ? sig.tone : "muted"} className="h-3.5" />
              {x.unread > 0 && <span className="font-mono text-[10px] text-accent">{x.unread} 未读</span>}
            </div>
          </button>
        );
      })}
      {pending.map((x) => (
        <div key={x.endpoint} className="flex w-60 shrink-0 items-center gap-2.5 rounded-2xl bg-panel-2/40 p-2.5 text-[12px] text-muted border border-dashed border-line-strong lg:w-auto">
          <Loader2 className="size-4 shrink-0 animate-spin" />
          <div className="min-w-0">
            <div className="font-medium">正在连接…</div>
            <div className="truncate font-mono text-[11px] text-subtle">{x.endpoint}</div>
          </div>
        </div>
      ))}
    </nav>
  );
}

// OrphansAlert offers to attribute messages saved before multi-device
// support to this device.
function OrphansAlert({ d, count }: { d: DeviceStatus; count: number }) {
  const toast = useToast();
  const qc = useQueryClient();
  const [busy, setBusy] = useState(false);
  return (
    <Alert
      tone="info"
      icon={Inbox}
      title={`有 ${count} 条升级前的短信还没有归属设备`}
      action={
        <Button
          size="sm"
          loading={busy}
          onClick={async () => {
            setBusy(true);
            try {
              const r = await api.post<{ affected: number }>(`/api/devices/${encodeURIComponent(d.id)}/adopt`);
              toast.ok(`已归属 ${r.affected} 条短信`);
              qc.invalidateQueries({ queryKey: ["status"] });
              qc.invalidateQueries({ queryKey: ["conversations"] });
            } catch (e) {
              toast.error((e as Error).message);
            } finally {
              setBusy(false);
            }
          }}
        >
          归属到“{d.name}”
        </Button>
      }
    >
      它们是只支持单台设备时收到的。同时接了多台模组，程序无法判断来自哪一台，请确认后归属，之后才能在会话里回复。
    </Alert>
  );
}

function DeviceHeader({ d }: { d: DeviceStatus }) {
  const toast = useToast();
  const qc = useQueryClient();
  const [editing, setEditing] = useState(false);
  const [name, setName] = useState(d.name);
  const [confirm, setConfirm] = useState<"disable" | "forget" | null>(null);
  const health = deviceHealth(d);
  const patch = async (body: { name?: string; enabled?: boolean }, done: string) => {
    try {
      await api.patch(`/api/devices/${encodeURIComponent(d.id)}`, body);
      toast.ok(done);
      qc.invalidateQueries({ queryKey: ["status"] });
      setEditing(false);
    } catch (e) {
      toast.error((e as Error).message);
    }
  };
  return (
    <Card>
      <div className="flex flex-wrap items-center gap-4 p-5">
        <DeviceAvatar d={d} size="lg" />
        <div className="min-w-0 flex-1">
          {editing ? (
            <form
              className="flex gap-2"
              onSubmit={(e) => {
                e.preventDefault();
                patch({ name }, "已重命名");
              }}
            >
              <Input value={name} onChange={(e) => setName(e.target.value)} maxLength={24} autoFocus className="max-w-xs" />
              <Button type="submit" variant="primary" disabled={!name.trim()}>
                保存
              </Button>
              <Button variant="ghost" onClick={() => (setEditing(false), setName(d.name))}>
                取消
              </Button>
            </form>
          ) : (
            <div className="flex items-center gap-2">
              <h2 className="truncate text-lg font-semibold tracking-tight">{d.name}</h2>
              <IconButton icon={Pencil} label="重命名" onClick={() => setEditing(true)} className="size-8" />
              <Badge tone={health.tone}>{health.label}</Badge>
            </div>
          )}
          <p className="mt-0.5 truncate font-mono text-[11px] text-muted">
            {d.identity.number && `本机 ${d.identity.number} · `}
            {d.model || "未知型号"} · IMEI {d.imei || "未知"} · {d.endpoint}
          </p>
        </div>
        <div className="flex items-center gap-3">
          <div className="flex items-center gap-2 text-[13px]">
            <span className="text-muted">启用</span>
            <Switch checked={d.enabled} label="启用设备" onChange={(v) => (v ? patch({ enabled: true }, "设备已启用") : setConfirm("disable"))} />
          </div>
          {!d.online && (
            <Button size="sm" variant="ghost" icon={Trash2} onClick={() => setConfirm("forget")}>
              移除
            </Button>
          )}
        </div>
      </div>
      <ConfirmModal
        open={confirm === "disable"}
        onClose={() => setConfirm(null)}
        title="停用这台设备？"
        description="停用后保持连接、继续显示状态，但不再读取和保存新短信，也不能发短信。随时可以重新启用。"
        confirmLabel="停用"
        onConfirm={async () => {
          await patch({ enabled: false }, "设备已停用");
          setConfirm(null);
        }}
      />
      <ConfirmModal
        open={confirm === "forget"}
        onClose={() => setConfirm(null)}
        danger
        title="从列表中移除？"
        description="只移除设备记录，它的短信会保留。模组再次插入时会重新出现（使用默认名称）。"
        confirmLabel="移除"
        onConfirm={async () => {
          try {
            await api.del(`/api/devices/${encodeURIComponent(d.id)}`);
            toast.ok("已移除");
            qc.invalidateQueries({ queryKey: ["status"] });
          } catch (e) {
            toast.error((e as Error).message);
          }
          setConfirm(null);
        }}
      />
    </Card>
  );
}

function useDeviceAction(d: DeviceStatus) {
  const toast = useToast();
  const qc = useQueryClient();
  const [busy, setBusy] = useState<string | null>(null);
  const run = async (key: string, path: string, body: unknown, success: string) => {
    setBusy(key);
    try {
      await api.post(`/api/devices/${encodeURIComponent(d.id)}${path}`, body);
      toast.ok(success);
      qc.invalidateQueries({ queryKey: ["status"] });
      return true;
    } catch (e) {
      toast.error((e as Error).message);
      return false;
    } finally {
      setBusy(null);
    }
  };
  return { busy, run };
}

function RefreshButton({ d }: { d: DeviceStatus }) {
  const { busy, run } = useDeviceAction(d);
  return (
    <Button size="sm" icon={RefreshCw} loading={busy === "refresh"} disabled={!d.connected} onClick={() => run("refresh", "/refresh", {}, "已刷新")}>
      立即刷新
    </Button>
  );
}

function NetworkCard({ d }: { d: DeviceStatus }) {
  const m = d.modem;
  const sig = signal(m);
  return (
    <Card>
      <CardHeader icon={Antenna} title="网络" subtitle="运营商、制式与数据连接" />
      <CardBody className="pt-3">
        <div className="mb-3 flex items-center gap-4 rounded-2xl bg-panel-2 px-4 py-3.5 ring-1 ring-line">
          <SignalBars bars={sig.bars} tone={sig.tone} className="h-7" />
          <div className="min-w-0 flex-1">
            <div className="text-sm font-semibold">
              {m.operator || "未注册"} <span className="font-normal text-muted">· {generation(m.accessTech)}</span>
            </div>
            <div className="text-xs text-muted">
              信号{sig.label}
              {sig.dbm !== null && ` · ${sig.dbm} dBm`}
            </div>
          </div>
          <Badge tone={m.registered ? "ok" : "warn"}>{m.registered ? "已注册" : m.registration || "未注册"}</Badge>
        </div>
        <dl className="divide-y divide-line">
          <KV label="接入技术" value={m.accessTech} />
          <KV label="频段" value={m.band} />
          <KV label="数据网络" value={m.attached === 1 ? <Badge tone="ok" icon={Wifi}>已附着</Badge> : m.attached === 0 ? <Badge>未附着</Badge> : ""} />
          <KV label="APN" value={m.apn} mono />
          <KV label="IP 地址" value={m.ip} mono />
          <KV label="射频" value={radioLabel(m.functionality)} />
        </dl>
      </CardBody>
    </Card>
  );
}

function SimCard({ d }: { d: DeviceStatus }) {
  const m = d.modem;
  const { pro } = usePrefs();
  const [dialog, setDialog] = useState<SimDialog>(null);
  const sim = simLabel(m.sim);
  const ready = m.sim === "READY";
  return (
    <Card>
      <CardHeader icon={CreditCard} title="SIM 卡" subtitle="PIN 码与卡片信息" action={<Badge tone={sim.tone}>{sim.label}</Badge>} />
      <CardBody className="pt-3">
        <dl className="divide-y divide-line">
          <KV label="PIN 锁" value={m.pinLock === 1 ? <Badge tone="info" icon={Lock}>已开启</Badge> : m.pinLock === 0 ? <Badge icon={Unlock}>未开启</Badge> : ""} />
          <KV label="剩余尝试次数" value={m.pinRetries >= 0 ? `PIN ${m.pinRetries} 次 · PUK ${m.pukRetries} 次` : ""} />
          <KV label="本机号码" value={d.identity.number} hint={!d.identity.number && ready ? "很多 SIM 卡不保存本机号码，这是正常的" : undefined} />
          <KV label="ICCID" value={d.identity.iccid} mono />
          {pro && <KV label="IMSI" value={d.identity.imsi} mono />}
          {pro && <KV label="短信中心" value={d.identity.smsc} mono />}
        </dl>
        {d.pinBlocked && (
          <div className="mt-3">
            <Alert tone="warn" icon={AlertTriangle} title="自动解锁已暂停">
              为防止锁卡，上次自动输入 PIN 失败（或剩余次数不足）后不再自动尝试。手动解锁或在设置里更新 PIN 后会恢复。
            </Alert>
          </div>
        )}
        <div className="mt-4 flex flex-wrap gap-2">
          {m.sim === "SIM PIN" && (
            <Button size="sm" variant="primary" icon={KeyRound} onClick={() => setDialog("pin")}>
              输入 PIN 解锁
            </Button>
          )}
          {m.sim === "SIM PUK" && (
            <Button size="sm" variant="danger" icon={KeyRound} onClick={() => setDialog("puk")}>
              用 PUK 解锁
            </Button>
          )}
          {ready && m.pinLock === 0 && (
            <Button size="sm" icon={Lock} onClick={() => setDialog("lock-on")}>
              开启 PIN 锁
            </Button>
          )}
          {ready && m.pinLock === 1 && (
            <>
              <Button size="sm" icon={KeyRound} onClick={() => setDialog("change")}>
                修改 PIN
              </Button>
              <Button size="sm" variant="ghost" icon={Unlock} onClick={() => setDialog("lock-off")}>
                关闭 PIN 锁
              </Button>
            </>
          )}
        </div>
      </CardBody>
      <SimDialogs deviceId={d.id} dialog={dialog} onClose={() => setDialog(null)} pinRetries={m.pinRetries} pukRetries={m.pukRetries} />
    </Card>
  );
}

function PowerCard({ d }: { d: DeviceStatus }) {
  const { busy, run } = useDeviceAction(d);
  const [confirm, setConfirm] = useState<"reboot" | "airplane" | null>(null);
  const airplane = d.modem.functionality === 4;
  return (
    <Card>
      <CardHeader icon={Power} title="电源与射频" subtitle="重启模组、飞行模式" />
      <CardBody className="space-y-3 pt-3">
        <div className="flex items-center justify-between gap-4 rounded-2xl bg-panel-2 px-4 py-3.5 ring-1 ring-line">
          <div className="flex items-start gap-3">
            <Plane className="mt-0.5 size-5 text-muted" />
            <div>
              <p className="text-sm font-medium">飞行模式</p>
              <p className="text-xs text-muted">关闭射频，期间收不到短信。可用于强制重新搜网。</p>
            </div>
          </div>
          <Switch checked={airplane} disabled={!d.connected || busy === "radio"} label="飞行模式" onChange={(on) => (on ? setConfirm("airplane") : run("radio", "/radio", { on: true }, "已关闭飞行模式"))} />
        </div>
        <div className="flex items-center justify-between gap-4 rounded-2xl bg-panel-2 px-4 py-3.5 ring-1 ring-line">
          <div className="flex items-start gap-3">
            <RotateCcw className="mt-0.5 size-5 text-muted" />
            <div>
              <p className="text-sm font-medium">重启模组</p>
              <p className="text-xs text-muted">模组卡住、长时间不注册网络时使用。大约 20～40 秒后自动恢复连接。</p>
            </div>
          </div>
          <Button size="sm" variant="danger" loading={busy === "reboot"} disabled={!d.connected} onClick={() => setConfirm("reboot")}>
            重启
          </Button>
        </div>
      </CardBody>
      <ConfirmModal
        open={confirm === "reboot"}
        onClose={() => setConfirm(null)}
        danger
        loading={busy === "reboot"}
        title="重启模组？"
        description="重启期间（约 20～40 秒）无法收短信，完成后会自动重新连接。如果 SIM 卡开启了 PIN 锁且未保存 PIN，重启后需要重新解锁。"
        confirmLabel="重启"
        onConfirm={async () => {
          await run("reboot", "/reboot", {}, "模组正在重启");
          setConfirm(null);
        }}
      />
      <ConfirmModal
        open={confirm === "airplane"}
        onClose={() => setConfirm(null)}
        loading={busy === "radio"}
        title="开启飞行模式？"
        description="射频关闭后收不到短信，直到你关闭飞行模式。"
        confirmLabel="开启"
        onConfirm={async () => {
          await run("radio", "/radio", { on: false }, "已开启飞行模式");
          setConfirm(null);
        }}
      />
    </Card>
  );
}

function InfoCard({ d }: { d: DeviceStatus }) {
  const { pro } = usePrefs();
  return (
    <Card>
      <CardHeader icon={Cpu} title="设备信息" />
      <CardBody className="pt-3">
        <dl className="divide-y divide-line">
          <KV label="型号" value={d.modem.model} />
          <KV label="固件版本" value={d.modem.revision} mono />
          <KV label="IMEI" value={d.identity.imei} mono />
          <KV label="连接方式" value={d.endpoint} mono />
          <KV label="短信功能" value={d.smsReady ? <Badge tone="ok">就绪</Badge> : <Badge tone="warn">未就绪</Badge>} />
          <KV label="模组短信存储" value={d.modem.storageTotal > 0 ? `${d.modem.storageUsed} / ${d.modem.storageTotal}` : ""} />
          {pro && <KV label="最近错误" value={d.lastError} />}
        </dl>
      </CardBody>
    </Card>
  );
}

function ScanModeCard({ d, scanModes }: { d: DeviceStatus; scanModes: Record<string, string> }) {
  const { busy, run } = useDeviceAction(d);
  const current = d.modem.scanMode;
  const [mode, setMode] = useState<string>(current >= 0 ? String(current) : "0");
  const [confirm, setConfirm] = useState(false);
  return (
    <Card>
      <CardHeader icon={SignalHigh} title={<span className="flex items-center gap-2">网络制式 <ProBadge /></span>} subtitle="限制模组只使用某种网络（移远 AT+QCFG）" />
      <CardBody className="space-y-3 pt-3">
        <p className="text-[13px] text-muted">当前：{current >= 0 ? scanModes[current] ?? `模式 ${current}` : "未知（模组可能不支持）"}</p>
        <div className="flex gap-2">
          <Select value={mode} onChange={(e) => setMode(e.target.value)}>
            {Object.entries(scanModes).map(([k, v]) => (
              <option key={k} value={k}>
                {v}
              </option>
            ))}
          </Select>
          <Button disabled={!d.connected || String(current) === mode} onClick={() => setConfirm(true)}>
            应用
          </Button>
        </div>
        <p className="text-xs text-muted">锁定在当地没有覆盖的制式会导致无法注册网络、收不到短信。国内 2G/3G 正在陆续退网，一般建议“自动”或“仅 4G”。</p>
      </CardBody>
      <ConfirmModal
        open={confirm}
        onClose={() => setConfirm(false)}
        loading={busy === "scan"}
        title={`切换到“${scanModes[mode]}”？`}
        description="模组会立即重新搜网，期间可能短暂断网。"
        confirmLabel="切换"
        onConfirm={async () => {
          await run("scan", "/scan-mode", { mode: Number(mode) }, "网络制式已切换");
          setConfirm(false);
        }}
      />
    </Card>
  );
}

function LteCard({ d }: { d: DeviceStatus }) {
  const c = d.modem.cell;
  return (
    <Card>
      <CardHeader icon={Antenna} title={<span className="flex items-center gap-2">服务小区 <ProBadge /></span>} subtitle="AT+QENG=&quot;servingcell&quot;" />
      <CardBody className="pt-3">
        {c.valid ? (
          <>
            <div className="mb-3 grid grid-cols-2 gap-2 sm:grid-cols-4">
              <Metric label="RSRP" value={c.rsrp} unit="dBm" good={c.rsrp >= -95} />
              <Metric label="RSRQ" value={c.rsrq} unit="dB" good={c.rsrq >= -12} />
              <Metric label="SINR" value={c.sinr} unit="dB" good={c.sinr >= 10} />
              <Metric label="RSSI" value={c.rssi} unit="dBm" good={c.rssi >= -75} />
            </div>
            <dl className="grid grid-cols-1 gap-x-6 divide-y divide-line sm:grid-cols-2 sm:divide-y-0">
              <KV label="状态" value={c.state} mono />
              <KV label="Band / EARFCN" value={`B${c.band} / ${c.earfcn}`} mono />
              <KV label="PCI" value={String(c.pci)} mono />
              <KV label="Cell ID" value={c.cellId} mono />
              <KV label="TAC" value={c.tac} mono />
              <KV label="MCC / MNC" value={`${c.mcc} / ${c.mnc}`} mono />
            </dl>
          </>
        ) : (
          <p className="text-[13px] text-muted">{c.rat ? `当前制式 ${c.rat}，仅解析 LTE 小区参数。` : "模组没有返回服务小区信息。"}</p>
        )}
        {c.raw && <pre className="mt-3 overflow-x-auto rounded-xl bg-panel-2 p-3 font-mono text-[11px] text-muted ring-1 ring-line">{c.raw}</pre>}
      </CardBody>
    </Card>
  );
}

function Metric({ label, value, unit, good }: { label: string; value: number; unit: string; good: boolean }) {
  return (
    <div className="rounded-xl bg-panel-2 px-3 py-2.5 ring-1 ring-line">
      <div className="text-[11px] text-muted">{label}</div>
      <div className={good ? "font-mono text-lg font-semibold text-ok" : "font-mono text-lg font-semibold text-warn"}>
        {value}
        <span className="ml-1 text-[11px] font-normal text-muted">{unit}</span>
      </div>
    </div>
  );
}

interface ConsoleLine {
  kind: "cmd" | "out" | "err";
  text: string;
}

const quickCommands = ["ATI", "AT+CSQ", "AT+COPS?", "AT+CPIN?", "AT+CREG?", "AT+CPMS?", 'AT+QENG="servingcell"', "AT+QNWINFO"];

function ConsoleCard({ d }: { d: DeviceStatus }) {
  const connected = d.connected;
  const [cmd, setCmd] = useState("");
  const [lines, setLines] = useState<ConsoleLine[]>([{ kind: "out", text: "号小助 AT 控制台。指令与短信同步排队执行，互不干扰。↑/↓ 浏览历史。" }]);
  const [history, setHistory] = useState<string[]>([]);
  const [cursor, setCursor] = useState(-1);
  const [busy, setBusy] = useState(false);
  const screen = useRef<HTMLDivElement>(null);

  const send = async (command: string) => {
    const c = command.trim();
    if (!c) return;
    setBusy(true);
    setLines((ls) => [...ls, { kind: "cmd", text: c }]);
    setHistory((h) => [c, ...h.filter((x) => x !== c)].slice(0, 50));
    setCursor(-1);
    setCmd("");
    try {
      const r = await api.post<{ lines: string[] }>(`/api/devices/${encodeURIComponent(d.id)}/console`, { command: c });
      setLines((ls) => [...ls, ...r.lines.map((t) => ({ kind: (t === "OK" ? "out" : /ERROR/.test(t) ? "err" : "out") as ConsoleLine["kind"], text: t }))]);
    } catch (e) {
      setLines((ls) => [...ls, { kind: "err", text: (e as Error).message }]);
    } finally {
      setBusy(false);
      requestAnimationFrame(() => screen.current?.scrollTo({ top: screen.current.scrollHeight }));
    }
  };

  const submit = (e: FormEvent) => {
    e.preventDefault();
    send(cmd);
  };

  return (
    <Card>
      <CardHeader icon={Terminal} title={<span className="flex items-center gap-2">AT 控制台 <ProBadge /></span>} subtitle="直接向模组发送 AT 指令" action={<Button size="sm" variant="ghost" onClick={() => setLines([])}>清屏</Button>} />
      <CardBody className="space-y-3 pt-3">
        <Alert tone="warn" icon={AlertTriangle} title="请谨慎操作">
          错误的指令可能改变模组配置、锁死 SIM 卡或导致模组无法联网。不确定的指令不要执行。所有指令都会记录到日志（PIN 会被隐去）。
        </Alert>
        <div ref={screen} className="scroll-thin h-72 overflow-y-auto rounded-2xl bg-[#05080f] p-4 font-mono text-[12.5px] leading-relaxed text-slate-300 ring-1 ring-line">
          {lines.map((l, i) => (
            <div key={i} className={l.kind === "cmd" ? "mt-2 text-cyan-300" : l.kind === "err" ? "text-rose-400" : l.text === "OK" ? "text-emerald-400" : ""}>
              {l.kind === "cmd" ? `> ${l.text}` : l.text}
            </div>
          ))}
          {busy && <div className="text-slate-500">…</div>}
        </div>
        <form onSubmit={submit} className="flex gap-2">
          <Input
            value={cmd}
            onChange={(e) => setCmd(e.target.value)}
            disabled={!connected}
            placeholder={connected ? "输入 AT 指令，例如 AT+CSQ" : "模组未连接"}
            className="font-mono"
            autoComplete="off"
            spellCheck={false}
            onKeyDown={(e) => {
              if (e.key === "ArrowUp" && history.length) {
                e.preventDefault();
                const n = Math.min(cursor + 1, history.length - 1);
                setCursor(n);
                setCmd(history[n]);
              } else if (e.key === "ArrowDown") {
                e.preventDefault();
                const n = cursor - 1;
                setCursor(Math.max(n, -1));
                setCmd(n >= 0 ? history[n] : "");
              }
            }}
          />
          <Button type="submit" variant="primary" loading={busy} disabled={!connected || !cmd.trim()}>
            发送
          </Button>
        </form>
        <div className="flex flex-wrap gap-1.5">
          {quickCommands.map((c) => (
            <button key={c} type="button" disabled={!connected || busy} onClick={() => send(c)} className="rounded-lg bg-panel-2 px-2 py-1 font-mono text-[11px] text-muted ring-1 ring-line transition hover:text-fg disabled:opacity-50">
              {c}
            </button>
          ))}
        </div>
      </CardBody>
    </Card>
  );
}
