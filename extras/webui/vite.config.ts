/// <reference types="vitest/config" />
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

const apiTarget = process.env.VITE_AFFENT_API_TARGET || process.env.AFFENT_WEBUI_API_TARGET || "http://127.0.0.1:7777";
const devPort = Number.parseInt(process.env.VITE_AFFENT_WEBUI_PORT || "18789", 10);

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
