/// <reference types="vitest/config" />
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

const apiTarget = process.env.VITE_AFFENT_API_TARGET || process.env.AFFENT_WEBUI_API_TARGET || "http://127.0.0.1:7777";
const devPort = Number.parseInt(process.env.VITE_AFFENT_WEBUI_PORT || "18789", 10);
const allowedHosts = parseAllowedHosts(process.env.VITE_AFFENT_ALLOWED_HOSTS);

// base: "./" keeps asset URLs relative so the built bundle works whether
// affentserve embeds it at "/" or a deployment serves it under a subpath.
export default defineConfig({
  base: "./",
  cacheDir: ".vite-cache",
  plugins: [react()],
  server: {
    host: "0.0.0.0",
    port: Number.isFinite(devPort) ? devPort : 18789,
    strictPort: true,
    ...(allowedHosts == null ? {} : { allowedHosts }),
    proxy: {
      "/v1": {
        target: apiTarget,
        changeOrigin: true,
      },
      "/healthz": {
        target: apiTarget,
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: "dist",
    sourcemap: true,
  },
  test: {
    globals: true,
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    include: ["src/**/*.{test,spec}.{ts,tsx}"],
  },
});

function parseAllowedHosts(value: string | undefined): true | string[] | undefined {
  if (!value) return undefined;
  const normalized = value.trim().toLowerCase();
  if (normalized === "true" || normalized === "1" || normalized === "*") return true;
  if (normalized === "false" || normalized === "0") return undefined;
  return value.split(",").map((host) => host.trim()).filter(Boolean);
}
