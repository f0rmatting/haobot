// Typed client for the agent's JSON API (internal/agent/web).

export class ApiError extends Error {
  status: number;
  code?: string;
  constructor(status: number, message: string, code?: string) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const init: RequestInit = { method, headers: {}, credentials: "same-origin" };
  if (body !== undefined) {
    init.body = JSON.stringify(body);
    (init.headers as Record<string, string>)["Content-Type"] = "application/json";
  }
  let resp: Response;
  try {
    resp = await fetch(path, init);
  } catch {
    throw new ApiError(0, "无法连接到号小助服务，请检查服务是否在运行");
  }
  const text = await resp.text();
  const data = text ? JSON.parse(text) : {};
  if (!resp.ok) {
    if (resp.status === 401 && path !== "/api/login") {
      window.dispatchEvent(new Event("haobot:unauthorized"));
    }
    throw new ApiError(resp.status, data.error ?? `请求失败（${resp.status}）`, data.code);
  }
  return data as T;
}

export const api = {
  get: <T>(path: string) => request<T>("GET", path),
  post: <T = { ok: boolean }>(path: string, body: unknown = {}) => request<T>("POST", path, body),
  put: <T = { ok: boolean }>(path: string, body: unknown) => request<T>("PUT", path, body),
  patch: <T>(path: string, body: unknown) => request<T>("PATCH", path, body),
  del: <T = { ok: boolean }>(path: string) => request<T>("DELETE", path),
};

export interface Cell {
  valid: boolean;
  state: string;
  rat: string;
  mcc: string;
  mnc: string;
  cellId: string;
  pci: number;
  earfcn: number;
  band: number;
  tac: string;
  rsrp: number;
  rsrq: number;
  rssi: number;
  sinr: number;
  raw: string;
}

export interface ModemStatus {
  model: string;
  revision: string;
  sim: string;
  pinRetries: number;
  pukRetries: number;
  operator: string;
  accessTech: string;
  band: string;
  registration: string;
  registered: boolean;
  csq: number;
  signalDbm: number;
  storageUsed: number;
  storageTotal: number;
  functionality: number;
  pinLock: number;
  attached: number;
  apn: string;
  ip: string;
  scanMode: number;
  cell: Cell;
  updatedAt: string | null;
}

export interface DeviceStatus {
  id: string; // IMEI; "" while a port has not answered yet
  name: string;
  model: string;
  imei: string;
  enabled: boolean;
  online: boolean;
  connected: boolean;
  endpoint: string;
  lastError: string;
  smsReady: boolean;
  lastSync: string | null;
  lastSeen: string | null;
  pinBlocked: boolean;
  unread: number;
  modem: ModemStatus;
  identity: { imei: string; imsi: string; iccid: string; number: string; smsc: string };
  health: DeviceHealth;
  calls: LiveCall[];
  audio: { active: boolean; listeners: number; error?: string; rxPerSecond: number; maxGapMs: number; micFrames: number; fillFrames: number };
  assistant: AssistantLive;
}

export interface TranscriptLine {
  role: "caller" | "assistant";
  text: string;
  final?: boolean;
  at: string;
}

// The AI assistant's part in the current call.
export interface AssistantLive {
  answerAt: string | null; // when it picks up the ringing call
  active: boolean;
  recordId: number;
  phase: "" | "connecting" | "talking" | "ending";
  error: string;
  lines: TranscriptLine[];
}

export interface CallSummary {
  caller?: string;
  purpose?: string;
  urgency?: string;
  callback?: string;
  category?: string;
  brief?: string;
  error?: string;
  usage?: { inputTokens: number; outputTokens: number };
}

export interface AssistantConfig {
  enabled: boolean;
  ringDelaySec: number;
  greeting: string;
  instructions: string;
  model: string;
  voice: string;
  maxDurationSec: number;
  url: string;
  speechRate: number;
  ownerName: string;
  style: "warm" | "formal" | "brief";
  discloseAI: boolean;
  dailyLimit: number;
  perCallerMax: number;
  maxTurns: number;
  record: "off" | "assistant" | "all";
  keepDays: number;
}

export interface AssistantSettings extends AssistantConfig {
  endpoint: string;
  derivedEndpoint: string;
  workspaceId: string;
  models: string[];
  hasOwnKey: boolean;
  ownKeyHint: string;
  hasSharedKey: boolean;
  problem: string;
}

// Named rows carry the phone book's name for their number: the user's own
// contact, or the built-in yellow pages.
export interface Named {
  name?: string;
  nameSource?: "contact" | "yellowpages";
  // avatar is a picture URL: the contact's photo, the yellow pages logo, or
  // the logo of the organisation an SMS is signed by (【招商银行】).
  avatar?: string;
}

export interface LiveCall extends Named {
  id: number;
  incoming: boolean;
  state: "active" | "held" | "dialing" | "alerting" | "incoming" | "waiting";
  number: string;
  startedAt: string;
  answeredAt: string | null;
  recordId: number;
}

export interface CallRecord extends Named {
  id: number;
  deviceId: string;
  peer: string;
  number: string;
  direction: "in" | "out";
  status: "ringing" | "dialing" | "active" | "completed" | "missed" | "rejected" | "canceled";
  startedAt: string;
  answeredAt: string | null;
  endedAt: string | null;
  durationSec: number;
  seen: boolean;
  handledBy: "" | "assistant";
  hasRecording: boolean;
  summary: CallSummary | null;
  transcript: TranscriptLine[] | null;
}

export interface Forwarding {
  reason: number;
  name: string;
  enabled: boolean;
  number: string;
}

export interface Recovery {
  at: string;
  action: string;
  reason: string;
  error?: string;
}

export interface SelfTestResult {
  state: "running" | "passed" | "failed";
  to: string;
  startedAt: string;
  finishedAt: string | null;
  delaySec: number;
  detail: string;
}

export interface DeviceHealth {
  level: "ok" | "warn" | "bad";
  summary: string;
  lastPing: string | null;
  pingLatencyMs: number;
  pingFailures: number;
  unregisteredSince: string | null;
  recoveries: Recovery[];
  autoRecover: boolean;
  suspended: boolean;
  selfTest: SelfTestResult | null;
  selfTestTarget: string;
}

export interface Status {
  devices: DeviceStatus[];
  orphans: number; // messages from before devices were tracked, not yet assigned
  missedCalls: number; // missed calls nobody has looked at
  messages: { total: number; unread: number; today: number; pending: number; categories: Record<string, number> };
  classifier: { enabled: boolean; ready: boolean; lastError: string };
  scanModes: Record<string, string>;
}

export interface Message extends Named {
  id: number;
  sender: string;
  body: string;
  sentAt: string;
  receivedAt: string;
  parts: number;
  complete: boolean;
  binary: boolean;
  kind: "sms" | "mms";
  mmsUrl?: string;
  mmsSize?: number;
  mmsExpiry?: string;
  category: string;
  categorySource: "" | "rule" | "llm" | "manual";
  read: boolean;
  deviceId: string;
  peer: string;
  direction: "in" | "out";
  status?: "sending" | "sent" | "failed";
  error?: string;
  code?: string;
}

export interface Conversation extends Named {
  deviceId: string;
  peer: string;
  total: number;
  unread: number;
  lastAt: string;
  last: Message;
}

export interface Page<T> {
  items: T[];
  total: number;
  page: number;
  pageSize: number;
}

export type MessagePage = Page<Message>;

export interface Finding {
  kind: string;
  count: number;
}

export interface Category {
  name: string;
  description: string;
}

export interface ClassifierConfig {
  enabled: boolean;
  provider: "openai" | "anthropic";
  baseUrl: string;
  model: string;
  categories: Category[];
}

export interface ClassifierSettings extends ClassifierConfig {
  hasApiKey: boolean;
  apiKeyHint: string;
  defaultCategories: Category[];
  status: { lastRun: string; lastError: string; lastErrorAt: string; classified: number; byModel: number };
}

export interface SIMSettings {
  autoUnlock: boolean;
  hasPin: boolean;
}

export interface ActivityBucket {
  start: string;
  sms: number;
  spam: number;
  codes: number;
  calls: number;
  ai: number;
  missed: number;
}

export interface Activity {
  hours: ActivityBucket[]; // last 24 hours, oldest first
  days: ActivityBucket[]; // last 7 days, oldest first
}

// Callback is a call still waiting for a call back.
export interface Callback extends CallRecord {
  reason: string;
  callback: string;
  urgency: string;
}

export interface LogEvent {
  id: number;
  at: string;
  level: "info" | "warn" | "error";
  text: string;
}

export interface ClassifyLog {
  id: number;
  messageId: number;
  at: string;
  source: "rule" | "llm" | "manual";
  category: string;
  rule?: string;
  provider?: string;
  model?: string;
  endpoint?: string;
  system?: string;
  prompt?: string;
  reply?: string;
  confidence: number;
  latencyMs: number;
  error?: string;
  sender?: string;
  body?: string;
}

export interface NotifyRules {
  sms: boolean;
  maskCodes: boolean;
  skipCategories: string[];
  ringing: boolean;
  missed: boolean;
  summaries: boolean;
  skipCallCategories: string[];
  skipUrgentToo: boolean;
}

export interface TelegramConfig {
  enabled: boolean;
  chatId: string;
  proxy: string;
  apiBase: string;
}

export interface TelegramSettings extends TelegramConfig {
  hasToken: boolean;
  tokenHint: string;
}

export type WebhookKind = "feishu" | "dingtalk" | "wecom";

export interface WebhookSettings {
  enabled: boolean;
  hasUrl: boolean;
  urlHint: string;
  hasSecret: boolean;
}

export interface NotifySettings {
  rules: NotifyRules;
  callCategories: string[];
  telegram: TelegramSettings;
  feishu: WebhookSettings;
  dingtalk: WebhookSettings;
  wecom: WebhookSettings;
}

// Unclassified stands for SMS pushed without a category in skipCategories.
export const Unclassified = "_unclassified";

export interface TelegramChat {
  id: number;
  type: "private" | "group" | "supergroup" | "channel";
  title: string;
}

export interface Contact {
  id: number;
  name: string;
  number: string;
  note: string;
  avatar?: string;
  updatedAt: string;
}

export interface YellowNumber {
  number: string;
  label?: string;
}

// YellowOrg is an organisation of the yellow pages as the user sees it.
export interface YellowOrg {
  // id is the user's change to it; absent for a built-in one as it ships.
  id?: number;
  // builtin is the name it ships with; absent for the user's own.
  builtin?: string;
  name: string;
  category: string;
  logo?: string; // picture URL
  numbers: YellowNumber[];
  edited?: boolean; // a built-in organisation the user changed
}

export interface YellowPages {
  source: string;
  orgs: YellowOrg[];
  hidden: YellowOrg[]; // built-in organisations the user removed
}

// ImportResult counts what an import of contacts or yellow pages did.
export interface ImportResult {
  added: number;
  updated: number;
  skipped: number;
}
