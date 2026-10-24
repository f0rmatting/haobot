import clsx from "clsx";
import { useEffect, useState, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, BellRing, Bot, Check, ChevronDown, CircleHelp, MessageSquareText, PhoneCall, PhoneMissed, Save, Search, Send } from "lucide-react";
import { api, Unclassified, type ClassifierSettings, type NotifyRules, type NotifySettings, type TelegramChat, type TelegramConfig, type WebhookKind, type WebhookSettings } from "../lib/api";
import { categoryStyle } from "../lib/categories";
import { usePrefs } from "../lib/prefs";
import { Alert, Badge, Button, Card, CardBody, CardHeader, Field, Input, Spinner, Switch, useToast } from "./ui";

type Kind = "telegram" | WebhookKind;

const channels: { kind: Kind; name: string; mark: string; color: string; note: string }[] = [
  { kind: "telegram", name: "Telegram", mark: "TG", color: "bg-[#229ED9]", note: "机器人私聊或群组，国内网络需要代理" },
  { kind: "feishu", name: "飞书", mark: "飞", color: "bg-[#3370FF]", note: "群里的自定义机器人" },
  { kind: "dingtalk", name: "钉钉", mark: "钉", color: "bg-[#0089FF]", note: "群里的自定义机器人" },
  { kind: "wecom", name: "企业微信", mark: "企", color: "bg-[#07C160]", note: "群里的消息推送机器人" },
];

function channelState(data: NotifySettings, kind: Kind): { configured: boolean; enabled: boolean } {
  if (kind === "telegram") return { configured: data.telegram.hasToken && !!data.telegram.chatId, enabled: data.telegram.enabled };
  return { configured: data[kind].hasUrl, enabled: data[kind].enabled };
}

export function useNotify() {
  return useQuery({ queryKey: ["notify"], queryFn: () => api.get<NotifySettings>("/api/settings/notify") });
}

// activeChannels counts the channels that are on and set up.
export function activeChannels(data?: NotifySettings): number {
  if (!data) return 0;
  return channels.filter((c) => {
    const s = channelState(data, c.kind);
    return s.configured && s.enabled;
  }).length;
}

export function NotifySection() {
  const { data } = useNotify();
  if (!data) {
    return (
      <Card className="flex h-40 items-center justify-center">
        <Spinner />
      </Card>
    );
  }
  return (
    <div className="space-y-5">
      <ChannelsCard data={data} />
      <RulesCard data={data} />
    </div>
  );
}

// ---------------- Channels ----------------

function ChannelsCard({ data }: { data: NotifySettings }) {
  const [open, setOpen] = useState<Kind | null>(() => channels.find((c) => channelState(data, c.kind).configured)?.kind ?? null);
  return (
    <Card>
      <CardHeader icon={Send} title="推送渠道" subtitle="可以同时开启多个，每条消息会发到所有开启的渠道" />
      <CardBody className="space-y-2">
        {channels.map((c) => {
          const s = channelState(data, c.kind);
          const expanded = open === c.kind;
          return (
            <div key={c.kind} className={clsx("rounded-2xl ring-1 transition", expanded ? "ring-accent/40" : "ring-line")}>
              <button type="button" aria-expanded={expanded} onClick={() => setOpen(expanded ? null : c.kind)} className="flex w-full items-center gap-3 rounded-2xl px-3.5 py-3 text-left transition hover:bg-panel-2">
                <span className={clsx("flex size-9 shrink-0 items-center justify-center rounded-xl text-[13px] font-semibold text-white", c.color)}>{c.mark}</span>
                <div className="min-w-0 flex-1">
                  <div className="text-sm font-medium">{c.name}</div>
                  <div className="truncate text-[11px] text-muted">{c.note}</div>
                </div>
                {s.configured && s.enabled ? <Badge tone="ok">已开启</Badge> : s.configured ? <Badge tone="warn">已关闭</Badge> : <Badge>未设置</Badge>}
                <ChevronDown className={clsx("size-4 shrink-0 text-muted transition", !expanded && "-rotate-90")} />
              </button>
              {expanded && <div className="border-t border-line px-3.5 pt-4 pb-4">{c.kind === "telegram" ? <TelegramForm data={data} /> : <WebhookForm kind={c.kind} data={data[c.kind]} />}</div>}
            </div>
          );
        })}
      </CardBody>
    </Card>
  );
}

function EnableRow({ checked, onChange, warn }: { checked: boolean; onChange: (v: boolean) => void; warn?: boolean }) {
  return (
    <div className={clsx("flex items-center justify-between gap-3 rounded-xl px-3 py-2.5 ring-1", warn ? "bg-warn/8 ring-warn/30" : "bg-panel-2 ring-line")}>
      <div>
        <div className="text-sm font-medium">推送到这里</div>
        <div className={clsx("text-[11px]", warn ? "text-warn" : "text-muted")}>{warn ? "已经设置好了，但开关关着，不会发送任何消息" : checked ? "新消息会发到这里" : "关闭时只保存设置，不发送"}</div>
      </div>
      <Switch checked={checked} onChange={onChange} label="推送到这里" />
    </div>
  );
}

function Steps({ children }: { children: ReactNode }) {
  return <ol className="list-decimal space-y-1 pl-5 text-xs leading-relaxed text-muted">{children}</ol>;
}

function Result({ result }: { result: { ok: boolean; text: string } | null }) {
  if (!result) return null;
  return <p className={clsx("rounded-xl px-3 py-2 text-xs", result.ok ? "bg-ok/10 text-ok" : "bg-bad/10 text-bad")}>{result.text}</p>;
}

// SecretInput edits a stored secret without showing it: null keeps the
// stored value, "" removes it.
function SecretInput({ value, onChange, stored, hint, placeholder, removable }: { value: string | null; onChange: (v: string | null) => void; stored: boolean; hint: string; placeholder: string; removable?: boolean }) {
  if (value === null && stored) {
    return (
      <div className="flex gap-2">
        <Input value={hint} disabled className="font-mono" />
        <Button onClick={() => onChange("")}>更换</Button>
      </div>
    );
  }
  return (
    <div className="flex gap-2">
      <Input type="password" value={value ?? ""} onChange={(e) => onChange(e.target.value.trim())} placeholder={placeholder} className="font-mono" autoComplete="off" />
      {value !== null && stored && (
        <Button variant="ghost" onClick={() => onChange(null)}>
          取消
        </Button>
      )}
      {removable && value === "" && stored && <span className="self-center text-[11px] whitespace-nowrap text-warn">留空保存会删除</span>}
    </div>
  );
}

const telegramFields = (d: TelegramConfig): TelegramConfig => ({ enabled: d.enabled, chatId: d.chatId, proxy: d.proxy, apiBase: d.apiBase });
const chatType: Record<TelegramChat["type"], string> = { private: "私聊", group: "群组", supergroup: "群组", channel: "频道" };

function TelegramForm({ data: all }: { data: NotifySettings }) {
  const data = all.telegram;
  const { pro } = usePrefs();
  const toast = useToast();
  const qc = useQueryClient();
  const [form, setForm] = useState<TelegramConfig>(() => telegramFields(data));
  const [token, setToken] = useState<string | null>(null);
  const [busy, setBusy] = useState<"save" | "test" | "chats" | null>(null);
  const [result, setResult] = useState<{ ok: boolean; text: string } | null>(null);
  const [chats, setChats] = useState<TelegramChat[] | null>(null);

  const set = (patch: Partial<TelegramConfig>) => {
    setForm({ ...form, ...patch });
    setResult(null);
  };
  const payload = () => ({ ...form, token: token === null ? undefined : token });
  const dirty = token !== null || JSON.stringify(form) !== JSON.stringify(telegramFields(data));
  const ready = (data.hasToken || !!token) && !!form.chatId;

  const run = async (kind: "save" | "test" | "chats") => {
    setBusy(kind);
    setResult(null);
    try {
      if (kind === "save") {
        const next = await api.put<NotifySettings>("/api/settings/notify/telegram", payload());
        qc.setQueryData(["notify"], next);
        setForm(telegramFields(next.telegram));
        setToken(null);
        if (next.telegram.enabled) toast.ok("Telegram 推送已开启");
        else toast.error("设置已保存，但推送开关是关着的，不会发送任何消息");
      } else if (kind === "test") {
        const r = await api.post<{ bot: string; latencyMs: number }>("/api/settings/notify/telegram/test", payload());
        setResult({ ok: true, text: `已通过 @${r.bot} 发送测试消息，耗时 ${r.latencyMs} ms，请在 Telegram 里查看` });
      } else {
        const r = await api.post<{ bot: string; chats: TelegramChat[] }>("/api/settings/notify/telegram/chats", payload());
        setChats(r.chats);
        if (r.chats.length === 0) setResult({ ok: false, text: `@${r.bot} 还没有收到消息：请在 Telegram 里打开 @${r.bot}，发送 /start（群组请先把机器人拉进群并发一条消息），然后再点“获取”` });
        else if (r.chats.length === 1 && !form.chatId) set({ chatId: String(r.chats[0].id) });
      }
    } catch (e) {
      if (kind === "save") toast.error((e as Error).message);
      else setResult({ ok: false, text: (e as Error).message });
    } finally {
      setBusy(null);
    }
  };

  return (
    <div className="space-y-4">
      <EnableRow checked={form.enabled} onChange={(v) => set({ enabled: v })} warn={!form.enabled && ready} />
      <Steps>
        <li>在 Telegram 里找 @BotFather，发送 /newbot 创建机器人，复制它给的 Bot Token 填到下面。</li>
        <li>打开你的机器人发送 /start（推送到群组就把机器人拉进群并发一条消息）。</li>
        <li>点“获取”自动找到 Chat ID，保存后点“发送测试消息”。</li>
      </Steps>
      <div className="grid gap-4 lg:grid-cols-2">
        <Field label="Bot Token" hint={token === null && data.hasToken ? "只保存在本机（加密），页面不会显示原文" : "形如 123456789:AAE…，在 @BotFather 里获取"}>
          <SecretInput value={token} onChange={setToken} stored={data.hasToken} hint={data.tokenHint} placeholder="123456789:AAE..." />
        </Field>
        <Field label="Chat ID" hint="私聊是正数，群组是负数，频道可以填 @频道用户名">
          <div className="flex gap-2">
            <Input value={form.chatId} onChange={(e) => set({ chatId: e.target.value.trim() })} placeholder="例如 123456789" className="font-mono" />
            <Button icon={Search} loading={busy === "chats"} onClick={() => run("chats")}>
              获取
            </Button>
          </div>
        </Field>
      </div>
      {chats && chats.length > 0 && (
        <div className="flex flex-wrap gap-2">
          {chats.map((c) => (
            <button
              key={c.id}
              type="button"
              onClick={() => set({ chatId: String(c.id) })}
              className={clsx("rounded-xl px-3 py-1.5 text-xs ring-1 transition", form.chatId === String(c.id) ? "bg-accent/10 text-accent ring-accent/50" : "bg-panel-2 ring-line hover:ring-line-strong")}
            >
              {chatType[c.type] ?? c.type} · {c.title || "（无名称）"} <span className="font-mono text-subtle">{c.id}</span>
            </button>
          ))}
        </div>
      )}
      <div className="grid gap-4 lg:grid-cols-2">
        <Field label="代理（可选）" hint="例如 socks5://127.0.0.1:7890；路由器已经代理就留空">
          <Input value={form.proxy} onChange={(e) => set({ proxy: e.target.value.trim() })} placeholder="socks5://127.0.0.1:7890" className="font-mono" />
        </Field>
        {pro && (
          <Field label="Bot API 地址（可选）" hint="留空使用 https://api.telegram.org">
            <Input value={form.apiBase} onChange={(e) => set({ apiBase: e.target.value.trim() })} placeholder="https://api.telegram.org" className="font-mono" />
          </Field>
        )}
      </div>
      <Result result={result} />
      <div className="flex flex-wrap justify-end gap-2">
        <Button icon={Send} loading={busy === "test"} onClick={() => run("test")}>
          发送测试消息
        </Button>
        <Button variant="primary" icon={Save} loading={busy === "save"} disabled={!dirty} onClick={() => run("save")}>
          保存
        </Button>
      </div>
    </div>
  );
}

const webhookGuide: Record<WebhookKind, { steps: ReactNode[]; placeholder: string; secret?: string }> = {
  feishu: {
    steps: [
      "在飞书群里点右上角的“更多 → 设置 → 群机器人 → 添加机器人”，选择“自定义机器人”。找不到这个选项的话，可能是企业管理员关闭了自定义机器人。",
      "安全设置建议选“签名校验”，把密钥填到下面；也可以选“自定义关键词”并填“号小助”。",
      "复制 Webhook 地址填到下面，保存后点“发送测试消息”。",
    ],
    placeholder: "https://open.feishu.cn/open-apis/bot/v2/hook/…",
    secret: "签名校验的密钥",
  },
  dingtalk: {
    steps: [
      "在钉钉群里打开“设置 → 机器人 → 添加机器人”，选择“自定义”。",
      "安全设置建议选“加签”，把 SEC 开头的密钥填到下面；也可以选“自定义关键词”并填“号小助”。",
      "复制 Webhook 地址填到下面，保存后点“发送测试消息”。钉钉每个机器人每分钟最多 20 条。",
    ],
    placeholder: "https://oapi.dingtalk.com/robot/send?access_token=…",
    secret: "SEC 开头的加签密钥",
  },
  wecom: {
    steps: [
      "在企业微信的群聊里点右上角，选择“添加群机器人 → 新创建一个机器人”。",
      "复制机器人的 Webhook 地址填到下面，保存后点“发送测试消息”。每个机器人每分钟最多 20 条。",
    ],
    placeholder: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=…",
  },
};

function WebhookForm({ kind, data }: { kind: WebhookKind; data: WebhookSettings }) {
  const toast = useToast();
  const qc = useQueryClient();
  const guide = webhookGuide[kind];
  const label = channels.find((c) => c.kind === kind)!.name;
  const [enabled, setEnabled] = useState(data.enabled);
  const [url, setUrl] = useState<string | null>(null);
  const [secret, setSecret] = useState<string | null>(null);
  const [busy, setBusy] = useState<"save" | "test" | null>(null);
  const [result, setResult] = useState<{ ok: boolean; text: string } | null>(null);
  const dirty = enabled !== data.enabled || url !== null || secret !== null;
  const payload = () => ({ enabled, url: url ?? undefined, secret: secret ?? undefined });

  const run = async (what: "save" | "test") => {
    setBusy(what);
    setResult(null);
    try {
      if (what === "save") {
        const next = await api.put<NotifySettings>(`/api/settings/notify/${kind}`, payload());
        qc.setQueryData(["notify"], next);
        setEnabled(next[kind].enabled);
        setUrl(null);
        setSecret(null);
        if (next[kind].enabled) toast.ok(`${label}推送已开启`);
        else toast.error("设置已保存，但推送开关是关着的，不会发送任何消息");
      } else {
        const r = await api.post<{ latencyMs: number }>(`/api/settings/notify/${kind}/test`, payload());
        setResult({ ok: true, text: `测试消息已发送，耗时 ${r.latencyMs} ms，请在${label}群里查看` });
      }
    } catch (e) {
      if (what === "save") toast.error((e as Error).message);
      else setResult({ ok: false, text: (e as Error).message });
    } finally {
      setBusy(null);
    }
  };

  return (
    <div className="space-y-4">
      <EnableRow
        checked={enabled}
        onChange={(v) => {
          setEnabled(v);
          setResult(null);
        }}
        warn={!enabled && (data.hasUrl || !!url)}
      />
      <Steps>
        {guide.steps.map((s, i) => (
          <li key={i}>{s}</li>
        ))}
      </Steps>
      <div className={clsx("grid gap-4", guide.secret && "lg:grid-cols-2")}>
        <Field label="Webhook 地址" hint={url === null && data.hasUrl ? "地址里带有机器人的密钥，只保存在本机（加密），页面不会显示原文" : "从机器人设置里完整复制"}>
          <SecretInput value={url} onChange={(v) => (setUrl(v), setResult(null))} stored={data.hasUrl} hint={data.urlHint} placeholder={guide.placeholder} />
        </Field>
        {guide.secret && (
          <Field label="签名密钥（可选）" hint={secret === null && data.hasSecret ? "已保存，只保存在本机（加密）" : `没有开启${kind === "feishu" ? "签名校验" : "加签"}就留空`}>
            <SecretInput value={secret} onChange={(v) => (setSecret(v), setResult(null))} stored={data.hasSecret} hint="••••••••" placeholder={guide.secret} removable />
          </Field>
        )}
      </div>
      <Result result={result} />
      <div className="flex flex-wrap justify-end gap-2">
        <Button icon={Send} loading={busy === "test"} disabled={!data.hasUrl && !url} onClick={() => run("test")}>
          发送测试消息
        </Button>
        <Button variant="primary" icon={Save} loading={busy === "save"} disabled={!dirty} onClick={() => run("save")}>
          保存
        </Button>
      </div>
    </div>
  );
}

// ---------------- Rules ----------------

// noisy are the default categories most people do not want pushed.
const noisy = ["营销推广", "推销骚扰"];
const callNoise = ["推销广告", "疑似诈骗"];

function Toggle({ label, hint, checked, onChange, icon: Icon }: { label: string; hint?: string; checked: boolean; onChange: (v: boolean) => void; icon?: typeof PhoneCall }) {
  return (
    <div className="flex items-center justify-between gap-3 rounded-xl px-3 py-2.5 ring-1 ring-line">
      <div className="flex min-w-0 items-center gap-2.5">
        {Icon && <Icon className="size-4 shrink-0 text-muted" />}
        <div className="min-w-0">
          <div className="text-sm">{label}</div>
          {hint && <div className="text-[11px] text-muted">{hint}</div>}
        </div>
      </div>
      <Switch checked={checked} onChange={onChange} label={label} />
    </div>
  );
}

function CategoryOption({ name, label, description, checked, onChange }: { name: string; label: string; description?: string; checked: boolean; onChange: () => void }) {
  const s = categoryStyle(name === Unclassified ? "" : name);
  return (
    <button
      type="button"
      role="checkbox"
      aria-checked={checked}
      onClick={onChange}
      className={clsx("flex items-start gap-3 rounded-xl px-3 py-2.5 text-left ring-1 transition", checked ? "bg-accent/8 ring-accent/40" : "ring-line hover:ring-line-strong")}
    >
      <span className={clsx("mt-0.5 flex size-[18px] shrink-0 items-center justify-center rounded-md ring-1 transition", checked ? "bg-gradient-accent text-white ring-transparent" : "bg-panel-2 ring-line-strong")}>{checked && <Check className="size-3" strokeWidth={3} />}</span>
      <span className="min-w-0">
        <span className={clsx("flex items-center gap-1.5 text-sm font-medium", !checked && "text-muted")}>
          <s.icon className="size-3.5" />
          {label}
        </span>
        {description && <span className="mt-0.5 line-clamp-2 block text-[11px] text-subtle">{description}</span>}
      </span>
    </button>
  );
}

function RulesCard({ data }: { data: NotifySettings }) {
  const toast = useToast();
  const qc = useQueryClient();
  const { data: classifier } = useQuery({ queryKey: ["classifier"], queryFn: () => api.get<ClassifierSettings>("/api/settings/classifier") });
  const [form, setForm] = useState<NotifyRules>(data.rules);
  const [busy, setBusy] = useState(false);
  const saved = JSON.stringify(data.rules);
  useEffect(() => setForm(JSON.parse(saved)), [saved]);

  const set = (patch: Partial<NotifyRules>) => setForm({ ...form, ...patch });
  const dirty = JSON.stringify(form) !== saved;
  const categories = classifier?.categories ?? [];
  const all = [...categories.map((c) => c.name), Unclassified];
  const pushed = all.filter((c) => !form.skipCategories.includes(c));
  const toggleCall = (name: string) =>
    set({ skipCallCategories: form.skipCallCategories.includes(name) ? form.skipCallCategories.filter((c) => c !== name) : [...form.skipCallCategories, name] });
  const toggle = (name: string) => set({ skipCategories: form.skipCategories.includes(name) ? form.skipCategories.filter((c) => c !== name) : [...form.skipCategories, name] });
  const preset = (skip: string[]) => set({ skipCategories: skip });
  const none = activeChannels(data) === 0;

  const save = async () => {
    setBusy(true);
    try {
      const next = await api.put<NotifySettings>("/api/settings/notify/rules", form);
      qc.setQueryData(["notify"], next);
      toast.ok("推送内容已保存");
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card>
      <CardHeader icon={BellRing} title="推送内容" subtitle="选择哪些短信和电话需要通知你，对所有渠道生效" />
      <CardBody className="space-y-5">
        {none && (
          <Alert tone="warn" title="还没有开启推送渠道">
            先在上面设置好一个渠道并打开开关，这里选的内容才会推送给你。
          </Alert>
        )}

        <div className="space-y-3">
          <div className="text-[13px] font-medium">电话</div>
          <div className="grid gap-2 sm:grid-cols-3">
            <Toggle icon={PhoneCall} label="来电提醒" hint="电话一响就推送" checked={form.ringing} onChange={(v) => set({ ringing: v })} />
            <Toggle icon={PhoneMissed} label="未接来电" checked={form.missed} onChange={(v) => set({ missed: v })} />
            <Toggle icon={Bot} label="AI 接听小结" hint="来电者、来意、是否回电" checked={form.summaries} onChange={(v) => set({ summaries: v })} />
          </div>
          {form.summaries && (
            <div className="space-y-2.5 rounded-2xl bg-panel-2/50 p-3 ring-1 ring-line">
              <div className="flex flex-wrap items-center gap-2">
                <span className="text-xs text-muted">AI 接听后，这些来电才推送小结：</span>
                <Button size="sm" variant="ghost" onClick={() => set({ skipCallCategories: [] })}>
                  全部
                </Button>
                <Button size="sm" variant="ghost" onClick={() => set({ skipCallCategories: callNoise.filter((c) => data.callCategories.includes(c)) })}>
                  不要推销和诈骗
                </Button>
              </div>
              <div className="flex flex-wrap gap-2">
                {[...data.callCategories, Unclassified].map((c) => {
                  const on = !form.skipCallCategories.includes(c);
                  return (
                    <button
                      key={c}
                      type="button"
                      role="checkbox"
                      aria-checked={on}
                      onClick={() => toggleCall(c)}
                      className={clsx("inline-flex items-center gap-1.5 rounded-xl px-3 py-1.5 text-[13px] ring-1 transition", on ? "bg-accent/8 ring-accent/40" : "text-muted ring-line hover:ring-line-strong")}
                    >
                      <span className={clsx("flex size-4 items-center justify-center rounded ring-1", on ? "bg-gradient-accent text-white ring-transparent" : "bg-panel-2 ring-line-strong")}>{on && <Check className="size-2.5" strokeWidth={3} />}</span>
                      {c === Unclassified ? "没有分出类别的" : c}
                    </button>
                  );
                })}
              </div>
              <Toggle
                icon={AlertTriangle}
                label="紧急来电总是推送"
                hint="AI 判断为“紧急程度：高”的来电，不管属于哪一类都推送"
                checked={!form.skipUrgentToo}
                onChange={(v) => set({ skipUrgentToo: !v })}
              />
              <p className="text-[11px] text-subtle">通话结束后由大模型判断类别和紧急程度。不推送的小结仍然保存在“电话”页里。</p>
            </div>
          )}
        </div>

        <div className="space-y-3">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div className="flex items-center gap-2 text-[13px] font-medium">
              <MessageSquareText className="size-4 text-muted" />
              短信
            </div>
            <Switch checked={form.sms} onChange={(v) => set({ sms: v })} label="推送短信" />
          </div>
          {form.sms ? (
            <>
              <div className="flex flex-wrap items-center gap-2">
                <span className="text-xs text-muted">勾选需要推送的类别：</span>
                <Button size="sm" variant="ghost" onClick={() => preset([])}>
                  全部
                </Button>
                <Button size="sm" variant="ghost" onClick={() => preset(noisy.filter((n) => all.includes(n)))}>
                  不要广告和推销
                </Button>
                {all.includes("验证码") && (
                  <Button size="sm" variant="ghost" onClick={() => preset(all.filter((n) => n !== "验证码"))}>
                    只要验证码
                  </Button>
                )}
              </div>
              {classifier ? (
                <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
                  {categories.map((c) => (
                    <CategoryOption key={c.name} name={c.name} label={c.name} description={c.description} checked={!form.skipCategories.includes(c.name)} onChange={() => toggle(c.name)} />
                  ))}
                  <CategoryOption name={Unclassified} label="还没分好类的" description="智能分类没开启、出错，或 20 秒内没有结果的短信" checked={!form.skipCategories.includes(Unclassified)} onChange={() => toggle(Unclassified)} />
                </div>
              ) : (
                <Spinner />
              )}
              <p className="flex items-start gap-1.5 text-[11px] text-subtle">
                <CircleHelp className="mt-px size-3.5 shrink-0" />
                {pushed.length === all.length ? "所有短信都会推送。" : pushed.length === 0 ? "所有类别都没勾选，不会推送任何短信。" : `推送 ${pushed.length} 类，不推送：${form.skipCategories.map((c) => (c === Unclassified ? "还没分好类的" : c)).join("、")}。`}
                以后新加的类别默认推送。
              </p>
              <Toggle label="隐藏验证码" hint="验证码显示为 ••••••；关闭时会单独列出，方便复制" checked={form.maskCodes} onChange={(v) => set({ maskCodes: v })} />
            </>
          ) : (
            <p className="rounded-xl bg-panel-2 px-3 py-2.5 text-xs text-muted ring-1 ring-line">不推送任何短信，只在网页上查看。</p>
          )}
        </div>

        <p className="text-[11px] leading-relaxed text-subtle">推送的短信原文（包括验证码，除非选择隐藏）会经过对应平台的服务器。</p>
        <div className="flex justify-end">
          <Button variant="primary" icon={Save} loading={busy} disabled={!dirty} onClick={save}>
            保存
          </Button>
        </div>
      </CardBody>
    </Card>
  );
}
