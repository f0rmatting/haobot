import clsx from "clsx";
import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Bot, CheckCircle2, Cpu, FlaskConical, Mic, Save, ShieldAlert, ShieldCheck } from "lucide-react";
import { api, type AssistantConfig, type AssistantSettings } from "../lib/api";
import { Alert, Button, Card, CardBody, CardHeader, Collapsible, Field, Input, Segmented, Select, Spinner, Switch, Textarea, useToast } from "./ui";

// Realtime speech models on Model Studio, best first.
const models = [
  { id: "qwen3.8-omni-flash-realtime", name: "Qwen3.8 Omni Flash", note: "推荐 · 最新，反应快，能听懂“嗯”“喂”不打断" },
  { id: "qwen3.5-omni-plus-realtime", name: "Qwen3.5 Omni Plus", note: "理解和表达更好，费用更高" },
  { id: "qwen3.5-omni-flash-realtime", name: "Qwen3.5 Omni Flash", note: "便宜、快" },
  { id: "qwen3-omni-flash-realtime", name: "Qwen3 Omni Flash", note: "旧版，按声音停顿断句" },
];

type Voice = { id: string; name: string };
// Mandarin voices of each model family.
const voices38: Voice[] = [
  { id: "Tina", name: "甜甜 Tina（温暖女声）" },
  { id: "Liora Mira", name: "清欢 Liora Mira（温柔女声）" },
  { id: "Mia", name: "舒然 Mia（温柔女声）" },
  { id: "Katerina", name: "卡捷琳娜 Katerina（成熟女声）" },
  { id: "Raymond", name: "林川野 Raymond（清亮男声）" },
  { id: "Ryan", name: "甜茶 Ryan（男声）" },
  { id: "Cindy", name: "林欣宜 Cindy（台湾口音女声）" },
];
const voices35: Voice[] = [
  { id: "Tina", name: "甜甜 Tina（温暖女声）" },
  { id: "Serena", name: "苏瑶 Serena（女声）" },
  { id: "Liora Mira", name: "清欢 Liora Mira（温柔女声）" },
  { id: "Mia", name: "舒然 Mia（温柔女声）" },
  { id: "Maia", name: "四月 Maia（女声）" },
  { id: "Katerina", name: "卡捷琳娜 Katerina（成熟女声）" },
  { id: "Ethan", name: "晨煦 Ethan（男声）" },
  { id: "Theo Calm", name: "予安 Theo Calm（沉稳男声）" },
  { id: "Raymond", name: "林川野 Raymond（清亮男声）" },
  { id: "Evan", name: "江晨 Evan（男声）" },
  { id: "Cindy", name: "林欣宜 Cindy（台湾口音女声）" },
];
const voices3: Voice[] = [
  { id: "Cherry", name: "Cherry（女声，默认）" },
  { id: "Jennifer", name: "Jennifer（女声）" },
  { id: "Katerina", name: "Katerina（成熟女声）" },
  { id: "Ethan", name: "Ethan（男声）" },
  { id: "Ryan", name: "Ryan（男声）" },
  { id: "Elias", name: "Elias（男声）" },
];

function voicesFor(model: string): Voice[] {
  if (model.startsWith("qwen3-omni")) return voices3;
  if (model.startsWith("qwen3.5")) return voices35;
  return voices38;
}

// The endpoint choice: derived from the classifier, a region of the
// workspace, or a URL typed in.
type EndpointMode = "auto" | "ap-southeast-1" | "cn-beijing" | "custom";
const regionURL = /^wss:\/\/([^./]+)\.(ap-southeast-1|cn-beijing)\.maas\.aliyuncs\.com\/api-ws\/v1\/realtime$/;
const buildRegionURL = (workspace: string, region: string) => `wss://${workspace || "{WorkspaceId}"}.${region}.maas.aliyuncs.com/api-ws/v1/realtime`;

function endpointMode(url: string): EndpointMode {
  if (!url) return "auto";
  const m = url.match(regionURL);
  return m ? (m[2] as EndpointMode) : "custom";
}

const fields = (d: AssistantConfig): AssistantConfig => ({
  enabled: d.enabled,
  ringDelaySec: d.ringDelaySec,
  greeting: d.greeting,
  instructions: d.instructions,
  model: d.model,
  voice: d.voice,
  maxDurationSec: d.maxDurationSec,
  url: d.url,
  speechRate: d.speechRate,
  ownerName: d.ownerName,
  style: d.style,
  discloseAI: d.discloseAI,
  dailyLimit: d.dailyLimit,
  perCallerMax: d.perCallerMax,
  maxTurns: d.maxTurns,
  record: d.record,
  keepDays: d.keepDays,
});

const num = (v: string) => Math.max(0, Math.round(Number(v) || 0));

export function AssistantSection() {
  const toast = useToast();
  const qc = useQueryClient();
  const { data } = useQuery({ queryKey: ["assistant"], queryFn: () => api.get<AssistantSettings>("/api/settings/assistant") });
  const [form, setForm] = useState<AssistantConfig | null>(null);
  const [apiKey, setApiKey] = useState<string | null>(null); // null: keep the stored key
  const [mode, setMode] = useState<EndpointMode>("auto");
  const [workspace, setWorkspace] = useState("");
  const [customModel, setCustomModel] = useState(false);
  const [busy, setBusy] = useState<"save" | "test" | null>(null);
  const [testResult, setTestResult] = useState<{ ok: boolean; text: string } | null>(null);

  useEffect(() => {
    if (data && !form) {
      setForm(fields(data));
      setMode(endpointMode(data.url));
      setWorkspace(data.url.match(regionURL)?.[1] ?? data.workspaceId);
      setCustomModel(!models.some((m) => m.id === data.model));
    }
  }, [data, form]);

  if (!data || !form) {
    return (
      <Card className="flex h-40 items-center justify-center">
        <Spinner />
      </Card>
    );
  }
  const set = (patch: Partial<AssistantConfig>) => {
    setForm({ ...form, ...patch });
    setTestResult(null);
  };
  const pickModel = (model: string) => {
    const list = voicesFor(model);
    // Voices differ between model families; keep the voice when it exists.
    set({ model, voice: list.some((v) => v.id === form.voice) ? form.voice : list[0].id });
  };
  const pickMode = (m: EndpointMode) => {
    setMode(m);
    if (m === "auto") set({ url: "" });
    else if (m === "custom") set({ url: form.url || data.derivedEndpoint.split("?")[0] });
    else set({ url: buildRegionURL(workspace, m) });
  };
  const changeWorkspace = (id: string) => {
    setWorkspace(id);
    if (mode === "ap-southeast-1" || mode === "cn-beijing") set({ url: buildRegionURL(id, mode) });
  };

  const payload = () => ({ ...form, apiKey: apiKey === null ? undefined : apiKey });
  const dirty = apiKey !== null || JSON.stringify(form) !== JSON.stringify(fields(data));
  const effective = (form.url || data.derivedEndpoint.split("?")[0]) + (form.url.includes("model=") ? "" : `?model=${form.model}`);
  const voices = voicesFor(form.model);

  const save = async () => {
    setBusy("save");
    try {
      const next = await api.put<AssistantSettings>("/api/settings/assistant", payload());
      qc.setQueryData(["assistant"], next);
      setForm(fields(next));
      setApiKey(null);
      toast.ok(next.enabled ? "AI 接听助手已开启" : "AI 接听设置已保存");
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
      const r = await api.post<{ latencyMs: number; format: string }>("/api/settings/assistant/test", payload());
      setTestResult({ ok: true, text: `连接成功：${form.model}，音频格式 ${r.format}，建立会话耗时 ${r.latencyMs} ms` });
    } catch (e) {
      setTestResult({ ok: false, text: (e as Error).message });
    } finally {
      setBusy(null);
    }
  };

  return (
    <Card glow={form.enabled}>
      <CardHeader
        icon={Bot}
        title="AI 接听助手"
        subtitle="来电响铃一会儿没人接时，由 AI 接听、问清楚对方是谁和来意，挂断后在通话记录里留下小结"
        action={<Switch checked={form.enabled} onChange={(v) => set({ enabled: v })} label="自动接听来电" />}
      />
      <CardBody className="space-y-4">
        {data.problem && !dirty && (
          <Alert tone="warn" title="还不能接听">
            {data.problem}
          </Alert>
        )}

        <div className="grid gap-4 sm:grid-cols-3">
          <Field label="响铃多久后接听" hint="这段时间里你可以先在网页上接听，或者点“不用 AI”">
            <Select value={String(form.ringDelaySec)} onChange={(e) => set({ ringDelaySec: Number(e.target.value) })}>
              {[...new Set([0, 5, 10, 15, 20, 30, form.ringDelaySec])]
                .sort((a, b) => a - b)
                .map((s) => (
                  <option key={s} value={s}>
                    {s === 0 ? "立即接听" : `${s} 秒`}
                  </option>
                ))}
            </Select>
          </Field>
          <Field label="声音">
            <Select value={form.voice} onChange={(e) => set({ voice: e.target.value })}>
              {voices.map((v) => (
                <option key={v.id} value={v.id}>
                  {v.name}
                </option>
              ))}
              {!voices.some((v) => v.id === form.voice) && <option value={form.voice}>{form.voice}</option>}
            </Select>
          </Field>
          <Field label="语速" hint="在本机加快播放，音调不变">
            <Select value={String(form.speechRate)} onChange={(e) => set({ speechRate: Number(e.target.value) })}>
              {[...new Set([1, 1.1, 1.2, 1.3, form.speechRate])]
                .sort((a, b) => a - b)
                .map((r) => (
                  <option key={r} value={r}>
                    {r === 1 ? "原速" : `${r} 倍${r === 1.1 ? "（默认）" : ""}`}
                  </option>
                ))}
            </Select>
          </Field>
        </div>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="怎么称呼你" hint="AI 会说“我会转告张先生”；留空就不提名字">
            <Input value={form.ownerName} onChange={(e) => set({ ownerName: e.target.value })} maxLength={20} placeholder="例如 张先生、王姐" />
          </Field>
          <Field label="说话风格">
            <Segmented
              value={form.style}
              onChange={(v) => set({ style: v })}
              options={[
                { value: "warm", label: "亲切" },
                { value: "formal", label: "正式" },
                { value: "brief", label: "简洁" },
              ]}
            />
          </Field>
        </div>
        <Field
          label="开场白"
          hint={
            form.discloseAI
              ? "接通后 AI 先说的话，例如“喂，您好，请问是哪位呀？”。说完它会接着表明自己是 AI 助手"
              : "接通后 AI 先说的话。像平常接电话一样就好，例如“喂，您好，请问是哪位呀？”。不用介绍自己是谁；对方认真问是不是 AI 时，它会如实回答"
          }
        >
          <Input value={form.greeting} onChange={(e) => set({ greeting: e.target.value })} maxLength={200} />
        </Field>
        <div className="flex items-start justify-between gap-4">
          <div>
            <p className="text-sm font-medium">接通后主动说明自己是 AI</p>
            <p className="mt-0.5 text-xs text-muted">有些地方的法规要求 AI 接听电话时主动表明身份，请按你所在地区的要求设置</p>
          </div>
          <Switch checked={form.discloseAI} onChange={(v) => set({ discloseAI: v })} label="接通后主动说明自己是 AI" />
        </div>
        <Field label="补充说明（可选）" hint="告诉 AI 怎么应对常见来电，例如：快递外卖请放门口；工作的事请发邮件。来电者可能套出这里写的内容，不要写住址、行程、证件号这类信息">
          <Textarea rows={3} value={form.instructions} onChange={(e) => set({ instructions: e.target.value })} maxLength={2000} placeholder="快递请放在门口，不用等我。" />
        </Field>
        <Collapsible
          icon={Cpu}
          title="模型与接口"
          subtitle={`${models.find((m) => m.id === form.model)?.name ?? form.model} · ${{ auto: "跟随智能分类", "ap-southeast-1": "新加坡", "cn-beijing": "北京", custom: "自定义地址" }[mode]}${apiKey === null && data.hasOwnKey ? " · 单独的 API Key" : ""}`}
          defaultOpen={!!data.problem}
        >
          <div className="space-y-2">
            <div className="text-[13px] font-medium">实时语音模型</div>
            <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-5">
              {models.map((m) => {
                const active = !customModel && form.model === m.id;
                return (
                  <button
                    key={m.id}
                    type="button"
                    onClick={() => {
                      setCustomModel(false);
                      pickModel(m.id);
                    }}
                    className={clsx("rounded-2xl px-3.5 py-3 text-left ring-1 transition", active ? "bg-accent/10 ring-accent/50" : "bg-panel-2 ring-line hover:ring-line-strong")}
                  >
                    <div className="flex items-center justify-between gap-2 text-sm font-medium">
                      {m.name}
                      {active && <CheckCircle2 className="size-4 shrink-0 text-accent" />}
                    </div>
                    <div className="mt-0.5 text-[11px] text-muted">{m.note}</div>
                  </button>
                );
              })}
              <button
                type="button"
                onClick={() => setCustomModel(true)}
                className={clsx("rounded-2xl px-3.5 py-3 text-left ring-1 transition", customModel ? "bg-accent/10 ring-accent/50" : "bg-panel-2 ring-line hover:ring-line-strong")}
              >
                <div className="flex items-center justify-between gap-2 text-sm font-medium">
                  自定义
                  {customModel && <CheckCircle2 className="size-4 shrink-0 text-accent" />}
                </div>
                <div className="mt-0.5 text-[11px] text-muted">填写其他实时模型或快照版本</div>
              </button>
            </div>
            {customModel && <Input value={form.model} onChange={(e) => pickModel(e.target.value.trim())} placeholder="qwen3.8-omni-flash-realtime-2026-xx-xx" className="font-mono" />}
          </div>

          <div className="space-y-2">
            <div className="text-[13px] font-medium">接口地址</div>
            <Segmented
              size="sm"
              value={mode}
              onChange={pickMode}
              options={[
                { value: "auto", label: "跟随智能分类" },
                { value: "ap-southeast-1", label: "新加坡" },
                { value: "cn-beijing", label: "北京" },
                { value: "custom", label: "自定义" },
              ]}
            />
            {(mode === "ap-southeast-1" || mode === "cn-beijing") && (
              <Field label="业务空间 ID" hint="在百炼控制台的“业务空间”里查看，形如 ws-xxxxxxxx。北京和新加坡的业务空间和 API Key 不通用">
                <Input value={workspace} onChange={(e) => changeWorkspace(e.target.value.trim())} placeholder="ws-xxxxxxxx" className="font-mono" />
              </Field>
            )}
            {mode === "custom" && <Input value={form.url} onChange={(e) => set({ url: e.target.value.trim() })} placeholder="wss://…/api-ws/v1/realtime" className="font-mono text-xs" />}
            {mode === "auto" && !data.derivedEndpoint && <p className="text-xs text-warn">“智能分类”里还没有百炼的 Base URL，请选择地域并填写业务空间 ID。</p>}
            {(form.url || data.derivedEndpoint) && (
              <p className="font-mono text-[11px] break-all text-subtle">
                实际连接：{effective}
              </p>
            )}
          </div>

          <Field
            label="API Key"
            hint={
              apiKey === null && data.hasOwnKey
                ? `已单独保存：${data.ownKeyHint}。只保存在本机（加密）。`
                : data.hasSharedKey
                  ? "留空则使用“智能分类”里的 API Key。接口地域不同时需要填写对应地域的 Key。"
                  : "“智能分类”里也没有 Key，请在这里填写。"
            }
          >
            {apiKey === null && data.hasOwnKey ? (
              <div className="flex gap-2">
                <Input value={data.ownKeyHint} disabled className="font-mono" />
                <Button onClick={() => setApiKey("")}>更换</Button>
              </div>
            ) : (
              <div className="flex gap-2">
                <Input type="password" value={apiKey ?? ""} onChange={(e) => setApiKey(e.target.value)} placeholder={data.hasSharedKey ? "留空：使用智能分类的 Key" : "sk-..."} className="font-mono" autoComplete="off" />
                {apiKey !== null && (
                  <Button variant="ghost" onClick={() => setApiKey(null)}>
                    取消
                  </Button>
                )}
              </div>
            )}
          </Field>
        </Collapsible>

        <Collapsible icon={ShieldCheck} title="防止恶意消耗" subtitle={`每天最多 ${form.dailyLimit} 通，同一号码 ${form.perCallerMax} 通，每通最多回复 ${form.maxTurns} 次、${form.maxDurationSec} 秒`}>
          <p className="text-xs text-muted">AI 只记录来意，闲聊、问答、让它写东西或改规则一律拒绝，纠缠就挂断。下面的上限超过后不再由 AI 接听，电话按普通来电响铃。</p>
          <div className="grid gap-3 sm:grid-cols-4">
            <Field label="每天最多接听">
              <Input type="number" min={1} max={500} value={form.dailyLimit} onChange={(e) => set({ dailyLimit: num(e.target.value) })} />
            </Field>
            <Field label="同一号码每天最多">
              <Input type="number" min={1} max={50} value={form.perCallerMax} onChange={(e) => set({ perCallerMax: num(e.target.value) })} />
            </Field>
            <Field label="每通最多回复几次">
              <Input type="number" min={3} max={50} value={form.maxTurns} onChange={(e) => set({ maxTurns: num(e.target.value) })} />
            </Field>
            <Field label="每通最长（秒）">
              <Input type="number" min={30} max={600} value={form.maxDurationSec} onChange={(e) => set({ maxDurationSec: num(e.target.value) })} />
            </Field>
          </div>
        </Collapsible>

        <Collapsible icon={Mic} title="通话录音" subtitle={form.record === "off" ? "不录音" : `${form.record === "all" ? "录下所有通话" : "录下 AI 接听的通话"}，保留 ${form.keepDays} 天`}>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="通话录音" hint="录音保存在本机数据目录的 recordings/ 下，可以在“电话”页播放和下载。录音前请留意当地法律对告知对方的要求">
              <Select value={form.record} onChange={(e) => set({ record: e.target.value as AssistantConfig["record"] })}>
                <option value="assistant">录下 AI 接听的通话</option>
                <option value="all">录下所有通话</option>
                <option value="off">不录音</option>
              </Select>
            </Field>
            <Field label="录音保留天数" hint="超过天数的录音自动删除，通话文字记录保留">
              <Input type="number" min={1} max={3650} value={form.keepDays} disabled={form.record === "off"} onChange={(e) => set({ keepDays: num(e.target.value) })} />
            </Field>
          </div>
        </Collapsible>

        <p className="flex items-start gap-1.5 text-[11px] leading-relaxed text-subtle">
          <ShieldAlert className="mt-px size-3.5 shrink-0" />
          通话的声音会实时发送到阿里云百炼的语音模型，无法像短信那样先去除敏感信息；通话文字记录和小结只保存在本机。小结使用“智能分类”里的文本模型，按 Token 计费。
        </p>
        {testResult && <p className={clsx("rounded-xl px-3 py-2 text-xs", testResult.ok ? "bg-ok/10 text-ok" : "bg-bad/10 text-bad")}>{testResult.text}</p>}
        <div className="flex flex-wrap justify-end gap-2">
          <Button icon={FlaskConical} loading={busy === "test"} onClick={test}>
            测试连接
          </Button>
          <Button variant="primary" icon={Save} loading={busy === "save"} disabled={!dirty} onClick={save}>
            保存
          </Button>
        </div>
      </CardBody>
    </Card>
  );
}
