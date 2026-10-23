import { createContext, useContext, useEffect, useState, type ReactNode } from "react";

export type Mode = "simple" | "pro";
export type Theme = "system" | "dark" | "light";

interface Prefs {
  mode: Mode;
  setMode: (m: Mode) => void;
  theme: Theme;
  setTheme: (t: Theme) => void;
  pro: boolean;
}

const PrefsContext = createContext<Prefs | null>(null);

// Browser storage is only a convenience; it may be unavailable.
function load<T extends string>(key: string, fallback: T, allowed: readonly T[]): T {
  try {
    const v = localStorage.getItem(key) as T | null;
    return v && allowed.includes(v) ? v : fallback;
  } catch {
    return fallback;
  }
}

function save(key: string, value: string) {
  try {
    localStorage.setItem(key, value);
  } catch {
    /* ignore */
  }
}

export function PrefsProvider({ children }: { children: ReactNode }) {
  const [mode, setModeState] = useState<Mode>(() => load("haobot.mode", "simple", ["simple", "pro"] as const));
  const [theme, setThemeState] = useState<Theme>(() => load("haobot.theme", "system", ["system", "dark", "light"] as const));

  useEffect(() => {
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    const apply = () => {
      const dark = theme === "dark" || (theme === "system" && media.matches);
      document.documentElement.dataset.theme = dark ? "dark" : "light";
    };
    apply();
    media.addEventListener("change", apply);
    return () => media.removeEventListener("change", apply);
  }, [theme]);

  const value: Prefs = {
    mode,
    pro: mode === "pro",
    setMode: (m) => {
      setModeState(m);
      save("haobot.mode", m);
    },
    theme,
    setTheme: (t) => {
      setThemeState(t);
      save("haobot.theme", t);
    },
  };
  return <PrefsContext.Provider value={value}>{children}</PrefsContext.Provider>;
}

export function usePrefs(): Prefs {
  const ctx = useContext(PrefsContext);
  if (!ctx) throw new Error("usePrefs outside PrefsProvider");
  return ctx;
}
