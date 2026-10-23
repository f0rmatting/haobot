import clsx from "clsx";
import { Cpu, LayoutDashboard, LogOut, MessageSquareText, Monitor, Moon, Phone, ScrollText, Settings, Sparkles, Sun, Wand2 } from "lucide-react";
import { NavLink, Outlet } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { api } from "../lib/api";
import { useStatus } from "../lib/hooks";
import { usePrefs, type Theme } from "../lib/prefs";
import { signal } from "../lib/format";
import { deviceHealth, knownDevices } from "../lib/devices";
import { Dot, IconButton, Segmented } from "./ui";
import { SignalBars } from "./Visuals";
import { CallCenter } from "./CallCenter";

const nav = [
  { to: "/", label: "概览", icon: LayoutDashboard, end: true },
  { to: "/messages", label: "短信", icon: MessageSquareText },
  { to: "/calls", label: "电话", icon: Phone },
  { to: "/device", label: "设备", icon: Cpu },
  { to: "/settings", label: "设置", icon: Settings },
  { to: "/logs", label: "日志", icon: ScrollText },
];

const themeIcon: Record<Theme, typeof Sun> = { system: Monitor, dark: Moon, light: Sun };
const themeNext: Record<Theme, Theme> = { system: "dark", dark: "light", light: "system" };
const themeLabel: Record<Theme, string> = { system: "跟随系统", dark: "深色", light: "浅色" };

export function Layout({ live }: { live: boolean }) {
  const { data: status } = useStatus();
  const { mode, setMode, theme, setTheme } = usePrefs();
  const qc = useQueryClient();
  const unread = status?.messages.unread ?? 0;
  const missed = status?.missedCalls ?? 0;
  const badge = (to: string) => (to === "/messages" ? unread : to === "/calls" ? missed : 0);

  const logout = async () => {
    await api.post("/api/logout").catch(() => {});
    qc.clear();
    window.location.assign("/");
  };

  const ThemeIcon = themeIcon[theme];
  const devices = knownDevices(status).filter((d) => d.enabled);
  const healthy = devices.filter((d) => deviceHealth(d).tone === "ok");
  const single = devices.length === 1 ? devices[0] : null;
  const sig = single?.connected ? signal(single.modem) : null;
  const pillTone = devices.length === 0 ? "bad" : healthy.length === devices.length ? "ok" : healthy.length > 0 ? "warn" : "bad";
  const pillText =
    devices.length === 0 ? "没有可用的模组" : single ? (pillTone === "ok" ? "运行正常" : deviceHealth(single).label) : healthy.length === devices.length ? `${devices.length} 台设备 · 全部正常` : `${healthy.length}/${devices.length} 台正常`;

  return (
    <div className="flex min-h-full">
      {/* Sidebar (desktop) */}
      <aside className="sticky top-0 hidden h-screen w-60 shrink-0 flex-col border-r border-line px-4 py-5 lg:flex">
        <Brand />
        <nav className="mt-8 flex flex-1 flex-col gap-1">
          {nav.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.end}
              className={({ isActive }) =>
                clsx(
                  "group flex items-center gap-3 rounded-xl px-3 py-2.5 text-sm font-medium transition",
                  isActive ? "bg-accent/10 text-fg ring-1 ring-accent/25" : "text-muted hover:bg-panel-2 hover:text-fg",
                )
              }
            >
              {({ isActive }) => (
                <>
                  <item.icon className={clsx("size-[18px]", isActive ? "text-accent" : "text-subtle group-hover:text-fg")} />
                  <span className="flex-1">{item.label}</span>
                  {badge(item.to) > 0 && (
                    <span className={clsx("min-w-5 rounded-full px-1.5 text-center text-[11px] leading-5 font-semibold text-white", item.to === "/calls" ? "bg-bad" : "bg-gradient-accent")}>{badge(item.to) > 99 ? "99+" : badge(item.to)}</span>
                  )}
                </>
              )}
            </NavLink>
          ))}
        </nav>
        <div className="glass space-y-3 rounded-2xl p-3">
          <div className="text-[11px] font-medium tracking-wide text-subtle uppercase">界面模式</div>
          <Segmented
            size="sm"
            value={mode}
            onChange={setMode}
            options={[
              { value: "simple", label: "简洁", icon: Sparkles },
              { value: "pro", label: "专业", icon: Wand2 },
            ]}
          />
          <p className="text-[11px] leading-relaxed text-muted">{mode === "simple" ? "只显示常用功能，适合日常使用。" : "显示 AT 控制台、射频参数、原始数据等高级功能。"}</p>
        </div>
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        {/* Top bar */}
        <header className="sticky top-0 z-30 border-b border-line bg-bg/70 backdrop-blur-xl">
          <div className="flex h-14 items-center gap-3 px-4 sm:px-6">
            <div className="lg:hidden">
              <Brand compact />
            </div>
            <div className="flex min-w-0 flex-1 items-center gap-2 overflow-hidden">
              {status && (
                <NavLink to="/device" className="flex min-w-0 items-center gap-2 rounded-full bg-panel-2 px-3 py-1 text-xs ring-1 ring-line hover:ring-line-strong">
                  <Dot tone={pillTone} pulse={pillTone !== "bad"} />
                  <span className="truncate font-medium">{pillText}</span>
                  {single && sig && (
                    <>
                      <span className="hidden text-line-strong sm:inline">|</span>
                      <span className="hidden truncate text-muted sm:inline">{single.modem.operator || "无运营商"}</span>
                      <SignalBars bars={sig.bars} tone={sig.tone} className="hidden h-3 sm:flex" />
                    </>
                  )}
                </NavLink>
              )}
              <span className={clsx("hidden items-center gap-1.5 text-[11px] text-subtle md:flex")} title={live ? "实时推送已连接" : "实时推送未连接，正在定时刷新"}>
                <span className={clsx("size-1.5 rounded-full", live ? "bg-ok" : "bg-subtle")} />
                {live ? "实时" : "离线刷新"}
              </span>
            </div>
            <div className="lg:hidden">
              <Segmented
                size="sm"
                value={mode}
                onChange={setMode}
                options={[
                  { value: "simple", label: "简洁" },
                  { value: "pro", label: "专业" },
                ]}
              />
            </div>
            <IconButton icon={ThemeIcon} label={`主题：${themeLabel[theme]}（点击切换）`} onClick={() => setTheme(themeNext[theme])} />
            <IconButton icon={LogOut} label="退出登录" onClick={logout} />
          </div>
        </header>

        <main className="mx-auto w-full max-w-7xl flex-1 px-4 pt-5 pb-28 sm:px-6 lg:pb-10">
          <Outlet />
        </main>
      </div>

      <CallCenter />

      {/* Bottom nav (mobile) */}
      <nav className="fixed inset-x-0 bottom-0 z-40 border-t border-line bg-bg/85 pb-[env(safe-area-inset-bottom)] backdrop-blur-xl lg:hidden">
        <div className="mx-auto flex max-w-lg justify-around px-2">
          {nav.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.end}
              className={({ isActive }) => clsx("relative flex flex-1 flex-col items-center gap-1 py-2.5 text-[11px] font-medium", isActive ? "text-accent" : "text-subtle")}
            >
              <item.icon className="size-5" />
              {item.label}
              {badge(item.to) > 0 && (
                <span className={clsx("absolute top-1.5 left-1/2 ml-2 min-w-4 rounded-full px-1 text-center text-[10px] leading-4 font-semibold text-white", item.to === "/calls" ? "bg-bad" : "bg-gradient-accent")}>{badge(item.to) > 99 ? "99+" : badge(item.to)}</span>
              )}
            </NavLink>
          ))}
        </div>
      </nav>
    </div>
  );
}

export function Brand({ compact }: { compact?: boolean }) {
  return (
    <div className="flex items-center gap-2.5">
      <div className="bg-gradient-accent flex size-9 items-center justify-center rounded-xl shadow-lg shadow-cyan-500/25">
        <svg viewBox="0 0 24 24" className="size-5 text-white" fill="currentColor" aria-hidden>
          <path d="M5 17h3v-5H5zm5.5 0h3V9h-3zM16 17h3V6h-3z" />
        </svg>
      </div>
      {!compact && (
        <div>
          <div className="text-[15px] leading-tight font-semibold tracking-tight">号小助</div>
          <div className="text-[11px] text-muted">短信和电话的小助理</div>
        </div>
      )}
    </div>
  );
}
