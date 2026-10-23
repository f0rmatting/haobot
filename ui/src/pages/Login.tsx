import { useState, type FormEvent } from "react";
import { Lock, ShieldCheck } from "lucide-react";
import { api, ApiError } from "../lib/api";
import { Brand } from "../components/Layout";
import { Button, Input } from "../components/ui";

export function Login({ onLogin }: { onLogin: () => void }) {
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await api.post("/api/login", { password });
      onLogin();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "登录失败");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="relative flex min-h-full items-center justify-center px-4 py-12">
      <div className="grid-lines pointer-events-none absolute inset-0 [mask-image:radial-gradient(ellipse_at_center,black_20%,transparent_70%)] opacity-60" />
      <div className="animate-in relative w-full max-w-sm">
        <div className="mb-8 flex justify-center">
          <Brand />
        </div>
        <form onSubmit={submit} className="glass space-y-5 rounded-3xl p-7">
          <div>
            <h1 className="text-xl font-semibold tracking-tight">欢迎回来</h1>
            <p className="mt-1 text-sm text-muted">输入管理员密码，进入短信与设备控制台。</p>
          </div>
          <div className="space-y-1.5">
            <label htmlFor="password" className="text-[13px] font-medium">
              管理员密码
            </label>
            <div className="relative">
              <Lock className="pointer-events-none absolute top-1/2 left-3.5 size-4 -translate-y-1/2 text-subtle" />
              <Input id="password" type="password" autoComplete="current-password" autoFocus value={password} onChange={(e) => setPassword(e.target.value)} className="h-11 pl-10" placeholder="••••••••" />
            </div>
            {error && <p className="text-xs text-bad">{error}</p>}
          </div>
          <Button type="submit" variant="primary" size="lg" className="w-full" loading={busy} disabled={!password}>
            登录
          </Button>
          <p className="flex items-start gap-2 text-xs leading-relaxed text-muted">
            <ShieldCheck className="mt-0.5 size-3.5 shrink-0 text-ok" />
            <span>
              首次启动时的随机密码打印在终端或日志里。忘记密码可在服务器上执行 <code className="font-mono text-fg">haobot-agent passwd</code> 重置。
            </span>
          </p>
        </form>
      </div>
    </div>
  );
}
