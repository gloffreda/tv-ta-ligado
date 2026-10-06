import { expect, test } from "@playwright/test";
import { BASE, shots } from "./helpers";

// Portão do site. NUNCA clica em "Confirmar": não liga sessão (pode rodar na
// produção). A senha vem de E2E_PASSWORD (ambiente, nunca do .env).
const PW = process.env.E2E_PASSWORD ?? "";

test("portão: sem login vai para /entrar; API e mídia 401; login, painel sem token e Sair", async ({ page, context }) => {
  expect(BASE.startsWith("https://")).toBe(true);
  // sem cookie
  const r = await page.request.get("/", { maxRedirects: 0 });
  expect(r.status()).toBe(302);
  expect(r.headers()["location"]).toMatch(/\/entrar$/);
  expect((await page.request.get("/humano", { maxRedirects: 0 })).status()).toBe(302);
  for (const p of ["/v1/now", "/v1/timeline", "/v1/schedule", "/v1/session", "/media/" + "a".repeat(64) + ".ogg"]) {
    expect((await page.request.get(p)).status(), p).toBe(401);
  }
  expect((await page.request.post("/v1/session/start", { data: {}, headers: { Origin: BASE } })).status()).toBe(401);
  expect((await page.request.get("/healthz")).status()).toBe(200);

  await page.goto("/");
  await expect(page).toHaveURL(/\/entrar$/);
  await expect(page.getByLabel("Senha")).toBeVisible();
  await shots(page, "entrar");

  test.skip(!PW, "sem E2E_PASSWORD: só a parte sem login");
  await page.getByLabel("Senha").fill("senha-errada-de-proposito");
  await page.getByRole("button", { name: "Entrar" }).click();
  await expect(page.getByRole("alert")).toContainText(/incorreta/);

  await page.getByLabel("Senha").fill(PW);
  await page.getByRole("button", { name: "Entrar" }).click();
  await expect(page).toHaveURL(new RegExp(BASE.replace(/[.]/g, "\\.") + "/$"));
  const c = (await context.cookies()).find((x) => x.name === "tvtl_sess");
  expect(c?.httpOnly).toBe(true);
  expect(c?.secure).toBe(true);
  expect(c?.sameSite).toBe("Strict");
  expect((c!.expires * 1000 - Date.now()) / 86400000).toBeGreaterThan(29.9);
  expect(await page.evaluate(() => document.cookie)).not.toContain("tvtl_sess"); // HttpOnly

  // layout: sem painel lateral; linha de fontes e aviso de IA sempre visíveis
  await expect(page.getByText("Fontes deste bloco")).toHaveCount(0);
  await expect(page.locator("[data-panel=fontes] .lbl")).toHaveText("FONTES");
  await expect(page.locator("[data-ai-notice]")).toHaveText("Conteúdo gerado por IA · vozes e apresentadores sintéticos");
  const tv = await page.locator("#tv").boundingBox();
  expect(tv && Math.abs(tv.width / tv.height - 16 / 9)).toBeLessThan(0.02);

  // em repouso: Ligar a TV abre o painel com limites e só "Confirmar" (não clicamos)
  await expect(page.locator("[data-offair] h2")).toHaveText("FORA DO AR", { timeout: 20000 });
  await page.getByRole("button", { name: "Ligar a TV" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toContainText("60 min");
  await expect(dialog).toContainText("US$ 2.00");
  await expect(dialog.getByRole("button", { name: "Confirmar" })).toBeEnabled();
  expect(await dialog.locator("input").count(), "sem token nem LIGAR").toBe(0);
  await shots(page, "ligar-painel");
  await dialog.getByRole("button", { name: "Cancelar" }).click();
  await expect(dialog).toHaveCount(0);
  expect((await (await page.request.get("/v1/session")).json()).active).toBe(false);

  // Sair → /entrar, e a API volta a pedir login
  await page.getByRole("button", { name: "Sair" }).click();
  await expect(page).toHaveURL(/\/entrar$/);
  expect((await page.request.get("/v1/now")).status()).toBe(401);
});
