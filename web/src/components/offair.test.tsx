import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { OffAir } from "./SessionControls";

const st = { active: false, on_air: false, max_min: 60, max_usd: 2, viewers: 0 };

describe("FORA DO AR", () => {
  it("SITE_GATE=off sem login: a página aparece, mas sem o botão de ligar", () => {
    const html = renderToStaticMarkup(<OffAir session={st} logged={false} onStarted={() => {}} />);
    expect(html).toContain("FORA DO AR");
    expect(html).not.toContain("Ligar a TV");
  });
  it("logado: mostra Ligar a TV e não pede token nem LIGAR", () => {
    const html = renderToStaticMarkup(<OffAir session={st} logged={true} onStarted={() => {}} />);
    expect(html).toContain("Ligar a TV");
    expect(html).not.toMatch(/token|LIGAR para confirmar|type="password"/i);
  });
});
