import type { BlackoutWindow } from "./types";

// Selo: preview → EM TESTE; live → AO VIVO; dentro do bloqueio eleitoral → REPRISE.
export function badge(publicMode: string, blackout: BlackoutWindow[] | null | undefined, nowMs: number): string {
  for (const w of blackout ?? []) {
    if (nowMs >= Date.parse(w.start) && nowMs < Date.parse(w.end)) return "REPRISE";
  }
  return publicMode === "live" ? "AO VIVO" : "EM TESTE";
}
