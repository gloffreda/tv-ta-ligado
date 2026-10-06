import { chromium, expect, test, type Page } from "@playwright/test";
import * as fs from "node:fs";
import { assertStaging, BASE, bubbleLog, check404s, log, shots, tvtl, watchBubbles } from "./helpers";

const MIN = Number(process.env.E2E_MIN ?? 30);
const TOKEN = process.env.TVTL_STAGING_ADMIN_TOKEN ?? "";

type Item = { id: number; kind: string; starts_at: string; ends_at: string; lines: unknown[] | null };

function pairDiffs(a: { key: string; wall: number }[], b: { key: string; wall: number }[]) {
  const mb = new Map(b.filter((x) => x.key).map((x) => [x.key, x.wall]));
  const out: number[] = [];
  for (const x of a) if (x.key && mb.has(x.key)) out.push(Math.abs(x.wall - mb.get(x.key)!));
  return out.slice(1); // a primeira pode ser a fala em curso na abertura
}
const pct = (xs: number[], ok: (x: number) => boolean) => (xs.length ? (100 * xs.filter(ok).length) / xs.length : 0);
const p95 = (xs: number[]) => (xs.length ? [...xs].sort((a, b) => a - b)[Math.floor(xs.length * 0.95)] : 0);

test("homologação: sessão pelo botão, sincronia, balões, som, pixel x humano e 30 min no ar", async ({ page: A, browser }) => {
  assertStaging();
  expect(TOKEN, "token da homologação").not.toBe("");
  const summary: Record<string, unknown> = { base: BASE, minutes: MIN };

  // 1. Página: HTTPS, mudo, selo, 404s.
  const resp = await A.goto("/");
  expect(resp?.status()).toBe(200);
  expect(A.url().startsWith("https://")).toBe(true);
  await expect(A.locator("[data-badge]")).toHaveText(/EM TESTE/);
  await expect(A.locator("#btnMute")).toHaveText("Som: desligado");
  await check404s(A);

  // 2. Liga a sessão pelo botão (painel na página, token + LIGAR).
  const st0 = await (await A.request.get("/v1/session")).json();
  expect(st0.active, "homologação deve começar em repouso").toBe(false);
  await shots(A, "homolog-fora-do-ar");
  await A.getByRole("button", { name: "Ligar a TV" }).click();
  const dialog = A.getByRole("dialog");
  await expect(dialog).toContainText(`${st0.max_min} min`);
  await expect(dialog).toContainText("US$");
  await expect(dialog.getByRole("button", { name: "Confirmar" })).toBeDisabled();
  await dialog.getByLabel("Token de administração").fill(TOKEN);
  await dialog.getByLabel("Digite LIGAR para confirmar").fill("LIGAR");
  const tClick = Date.now();
  await dialog.getByRole("button", { name: "Confirmar" }).click();
  await expect(A.locator("[data-offair]")).toHaveCount(0, { timeout: 30000 });
  await expect(A.locator(".bubble")).toBeVisible({ timeout: 180000 });
  summary.click_to_first_bubble_ms = Date.now() - tClick;
  const ls = await A.evaluate(() => localStorage.length ? Object.keys(localStorage) : []);
  expect(ls.join(","), "token nunca vai para o localStorage").not.toMatch(/tok|token/i);
  summary.localStorage_keys = ls;

  // 3. Segundo navegador e a página /humano.
  const browser2 = await chromium.launch({ args: ["--use-gl=swiftshader", "--enable-unsafe-swiftshader"] });
  const B = await (await browser2.newContext({ baseURL: BASE })).newPage();
  const C = await (await browser.newContext({ baseURL: BASE })).newPage();
  await B.goto("/");
  await C.goto("/humano");
  await expect(B.locator(".bubble")).toBeVisible({ timeout: 60000 });
  await expect(C.locator("svg[data-renderer=vector]")).toBeVisible({ timeout: 30000 });
  await expect(A.locator("canvas[data-renderer=pixel]")).toBeVisible();
  for (const p of [A, B, C]) await watchBubbles(p);

  // 4. Capturas ao vivo (desktop 1440 e celular 390), pixel e humano.
  await shots(A, "homolog-pixel");
  await shots(C, "homolog-humano");

  // 5. Balões: desligar esconde, ligar mostra.
  await A.locator("#btnCap").click();
  await expect(A.locator(".bubble")).toHaveCount(0);
  await expect(A.locator("#btnCap")).toHaveText("Balões: desligados");
  await A.locator("#btnCap").click();
  await expect(A.locator(".bubble")).toBeVisible({ timeout: 20000 });

  // 6. Som: liga no ponto certo da fala (não recomeça).
  await B.getByRole("button", { name: "Ligar o som" }).click();
  await expect(B.locator("#btnMute")).toHaveText("Som: ligado");
  const drifts: number[] = [];
  for (let i = 0; i < 40; i++) {
    await B.waitForTimeout(500);
    const t = await tvtl(B);
    if (typeof t.audioPos === "number" && typeof t.posMs === "number" && t.posMs > 300) drifts.push(Math.abs((t.audioPos as number) - (t.posMs as number)));
  }
  summary.audio_drift_samples = drifts.length;
  summary.audio_drift_p95_ms = p95(drifts);
  expect.soft(drifts.length, "áudio tocando").toBeGreaterThan(5);
  expect.soft(p95(drifts), "áudio alinhado ao relógio (p95)").toBeLessThan(250);

  // 7. Rodada longa: amostra bocas de / e /humano no mesmo instante.
  const end = Date.now() + MIN * 60000;
  let mouthSame = 0, mouthTotal = 0, offAirSamples = 0, samples = 0;
  while (Date.now() < end) {
    const [ta, tc] = await Promise.all([tvtl(A), tvtl(C)]);
    samples++;
    if (!ta.lineKey) offAirSamples++;
    // só compara amostras do mesmo instante do canal (até 2 quadros de diferença)
    if (ta.lineKey && ta.lineKey === tc.lineKey && ta.speaker && Math.abs((ta.now as number) - (tc.now as number)) < 34) {
      const ma = (ta.mouths as Record<string, string>)?.[ta.speaker as string];
      const mc = (tc.mouths as Record<string, string>)?.[tc.speaker as string];
      if (ma && mc) {
        mouthTotal++;
        if (ma === mc) mouthSame++;
      }
    }
    if (samples % 30 === 0) log("progresso", { samples, mouthSame, mouthTotal, offAirSamples, lineKey: ta.lineKey });
    await A.waitForTimeout(2000);
  }

  // 8. Métricas de sincronia e balões.
  const [la, lb, lc] = await Promise.all([bubbleLog(A), bubbleLog(B), bubbleLog(C)]);
  const ab = pairDiffs(la, lb);
  const ac = pairDiffs(la, lc);
  const late = la.filter((x) => x.key && x.lineStart).slice(1).map((x) => Math.abs(x.chan - x.lineStart));
  const facts = [...la, ...lb].filter((x) => x.type === "fact");
  Object.assign(summary, {
    lines_seen: la.filter((x) => x.key).length,
    two_browsers_pairs: ab.length, two_browsers_within_250_pct: pct(ab, (d) => d < 250), two_browsers_p95_ms: p95(ab),
    pixel_vs_humano_pairs: ac.length, pixel_vs_humano_within_250_pct: pct(ac, (d) => d < 250), pixel_vs_humano_p95_ms: p95(ac),
    bubble_vs_line_start_within_250_pct: pct(late, (d) => d < 250), bubble_vs_line_start_p95_ms: p95(late),
    fact_bubbles: facts.length, fact_bubbles_with_source_pct: pct(facts.map((f) => (f.hasSource ? 1 : 0)), (x) => x === 1),
    mouth_samples: mouthTotal, mouth_same_pct: mouthTotal ? (100 * mouthSame) / mouthTotal : 0,
    samples, off_air_samples: offAirSamples,
  });
  expect.soft(pct(ab, (d) => d < 250), "dois navegadores < 250 ms").toBeGreaterThanOrEqual(95);
  expect.soft(pct(ac, (d) => d < 250), "/ e /humano < 250 ms").toBeGreaterThanOrEqual(95);
  expect.soft(pct(late, (d) => d < 250), "balão alinhado ao line_started").toBeGreaterThanOrEqual(95);
  expect.soft(facts.length, "houve falas fact").toBeGreaterThan(0);
  expect.soft(pct(facts.map((f) => (f.hasSource ? 1 : 0)), (x) => x === 1), "FONTE em toda fala fact").toBe(100);
  expect.soft(mouthTotal ? (100 * mouthSame) / mouthTotal : 0, "mesmo visema nas duas páginas").toBeGreaterThanOrEqual(90);

  // 9. Composição do ar e buracos (pela linha do tempo do servidor).
  const ses = (await (await A.request.get("/v1/session")).json()).last;
  const from = Date.parse(ses.started_at);
  const to = Date.now() + (await A.evaluate(() => (window as unknown as { __tvtl: { offset: number } }).__tvtl.offset));
  const tl = (await (await A.request.get(`/v1/timeline?from=${from}&to=${Math.floor(to)}`)).json()).items as Item[];
  const comp: Record<string, number> = {};
  const compAfter15: Record<string, number> = {};
  let gaps = 0, gapMs = 0, prevEnd = 0;
  for (const it of tl ?? []) {
    const s = Math.max(Date.parse(it.starts_at), from), e = Math.min(Date.parse(it.ends_at), to);
    if (e <= s) continue;
    comp[it.kind] = (comp[it.kind] ?? 0) + (e - s);
    const s15 = Math.max(s, from + 15 * 60000);
    if (e > s15) compAfter15[it.kind] = (compAfter15[it.kind] ?? 0) + (e - s15);
    if (prevEnd && s - prevEnd > 1000) (gaps++, (gapMs += s - prevEnd));
    prevEnd = Math.max(prevEnd, e);
  }
  const share = (c: Record<string, number>) => {
    const tot = Object.values(c).reduce((a, b) => a + b, 0) || 1;
    return Object.fromEntries(Object.entries(c).map(([k, v]) => [k, Math.round((1000 * v) / tot) / 10]));
  };
  Object.assign(summary, { session: ses, composition_pct: share(comp), composition_after_15min_pct: share(compAfter15), gaps, gap_ms: gapMs });
  if (MIN >= 20) expect.soft(share(compAfter15).bumper ?? 0, "vinheta < 5% depois de 15 min").toBeLessThan(5);
  expect.soft(gaps, "sem buraco de silêncio não planejado").toBe(0);

  // 10. Desligar (livre, sem confirmação) e os dois voltam a FORA DO AR.
  await shots(A, "homolog-pixel-fim");
  await A.getByRole("button", { name: "Desligar" }).click();
  await expect(A.locator("[data-offair] h2")).toHaveText("FORA DO AR", { timeout: 30000 });
  await expect(B.locator("[data-offair] h2")).toHaveText("FORA DO AR", { timeout: 30000 });
  const st1 = await (await A.request.get("/v1/session")).json();
  summary.session_end = st1.last;
  expect(st1.active).toBe(false);
  expect(st1.last.end_reason).toBe("button");
  fs.writeFileSync("/out/e2e/resumo.json", JSON.stringify(summary, null, 2));
  await browser2.close();
});
