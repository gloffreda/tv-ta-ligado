import { defineConfig } from "@playwright/test";

const min = Number(process.env.E2E_MIN ?? 30);
export default defineConfig({
  testDir: "tests",
  timeout: (min + 15) * 60_000,
  workers: 1,
  reporter: [["list"], ["json", { outputFile: "/out/e2e/resultado.json" }]],
  use: {
    baseURL: process.env.BASE_URL,
    ignoreHTTPSErrors: false,
    browserName: "chromium",
    launchOptions: { args: ["--use-gl=swiftshader", "--enable-unsafe-swiftshader"] },
  },
});
