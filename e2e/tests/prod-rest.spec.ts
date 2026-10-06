import { expect, test } from "@playwright/test";
import { BASE, check404s, shots } from "./helpers";

// Produção SÓ em repouso: carrega, HTTPS, FORA DO AR, selo e 404s. Nunca liga.
test("produção em repouso", async ({ page }) => {
  test.skip(!process.env.PROD_REST, "só no alvo e2e-prod-rest");
  expect(BASE.startsWith("https://")).toBe(true);
  // portão ligado: sem login, a página manda para /entrar e a API responde 401
  const r = await page.goto("/");
  expect(r?.status()).toBe(200);
  expect(page.url()).toMatch(/^https:\/\/.*\/entrar$/);
  expect((await page.request.get("/v1/session")).status()).toBe(401);
  await check404s(page);
  await shots(page, "producao-entrar");
});
