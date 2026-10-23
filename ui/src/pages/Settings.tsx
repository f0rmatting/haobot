import clsx from "clsx";
import { useEffect, useState, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useLocation, useNavigate } from "react-router-dom";
import { ArrowRight, BellRing, Bot, Brain, CheckCircle2, CreditCard, Cpu, Eye, FlaskConical, KeyRound, Monitor, Moon, Palette, Plus, RotateCcw, Save, ScrollText, ShieldCheck, SlidersHorizontal, Sparkles, Sun, Trash2, Wand2, type LucideIcon } from "lucide-react";
import { api, ApiError, type AssistantSettings, type Category, type ClassifierConfig, type ClassifierSettings, type ClassifyLog, type Finding } from "../lib/api";
import { knownDevices } from "../lib/devices";
import { useStatus } from "../lib/hooks";
import { ago, simLabel } from "../lib/format";
import { usePrefs } from "../lib/prefs";
import { Alert, Badge, Button, Card, CardBody, CardHeader, Collapsible, ConfirmModal, Empty, Field, Input, ProBadge, Segmented, Spinner, Switch, Textarea, useToast } from "../components/ui";
import { CategoryChip } from "../components/MessageParts";
import { ClassifyLogList } from "../components/ClassifyLogList";
import { ClassifyLogDetail } from "../components/ClassifyLog";
import { DeviceIdentity } from "../components/DeviceIdentity";
import { AutoUnlock, PinProtectionNote } from "../components/AutoUnlock";
import { AssistantSection } from "../components/AssistantSettings";
import { activeChannels, NotifySection, useNotify } from "../components/NotifySettings";

type Tab = "notify" | "assistant" | "classifier" | "devices" | "general";

const tabs: { id: Tab; label: string; hint: string; icon: LucideIcon }[] = [
  { id: "notify", label: "消息推送", hint: "Telegram、飞书、钉钉、企业微信", icon: BellRing },
  { id: "assistant", label: "AI 接听", hint: "自动接电话、问来意", icon: Bot },
  { id: "classifier", label: "智能分类", hint: "大模型、类别、调用记录", icon: Brain },
  { id: "devices", label: "设备", hint: "按设备分别设置 SIM 卡自动解锁", icon: Cpu },
  { id: "general", label: "通用", hint: "登录密码、界面", icon: SlidersHorizontal },
];

// Older links pointed at sections of the single long page.
const legacyHash: Record<string, Tab> = { telegram: "notify", sim: "devices" };

function tabFromHash(hash: string): Tab {
  const id = hash.slice(1);
  return tabs.find((t) => t.id === id)?.id ?? legacyHash[id] ?? "notify";
}

export function SettingsPage() {
  const { hash } = useLocation();
  const navigate = useNavigate();
  const tab = tabFromHash(hash);
  const { data: notify } = useNotify();
  const { data: assistant } = useQuery({ queryKey: ["assistant"], queryFn: () => api.get<AssistantSettings>("/api/settings/assistant") });
  const { data: classifier } = useQuery({ queryKey: ["classifier"], queryFn: () => api.get<ClassifierSettings>("/api/settings/classifier") });
  const state: Partial<Record<Tab, boolean | undefined>> = {
    notify: notify && activeChannels(notify) > 0,
    assistant: assistant?.enabled,
    classifier: classifier?.enabled,
  };
  const open = (id: Tab) => {
    navigate({ hash: id }, { replace: true });
    window.scrollTo({ top: 0 });
  };

  return (
    <div className="space-y-5">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">设置</h1>
        <p className="mt-0.5 text-sm text-muted">{tabs.find((t) => t.id === tab)!.hint}</p>
      </div>
      <div className="grid gap-5 lg:grid-cols-[208px_minmax(0,1fr)] lg:items-start">
        <nav aria-label="设置分类" className="scroll-thin -mx-4 flex gap-1.5 overflow-x-auto px-4 pb-1 lg:sticky lg:top-6 lg:mx-0 lg:flex-col lg:overflow-visible lg:px-0 lg:pb-0">
          {tabs.map((t) => {
            const active = t.id === tab;
            const on = state[t.id];
            return (
              <button
                key={t.id}
                type="button"
                aria-current={active ? "page" : undefined}
                onClick={() => open(t.id)}
                className={clsx(
                  "flex shrink-0 items-center gap-2.5 rounded-xl px-3 py-2 text-left text-sm whitespace-nowrap transition outline-none focus-visible:ring-2 focus-visible:ring-accent/60 lg:py-2.5",
                  active ? "bg-accent/10 font-medium text-fg ring-1 ring-accent/30" : "text-muted hover:bg-panel-2 hover:text-fg",
                )}
              >
                <t.icon className={clsx("size-4 shrink-0", active && "text-accent")} />
                <span className="flex-1">{t.label}</span>
                {on !== undefined && <span className={clsx("hidden text-[10px] lg:inline", on ? "text-ok" : "text-subtle")}>{on ? "已开启" : "未开启"}</span>}
              </button>
            );
          })}
        </nav>
        <div key={tab} className="animate-in min-w-0 space-y-5">
          {tab === "notify" && <NotifySection />}
          {tab === "assistant" && <AssistantSection />}
          {tab === "classifier" && <ClassifierSection />}
          {tab === "devices" && <DevicesSection />}
          {tab === "general" && (
            <div className="grid gap-5 xl:grid-cols-2">
              <PasswordSection />
              <AppearanceSection />
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

// ---------------- Password ----------------

function PasswordSection() {
  const toast = useToast();
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const strength = next.length >= 14 ? 3 : next.length >= 10 ? 2 : next.length >= 8 ? 1 : 0;

  const save = async () => {
    setBusy(true);
    setError("");
    try {
      await api.put("/api/settings/password", { current, new: next });
      toast.ok("密码已修改，其他设备上的登录已退出");
      setCurrent("");
      setNext("");
      setConfirm("");
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card>
      <CardHeader icon={KeyRound} title="后台登录密码" subtitle="修改后其他设备上的登录会自动退出" />
      <CardBody className="space-y-4">
        <Field label="当前密码">
          <Input type="password" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} />
        </Field>
        <Field label="新密码" hint="至少 8 位，建议 12 位以上并混合字母、数字和符号。">
          <Input type="password" autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)} />
          <div className="mt-2 flex gap-1">
            {[1, 2, 3].map((i) => (
              <span key={i} className={clsx("h-1 flex-1 rounded-full", i <= strength ? (strength === 1 ? "bg-warn" : "bg-ok") : "bg-line-strong")} />
            ))}
          </div>
        </Field>
        <Field label="确认新密码" error={confirm && confirm !== next ? "两次输入不一致" : undefined}>
          <Input type="password" autoComplete="new-password" value={confirm} onChange={(e) => setConfirm(e.target.value)} />
        </Field>
        {error && <p className="text-sm text-bad">{error}</p>}
        <Button variant="primary" icon={Save} loading={busy} disabled={!current || next.length < 8 || next !== confirm} onClick={save}>
          修改密码
        </Button>
      </CardBody>
    </Card>
  );
}

// ---------------- Devices ----------------

// DevicesSection lists every device's own settings, each under a header
// that says which device it is.
function DevicesSection() {
  const { data: status } = useStatus();
  const navigate = useNavigate();
  const devices = knownDevices(status);
  if (!status) return <Spinner />;
  if (devices.length === 0) {
    return (
      <Card>
        <Empty icon={Cpu} title="还没有设备">
          模组连接后会出现在这里，可以为每台设备单独保存 SIM 卡的 PIN。
        </Empty>
      </Card>
    );
  }
  return (
    <div className="space-y-4">
      {devices.length > 1 && <p className="text-[13px] text-muted">共 {devices.length} 台设备。每台设备的设置互不影响，颜色和编号（IMEI 末两位）在各个页面都一样。</p>}
      {devices.map((d) => {
        const sim = simLabel(d.modem.sim);
        return (
          <Card key={d.id}>
            <div className="flex flex-wrap items-center gap-3 border-b border-line px-5 py-4">
              <div className="min-w-0 flex-1">
                <DeviceIdentity d={d} extra={d.online ? d.modem.operator || undefined : d.lastSeen ? `最后在线 ${ago(d.lastSeen)}` : undefined} />
              </div>
              <Button size="sm" variant="ghost" icon={ArrowRight} onClick={() => navigate(`/device?d=${encodeURIComponent(d.id)}&tab=sim`)}>
                设备页
              </Button>
            </div>
            <CardBody className="grid gap-5 lg:grid-cols-[minmax(0,220px)_minmax(0,1fr)]">
              <div className="space-y-2">
                <div className="flex items-center gap-2 text-[13px] font-medium">
                  <CreditCard className="size-4 text-muted" />
                  SIM 卡
                </div>
                <dl className="space-y-1.5 text-[13px]">
                  <div className="flex justify-between gap-2">
                    <dt className="text-muted">状态</dt>
                    <dd>
                      <Badge tone={sim.tone}>{sim.label}</Badge>
                    </dd>
                  </div>
                  <div className="flex justify-between gap-2">
                    <dt className="text-muted">PIN 锁</dt>
                    <dd>{d.modem.pinLock === 1 ? "已开启" : d.modem.pinLock === 0 ? "未开启" : "—"}</dd>
                  </div>
                  <div className="flex justify-between gap-2">
                    <dt className="text-muted">ICCID</dt>
                    <dd className="truncate font-mono text-xs" title={d.identity.iccid}>
                      {d.identity.iccid ? `··${d.identity.iccid.slice(-6)}` : "—"}
                    </dd>
                  </div>
                </dl>
                {d.modem.pinLock === 0 && <p className="text-[11px] text-subtle">这张卡没开 PIN 锁，不需要自动解锁。</p>}
              </div>
              <AutoUnlock d={d} />
            </CardBody>
          </Card>
        );
      })}
      <PinProtectionNote />
    </div>
  );
}

// ---------------- Classifier ----------------

interface Preset {
  id: string;
  name: string;
  provider: "openai" | "anthropic";
  baseUrl: string;
  model: string;
  note: string;
}

// Model names are examples; providers rename models over time.
const presets: Preset[] = [
  { id: "deepseek", name: "DeepSeek", provider: "openai", baseUrl: "https://api.deepseek.com/v1", model: "deepseek-chat", note: "国内直连，价格低" },
  { id: "qwen", name: "通义千问（阿里云百炼）", provider: "openai", baseUrl: "https://dashscope.aliyuncs.com/compatible-mode/v1", model: "qwen-plus", note: "OpenAI 兼容模式" },
  { id: "zhipu", name: "智谱 GLM", provider: "openai", baseUrl: "https://open.bigmodel.cn/api/paas/v4", model: "glm-4-flash", note: "有免费模型" },
  { id: "moonshot", name: "Kimi（月之暗面）", provider: "openai", baseUrl: "https://api.moonshot.cn/v1", model: "", note: "填写控制台中的模型名" },
  { id: "ollama", name: "Ollama（本地）", provider: "openai", baseUrl: "http://127.0.0.1:11434/v1", model: "qwen2.5:7b", note: "数据不出本机，无需 Key" },
  { id: "openai", name: "OpenAI", provider: "openai", baseUrl: "https://api.openai.com/v1", model: "", note: "填写要使用的模型名" },
  { id: "anthropic", name: "Claude（Anthropic）", provider: "anthropic", baseUrl: "", model: "claude-haiku-5-5", note: "原生 Messages API" },
];

function ClassifierSection() {
  const { pro } = usePrefs();
  const toast = useToast();
  const qc = useQueryClient();
  const { data: status } = useStatus();
  const { data } = useQuery({ queryKey: ["classifier"], queryFn: () => api.get<ClassifierSettings>("/api/settings/classifier"), refetchInterval: 15000 });
  const [form, setForm] = useState<ClassifierConfig | null>(null);
  const [apiKey, setApiKey] = useState<string | null>(null); // null: keep stored key
  const [busy, setBusy] = useState<"save" | "test" | "reclassify" | null>(null);
  const [testResult, setTestResult] = useState<{ ok: boolean; text: string } | null>(null);
  const [confirmAll, setConfirmAll] = useState(false);

  useEffect(() => {
    if (data && !form) setForm({ enabled: data.enabled, provider: data.provider, baseUrl: data.baseUrl, model: data.model, categories: data.categories });
  }, [data, form]);

  if (!data || !form) {
    return (
      <Card className="flex h-48 items-center justify-center">
        <Spinner />
      </Card>
    );
  }

  const set = (patch: Partial<ClassifierConfig>) => {
    setForm({ ...form, ...patch });
    setTestResult(null);
  };
  const payload = () => ({ ...form, apiKey: apiKey === null ? undefined : apiKey });
  const dirty =
    apiKey !== null ||
    JSON.stringify({ ...form }) !== JSON.stringify({ enabled: data.enabled, provider: data.provider, baseUrl: data.baseUrl, model: data.model, categories: data.categories });

  const save = async () => {
    setBusy("save");
    try {
      const next = await api.put<ClassifierSettings>("/api/settings/classifier", payload());
      qc.setQueryData(["classifier"], next);
      qc.invalidateQueries({ queryKey: ["status"] });
      setApiKey(null);
      toast.ok("分类设置已保存");
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(null);
    }
  };

  const test = async () => {
    setBusy("test");
    setTestResult(null);
    try {
      const r = await api.post<{ reply: string; latencyMs: number }>("/api/settings/classifier/test", payload());
      setTestResult({ ok: true, text: `连接成功，耗时 ${r.latencyMs} ms，模型回复：${r.reply.slice(0, 40)}` });
    } catch (e) {
      setTestResult({ ok: false, text: (e as Error).message });
    } finally {
      setBusy(null);
    }
  };

  const reclassifyAll = async () => {
    setBusy("reclassify");
    try {
      const r = await api.post<{ affected: number }>("/api/settings/classifier/reclassify");
      toast.ok(`已将 ${r.affected} 条短信加入重新分类队列`);
      qc.invalidateQueries({ queryKey: ["status"] });
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(null);
      setConfirmAll(false);
    }
  };

  const activePreset = presets.find((p) => p.provider === form.provider && p.baseUrl === form.baseUrl)?.id ?? "custom";
  const st = data.status;

  return (
    <Card glow>
      <CardHeader
        icon={Brain}
        title="智能分类"
        subtitle="先在本机去除敏感信息，再交给大模型判断短信类别"
        action={<Switch checked={form.enabled} onChange={(v) => set({ enabled: v })} label="启用大模型分类" />}
      />
      <CardBody className="space-y-4">
        <div className="grid gap-3 sm:grid-cols-4">
          <StatBox label="待分类" value={status?.messages.pending ?? 0} />
          <StatBox label="本次启动已分类" value={st.classified} />
          <StatBox label="其中由大模型判定" value={st.byModel} />
          <StatBox label="上次运行" text={st.lastRun && !st.lastRun.startsWith("0001") ? ago(st.lastRun) : "—"} />
        </div>
        {st.lastError && (
          <Alert tone="warn" title="最近一次分类失败">
            {st.lastError}（失败后会自动退避重试，修正配置并保存后立即重试）
          </Alert>
        )}

        {!form.enabled && (
          <Alert tone="warn" icon={Sparkles} title="未启用大模型">
            短信分类完全由大模型完成，没有本地规则兜底。启用并配置好大模型之前，新短信会保持“待分类”；配置好后会自动补上分类。
          </Alert>
        )}

        <Collapsible
          icon={Sparkles}
          title="模型与接口"
          subtitle={form.model ? `${presets.find((p) => p.id === activePreset)?.name ?? "自定义"} · ${form.model}${data.hasApiKey || apiKey ? "" : " · 未填写 API Key"}` : "还没有设置"}
          defaultOpen={!form.model}
          className={clsx(!form.enabled && "opacity-70")}
        >
          <div>
            <div className="mb-2 text-[13px] font-medium">选择服务商</div>
            <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
              {presets.map((p) => (
                <button
                  key={p.id}
                  type="button"
                  onClick={() => set({ provider: p.provider, baseUrl: p.baseUrl, model: p.model || form.model })}
                  className={clsx(
                    "rounded-2xl px-3.5 py-3 text-left ring-1 transition",
                    activePreset === p.id ? "bg-accent/10 ring-accent/50" : "bg-panel-2 ring-line hover:ring-line-strong",
                  )}
                >
                  <div className="flex items-center justify-between gap-2 text-sm font-medium">
                    {p.name}
                    {activePreset === p.id && <CheckCircle2 className="size-4 text-accent" />}
                  </div>
                  <div className="mt-0.5 text-[11px] text-muted">{p.note}</div>
                </button>
              ))}
              <div className={clsx("rounded-2xl px-3.5 py-3 ring-1", activePreset === "custom" ? "bg-accent/10 ring-accent/50" : "bg-panel-2 ring-line")}>
                <div className="text-sm font-medium">自定义</div>
                <div className="mt-0.5 text-[11px] text-muted">任何 OpenAI 兼容接口</div>
              </div>
            </div>
          </div>

          <div className="grid gap-4 lg:grid-cols-2">
            {pro && (
              <Field label={<span className="flex items-center gap-2">接口类型 <ProBadge /></span>}>
                <Segmented
                  value={form.provider}
                  onChange={(v) => set({ provider: v })}
                  options={[
                    { value: "openai", label: "OpenAI 兼容" },
                    { value: "anthropic", label: "Anthropic" },
                  ]}
                />
              </Field>
            )}
            <Field label="Base URL" hint={form.provider === "anthropic" ? "留空使用 https://api.anthropic.com" : "通常以 /v1 结尾，请求会发到 {Base URL}/chat/completions"}>
              <Input value={form.baseUrl} onChange={(e) => set({ baseUrl: e.target.value })} placeholder={form.provider === "anthropic" ? "https://api.anthropic.com" : "https://api.example.com/v1"} className="font-mono" spellCheck={false} />
            </Field>
            <Field label="模型名称" hint="预设里的模型名仅作示例，请以服务商控制台为准。">
              <Input value={form.model} onChange={(e) => set({ model: e.target.value })} placeholder="例如 deepseek-chat" className="font-mono" spellCheck={false} />
            </Field>
            <Field
              label="API Key"
              hint={
                data.hasApiKey && apiKey === null
                  ? `已保存：${data.apiKeyHint}。只保存在本机（加密），页面不会显示原文。`
                  : data.hasApiKey
                    ? "输入新的 Key 后保存；留空保存会删除已保存的 Key。"
                    : "本地 Ollama 可以留空。"
              }
            >
              {apiKey === null && data.hasApiKey ? (
                <div className="flex gap-2">
                  <Input value={data.apiKeyHint} disabled className="font-mono" />
                  <Button onClick={() => setApiKey("")}>更换</Button>
                </div>
              ) : (
                <div className="flex gap-2">
                  <Input type="password" value={apiKey ?? ""} onChange={(e) => setApiKey(e.target.value)} placeholder="sk-..." className="font-mono" autoComplete="off" />
                  {data.hasApiKey && (
                    <Button variant="ghost" onClick={() => setApiKey(null)}>
                      保留原 Key
                    </Button>
                  )}
                </div>
              )}
            </Field>
          </div>

        </Collapsible>

        <CategoryEditor categories={form.categories} defaults={data.defaultCategories} onChange={(categories) => set({ categories })} editable={pro} />

        {testResult && (
          <Alert tone={testResult.ok ? "ok" : "bad"} title={testResult.ok ? "连接正常" : "连接失败"}>
            {testResult.text}
          </Alert>
        )}

        <div className="flex flex-wrap gap-2">
          <Button variant="primary" icon={Save} loading={busy === "save"} disabled={!dirty} onClick={save}>
            保存设置
          </Button>
          <Button icon={FlaskConical} loading={busy === "test"} disabled={!form.model} onClick={test}>
            测试连接
          </Button>
          <Button variant="ghost" icon={RotateCcw} onClick={() => setConfirmAll(true)}>
            重新分类全部短信
          </Button>
        </div>

        <Collapsible icon={ShieldCheck} title="隐私保护：发送前自动脱敏" subtitle="看看大模型实际会收到什么">
          <PrivacyNote />
          <Playground form={form} apiKey={apiKey} />
        </Collapsible>

        <Collapsible icon={ScrollText} title="分类调用记录" subtitle="每一次大模型调用和手动修改，最近 2000 条">
          <ClassifyLogList />
        </Collapsible>
      </CardBody>

      <ConfirmModal
        open={confirmAll}
        onClose={() => setConfirmAll(false)}
        onConfirm={reclassifyAll}
        loading={busy === "reclassify"}
        title="重新分类全部短信？"
        description="除手动指定过分类的短信外，其余短信会按当前设置重新分类。启用大模型时会产生相应的调用费用。"
        confirmLabel="开始"
      />
    </Card>
  );
}

function StatBox({ label, value, text }: { label: string; value?: number; text?: string }) {
  return (
    <div className="rounded-2xl bg-panel-2 px-4 py-3 ring-1 ring-line">
      <div className="text-[11px] text-muted">{label}</div>
      <div className="mt-0.5 text-lg font-semibold tabular-nums">{text ?? value}</div>
    </div>
  );
}

function PrivacyNote() {
  const items = ["验证码", "取件码/兑换码", "密码", "身份证/护照等证件", "银行卡号", "卡号/账户尾号", "订单/快递单号", "手机号", "座机/400 电话", "邮箱", "微信/QQ 等账号", "车牌", "IP 地址", "地址与门牌", "姓名（称呼、收件人等）", "金额", "链接参数", "其余 4 位以上数字"];
  return (
    <div className="rounded-2xl bg-ok/8 px-4 py-3.5 ring-1 ring-ok/20">
      <p className="flex items-center gap-1.5 text-[13px] font-semibold text-ok">
        <ShieldCheck className="size-4" /> 发送前自动脱敏
      </p>
      <p className="mt-1 text-xs leading-relaxed text-muted">以下信息会在本机用规则识别并替换成占位符（如 [验证码]），大模型只看到脱敏后的文字。【签名】、日期和年份会保留，方便判断类别；个人手机号发件人也会被隐去，只保留 95588、106 这类服务号码。规则无法保证识别出所有隐私，可以用下面的“试一试”检查：</p>
      <div className="mt-2 flex flex-wrap gap-1.5">
        {items.map((i) => (
          <span key={i} className="rounded-md bg-panel-2 px-2 py-0.5 text-[11px] text-fg/80 ring-1 ring-line">
            {i}
          </span>
        ))}
      </div>
    </div>
  );
}

function Playground({ form, apiKey }: { form: ClassifierConfig; apiKey: string | null }) {
  const [text, setText] = useState("【某银行】您尾号8842的储蓄卡于10月20日支出 128.00 元，验证码 663201。尊敬的张三先生，如有疑问请致电 13812345678。");
  const [sender, setSender] = useState("95588");
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<{ sender: string; text: string; findings: Finding[]; code?: string; model?: { category: string; confidence: number }; modelError?: string; call?: ClassifyLog } | null>(null);

  const run = async (callModel: boolean) => {
    setBusy(true);
    try {
      const r = await api.post<typeof result>("/api/settings/classifier/preview", { ...form, apiKey: apiKey ?? undefined, text, sender, callModel });
      setResult(r);
    } catch (e) {
      setResult({ sender: "", text: "", findings: [], modelError: e instanceof ApiError ? e.message : "请求失败" });
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="space-y-3 rounded-2xl border border-dashed border-line-strong p-4">
      <div className="flex items-center gap-2 text-[13px] font-semibold">
        <Eye className="size-4 text-accent" /> 试一试：看看大模型会收到什么
      </div>
      <div className="grid gap-3 sm:grid-cols-[160px_1fr]">
        <Field label="发件号码">
          <Input value={sender} onChange={(e) => setSender(e.target.value)} className="font-mono" />
        </Field>
        <Field label="短信内容">
          <Textarea value={text} onChange={(e) => setText(e.target.value)} rows={3} />
        </Field>
      </div>
      <div className="flex flex-wrap gap-2">
        <Button size="sm" icon={ShieldCheck} loading={busy} onClick={() => run(false)}>
          只看脱敏结果
        </Button>
        <Button size="sm" variant="primary" icon={Wand2} loading={busy} disabled={!form.model} onClick={() => run(true)}>
          脱敏并调用大模型分类
        </Button>
      </div>
      {result && (
        <div className="animate-in space-y-3">
          <div className="flex flex-wrap gap-1.5">
            {result.findings.length === 0 ? <Badge tone="ok">未发现敏感信息</Badge> : result.findings.map((f) => <Badge key={f.kind} tone="warn">{`${f.kind} ×${f.count}`}</Badge>)}
          </div>
          {result.text && <pre className="rounded-xl bg-panel-2 p-3 font-mono text-xs leading-relaxed whitespace-pre-wrap ring-1 ring-line">{`发件号码：${result.sender}\n短信内容：\n${result.text}`}</pre>}
          <div className="flex flex-wrap items-center gap-3 text-[13px]">
            {result.code && (
              <span>
                识别到验证码：<code className="font-mono font-semibold">{result.code}</code>
              </span>
            )}
            {result.model && (
              <span className="flex items-center gap-1.5">
                大模型：<CategoryChip name={result.model.category} />
                <span className="text-muted">置信度 {Math.round(result.model.confidence * 100)}%</span>
              </span>
            )}
          </div>
          {result.modelError && <p className="text-[13px] text-bad">{result.modelError}</p>}
          {result.call && (
            <div className="rounded-2xl p-3 ring-1 ring-line">
              <p className="mb-2 text-xs font-medium text-muted">这次调用的完整请求和回复</p>
              <ClassifyLogDetail l={result.call} />
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function CategoryEditor({ categories, defaults, onChange, editable }: { categories: Category[]; defaults: Category[]; onChange: (c: Category[]) => void; editable: boolean }) {
  const update = (i: number, patch: Partial<Category>) => onChange(categories.map((c, j) => (j === i ? { ...c, ...patch } : c)));
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2 text-[13px] font-semibold">
          分类类别 {!editable && <span className="text-xs font-normal text-muted">（切换到专业模式可编辑）</span>}
        </div>
        {editable && (
          <div className="flex gap-2">
            <Button size="sm" variant="ghost" icon={RotateCcw} onClick={() => onChange(defaults)}>
              恢复默认
            </Button>
            <Button size="sm" icon={Plus} disabled={categories.length >= 20} onClick={() => onChange([...categories, { name: "", description: "" }])}>
              添加类别
            </Button>
          </div>
        )}
      </div>
      {editable ? (
        <div className="space-y-2">
          {categories.map((c, i) => (
            <div key={i} className="grid gap-2 rounded-2xl bg-panel-2 p-3 ring-1 ring-line sm:grid-cols-[160px_1fr_auto]">
              <Input value={c.name} maxLength={12} onChange={(e) => update(i, { name: e.target.value })} placeholder="类别名称" />
              <Input value={c.description} onChange={(e) => update(i, { description: e.target.value })} placeholder="告诉大模型这一类包含什么样的短信" />
              <Button variant="ghost" icon={Trash2} disabled={categories.length <= 1} onClick={() => onChange(categories.filter((_, j) => j !== i))} aria-label="删除类别" />
            </div>
          ))}
          <p className="text-xs text-muted">描述会原样发给大模型，写得越具体分得越准。修改后记得保存，并可以“重新分类全部短信”。</p>
        </div>
      ) : (
        <div className="flex flex-wrap gap-2">
          {categories.map((c) => (
            <span key={c.name} title={c.description}>
              <CategoryChip name={c.name} />
            </span>
          ))}
        </div>
      )}
    </div>
  );
}

// ---------------- Appearance ----------------

function AppearanceSection() {
  const { mode, setMode, theme, setTheme } = usePrefs();
  return (
    <Card>
      <CardHeader icon={Palette} title="界面" subtitle="保存在当前浏览器" />
      <CardBody className="space-y-5">
        <Row label="使用模式" hint={mode === "simple" ? "简洁模式：只显示日常需要的功能，操作都有引导。" : "专业模式：额外显示 AT 控制台、LTE 射频参数、网络制式、原始数据和类别编辑。"}>
          <Segmented
            value={mode}
            onChange={setMode}
            options={[
              { value: "simple", label: "简洁", icon: Sparkles },
              { value: "pro", label: "专业", icon: Wand2 },
            ]}
          />
        </Row>
        <Row label="主题">
          <Segmented
            value={theme}
            onChange={setTheme}
            options={[
              { value: "system", label: "跟随系统", icon: Monitor },
              { value: "dark", label: "深色", icon: Moon },
              { value: "light", label: "浅色", icon: Sun },
            ]}
          />
        </Row>
      </CardBody>
    </Card>
  );
}

function Row({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <div className="space-y-2">
      <div className="text-[13px] font-medium">{label}</div>
      {children}
      {hint && <p className="text-xs leading-relaxed text-muted">{hint}</p>}
    </div>
  );
}
