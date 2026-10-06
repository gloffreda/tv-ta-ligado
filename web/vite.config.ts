import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  // assetsInlineLimit 0: fontes viram arquivos (a CSP não aceita data: em font-src)
  build: { outDir: "dist", assetsDir: "assets", sourcemap: false, chunkSizeWarningLimit: 1200, assetsInlineLimit: 0,
    rollupOptions: { input: { main: "index.html", entrar: "entrar.html" } } },
  test: { environment: "jsdom", include: ["src/**/*.test.ts", "src/**/*.test.tsx"] },
});
