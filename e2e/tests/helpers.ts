import { expect, type Page } from "@playwright/test";
import * as fs from "node:fs";

export const BASE = process.env.BASE_URL ?? "";
export const PROD = process.env.PROD_URL ?? "";

// Trava: nada que ligue sessão roda contra produção.
export function assertStaging() {
  if (!BASE || !BASE.startsWith("https://")) throw new Error("BASE_URL precisa ser https");
  if (process.env.PROD_REST) throw new Error("teste de sessão não roda no modo produção-em-repouso");
  if (PROD && BASE.replace(/\/$/, "") === PROD.replace(/\/$/, "")) throw new Error("RECUSADO: BASE_URL é a produção");
}

export function log(name: string, data: unknown) {
  fs.mkdirSync("/out/e2e", { recursive: true });
  fs.appendFileSync(`/out/e2e/${name}.jsonl`, JSON.stringify({ at: new Date().toISOString(), ...(data as object) }) + "\n");
}

export async function tvtl(page: Page) {
  return page.evaluate(() => (window as unknown as { __tvtl?: Record<string, unknown> }).__tvtl ?? {});
}

// Registra, na própria página, cada troca de balão: hora local, chave da fala,
// hora do relógio do canal e início previsto da fala.
export async function watchBubbles(page: Page) {
  await page.evaluate(() => {
    const w = window as unknown as { __log: unknown[]; __tvtl?: Record<string, number | string | null> };
    w.__log = [];
    let last = "";
    const check = () => {
      const b = document.querySelector<HTMLElement>(".bubble");
      const key = b?.dataset.lineKey ?? "";
      if (key !== last) {
        last = key;
        const t = w.__tvtl ?? {};
        w.__log.push({ wall: Date.now(), key, chan: t.now, lineStart: t.lineStart, type: b?.dataset.type ?? null, hasSource: !!b?.querySelector("[data-source]") });
      }
    };
    new MutationObserver(check).observe(document.body, { subtree: true, childList: true, attributes: true, attributeFilter: ["data-line-key"] });
    check();
  });
}

export async function bubbleLog(page: Page) {
  return page.evaluate(() => (window as unknown as { __log: { wall: number; key: string; chan: number; lineStart: number; type: string | null; hasSource: boolean }[] }).__log);
}

export async function shots(page: Page, name: string) {
  for (const w of [1440, 390]) {
    await page.setViewportSize({ width: w, height: w === 1440 ? 900 : 844 });
    await page.waitForTimeout(800);
    await page.screenshot({ path: `/out/screens/${name}-${w}.png`, fullPage: w === 390 ? false : false });
  }
  await page.setViewportSize({ width: 1440, height: 900 });
}

export async function check404s(page: Page) {
  for (const p of ["/v1/admin", "/.env", "/nginx.conf", "/v1/session/start", "/v1/stats", "/etc/passwd", "/healthz", "/index.html", "/media/x.ogg"]) {
    const r = await page.request.get(p);
    expect(r.status(), p).toBe(404);
  }
}
