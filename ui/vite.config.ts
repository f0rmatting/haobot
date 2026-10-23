import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// The build lands in the Go package that embeds it; `pnpm dev` proxies the
// API to an agent running locally, on :9081 unless HAOBOT_PORT says otherwise.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    outDir: "../internal/agent/web/dist",
    emptyOutDir: true,
    chunkSizeWarningLimit: 800,
  },
  server: {
    proxy: { "/api": { target: `https://127.0.0.1:${process.env.HAOBOT_PORT || 9081}`, changeOrigin: false, ws: true, secure: false } },
  },
});
