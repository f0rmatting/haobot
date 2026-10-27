import { useEffect } from "react";
import { Route, Routes } from "react-router-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./lib/api";
import { useLiveUpdates } from "./lib/hooks";
import { Layout } from "./components/Layout";
import { Spinner } from "./components/ui";
import { Login } from "./pages/Login";
import { Dashboard } from "./pages/Dashboard";
import { Messages } from "./pages/Messages";
import { Calls } from "./pages/Calls";
import { Contacts } from "./pages/Contacts";
import { Device } from "./pages/Device";
import { SettingsPage } from "./pages/Settings";
import { Logs } from "./pages/Logs";

export function App() {
  const qc = useQueryClient();
  const session = useQuery({ queryKey: ["session"], queryFn: () => api.get<{ authenticated: boolean }>("/api/session"), staleTime: Infinity });
  const authed = session.data?.authenticated === true;
  const live = useLiveUpdates(authed);

  // Any 401 (expired session, password changed elsewhere) returns to login.
  useEffect(() => {
    const onUnauthorized = () => qc.setQueryData(["session"], { authenticated: false });
    window.addEventListener("haobot:unauthorized", onUnauthorized);
    return () => window.removeEventListener("haobot:unauthorized", onUnauthorized);
  }, [qc]);

  if (session.isPending) {
    return (
      <div className="flex h-full items-center justify-center">
        <Spinner className="size-7" />
      </div>
    );
  }
  if (!authed) return <Login onLogin={() => qc.setQueryData(["session"], { authenticated: true })} />;

  return (
    <Routes>
      <Route element={<Layout live={live} />}>
        <Route index element={<Dashboard />} />
        <Route path="messages" element={<Messages />} />
        <Route path="calls" element={<Calls />} />
        <Route path="contacts" element={<Contacts />} />
        <Route path="device" element={<Device />} />
        <Route path="settings" element={<SettingsPage />} />
        <Route path="logs" element={<Logs />} />
        <Route path="*" element={<Dashboard />} />
      </Route>
    </Routes>
  );
}
