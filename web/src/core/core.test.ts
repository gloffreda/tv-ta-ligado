import { describe, expect, it } from "vitest";
import { estimateOffset, median } from "./clock";
import { mouthFrame, visemeAt, MOUTHS } from "./visemes";
import { driftMs, needsCorrection, startPosition, DRIFT_LIMIT_MS } from "./drift";
import { badge } from "./badge";
import { TimelineBuffer } from "./buffer";
import { backoffMs } from "./events";
import type { Item } from "./types";

describe("visema → quadro de boca", () => {
  it("mapeia as 9 formas do Rhubarb e cai em X para o desconhecido", () => {
    expect(MOUTHS).toEqual(["A", "B", "C", "D", "E", "F", "G", "H", "X"]);
    MOUTHS.forEach((m, i) => expect(mouthFrame(m)).toBe(i));
    expect(mouthFrame("a")).toBe(0);
    expect(mouthFrame("Z")).toBe(8);
    expect(mouthFrame(undefined)).toBe(8);
  });
  it("acha a forma na posição da fala", () => {
    const vs = [
      { start_ms: 0, end_ms: 100, shape: "X" },
      { start_ms: 100, end_ms: 250, shape: "B" },
      { start_ms: 250, end_ms: 400, shape: "E" },
    ];
    expect(visemeAt(vs, 0)).toBe("X");
    expect(visemeAt(vs, 100)).toBe("B");
    expect(visemeAt(vs, 249)).toBe("B");
    expect(visemeAt(vs, 250)).toBe("E");
    expect(visemeAt(vs, 400)).toBe("X");
    expect(visemeAt(vs, -5)).toBe("X");
    expect(visemeAt(null, 10)).toBe("X");
  });
});

describe("deslocamento do relógio", () => {
  it("descarta a pior amostra e usa a mediana de hora do servidor + RTT/2", () => {
    // servidor 10 s à frente; RTTs 40, 20, 30, 60 e uma amostra péssima (2 s)
    const rtts = [40, 20, 30, 60, 2000];
    let t = 1_000_000;
    const samples = rtts.map((rtt) => {
      const sentAt = t;
      const receivedAt = t + rtt;
      t += 5000;
      return { sentAt, receivedAt, serverMs: sentAt + rtt / 2 + 10_000 };
    });
    // a péssima, se usada, puxaria a estimativa; com assimetria ela erraria.
    samples[4].serverMs += 900;
    const { offset, rtt } = estimateOffset(samples);
    expect(offset).toBe(10_000);
    expect(rtt).toBe(35);
  });
  it("mediana", () => {
    expect(median([3, 1, 2])).toBe(2);
    expect(median([4, 1, 2, 3])).toBe(2.5);
  });
});

describe("deriva", () => {
  it("só corrige acima de 150 ms", () => {
    expect(DRIFT_LIMIT_MS).toBe(150);
    expect(driftMs(1.2, 1000)).toBeCloseTo(200);
    expect(needsCorrection(driftMs(1.1, 1000))).toBe(false);
    expect(needsCorrection(driftMs(1.2, 1000))).toBe(true);
    expect(needsCorrection(-151)).toBe(true);
  });
  it("na próxima fala compensa a latência de partida (limitada)", () => {
    expect(startPosition(500, 0)).toBe(500);
    expect(startPosition(500, 120)).toBe(620);
    expect(startPosition(500, 5000)).toBe(900);
    expect(startPosition(-20, 0)).toBe(0);
  });
});

describe("selo", () => {
  const bo = [{ name: "2º turno", start: "2026-10-22T03:00:00Z", end: "2026-10-27T03:00:00Z", active: false }];
  it("preview → EM TESTE, live → AO VIVO, bloqueio → REPRISE", () => {
    const fora = Date.parse("2026-10-06T12:00:00Z");
    const dentro = Date.parse("2026-10-23T12:00:00Z");
    expect(badge("preview", bo, fora)).toBe("EM TESTE");
    expect(badge("live", bo, fora)).toBe("AO VIVO");
    expect(badge("live", bo, dentro)).toBe("REPRISE");
    expect(badge("preview", bo, dentro)).toBe("REPRISE");
    expect(badge("qualquer", null, fora)).toBe("EM TESTE");
  });
});

describe("buffer da linha do tempo", () => {
  const t0 = Date.parse("2026-10-06T15:00:00Z");
  const iso = (ms: number) => new Date(t0 + ms).toISOString();
  const line = (seq: number, off: number, dur: number) => ({
    seq, speaker: seq % 2 ? "orlando" : "duda", type: "banter", text: `fala ${seq}`, spoken_text: "", audio_hash: "h",
    offset_ms: off, duration_ms: dur, audio_url: "/media/h.ogg", starts_at: iso(off), ends_at: iso(off + dur), visemes: [],
  });
  const item: Item = { id: 7, kind: "segment", starts_at: iso(0), ends_at: iso(6000), status: "scheduled", lines: [line(1, 0, 2000), line(2, 2500, 1500)] };
  it("acha fala, pausa e próxima fala", () => {
    const b = new TimelineBuffer();
    b.upsert(item);
    expect(b.at(t0 + 100).line?.seq).toBe(1);
    const p = b.at(t0 + 2100);
    expect(p.line).toBeNull();
    expect(p.nextLine?.seq).toBe(2);
    expect(b.at(t0 + 2600).posMs).toBe(100);
    expect(b.at(t0 + 7000).item).toBeNull();
  });
  it("item pulado (sessão encerrada) sai do buffer", () => {
    const b = new TimelineBuffer();
    b.upsert(item);
    b.upsert({ ...item, status: "skipped" });
    expect(b.at(t0 + 100).item).toBeNull();
  });
});

describe("reconexão do SSE", () => {
  it("backoff cresce e para em 30 s", () => {
    expect(backoffMs(0)).toBeGreaterThanOrEqual(800);
    expect(backoffMs(0)).toBeLessThanOrEqual(1200);
    expect(backoffMs(10)).toBeLessThanOrEqual(36000);
    expect(backoffMs(10)).toBeGreaterThanOrEqual(24000);
  });
});
