import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, type Status } from "./api";

export function useStatus() {
  return useQuery({ queryKey: ["status"], queryFn: () => api.get<Status>("/api/status"), refetchInterval: 30000 });
}

// useLiveUpdates listens to the agent's event stream and refetches whatever
// changed. It reports whether the stream is connected.
export function useLiveUpdates(enabled: boolean): boolean {
  const qc = useQueryClient();
  const [live, setLive] = useState(false);
  useEffect(() => {
    if (!enabled) return;
    const es = new EventSource("/api/stream");
    es.onopen = () => setLive(true);
    es.onerror = () => setLive(false);
    es.addEventListener("status", () => qc.invalidateQueries({ queryKey: ["status"] }));
    es.addEventListener("messages", () => {
      for (const key of ["messages", "message", "conversations", "thread", "status", "activity"]) qc.invalidateQueries({ queryKey: [key] });
    });
    es.addEventListener("events", () => qc.invalidateQueries({ queryKey: ["events"] }));
    es.addEventListener("calls", () => {
      qc.invalidateQueries({ queryKey: ["calls"] });
      qc.invalidateQueries({ queryKey: ["status"] });
      qc.invalidateQueries({ queryKey: ["activity"] });
      qc.invalidateQueries({ queryKey: ["callbacks"] });
    });
    return () => {
      es.close();
      setLive(false);
    };
  }, [enabled, qc]);
  return live;
}

// useNow re-renders every interval so relative times stay fresh.
export function useNow(intervalMs = 30000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), intervalMs);
    return () => clearInterval(id);
  }, [intervalMs]);
  return now;
}
