import { expect, test } from "@playwright/test";
import { BASE, check404s, shots } from "./helpers";

// Produção SÓ em repouso: carrega, HTTPS, FORA DO AR, selo e 404s. Nunca liga.
test("produção em repouso", async ({ page }) => {
  test.skip(!process.env.PROD_REST, "só no alvo e2e-prod-rest");
  expect(BASE.startsWith("https://")).toBe(true);
  const r = await page.goto("/");
  expect(r?.status()).toBe(200);
  expect(page.url().startsWith("https://")).toBe(true);
  await expect(page.locator("[data-badge]")).toHaveText(/EM TESTE/);
  await expect(page.locator("[data-offair] h2")).toHaveText("FORA DO AR", { timeout: 20000 });
  await expect(page.getByRole("button", { name: "Ligar a TV" })).toBeVisible();
  const s = await (await page.request.get("/v1/session")).json();
  expect(s.active).toBe(false);
  await check404s(page);
  await shots(page, "producao-repouso");
  await page.goto("/humano");
  await expect(page.locator("[data-offair] h2")).toHaveText("FORA DO AR", { timeout: 20000 });
  await shots(page, "producao-repouso-humano");
});
