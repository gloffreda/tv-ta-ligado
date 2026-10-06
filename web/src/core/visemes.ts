import type { Viseme } from "./types";

// As 9 formas de boca do Rhubarb: A–H e X (repouso). Mesmo índice nos dois rigs.
export const MOUTHS = ["A", "B", "C", "D", "E", "F", "G", "H", "X"] as const;
export type Mouth = (typeof MOUTHS)[number];

export function mouthFrame(shape: string | undefined | null): number {
  const i = MOUTHS.indexOf((shape ?? "X").toUpperCase() as Mouth);
  return i < 0 ? MOUTHS.indexOf("X") : i;
}

// Forma de boca na posição (ms desde o início da fala). Fora da fala: X.
export function visemeAt(vs: Viseme[] | null | undefined, posMs: number): Mouth {
  if (!vs || posMs < 0) return "X";
  // busca binária: visemas vêm ordenados por start_ms
  let lo = 0;
  let hi = vs.length - 1;
  while (lo <= hi) {
    const mid = (lo + hi) >> 1;
    const v = vs[mid];
    if (posMs < v.start_ms) hi = mid - 1;
    else if (posMs >= v.end_ms) lo = mid + 1;
    else return (MOUTHS as readonly string[]).includes(v.shape) ? (v.shape as Mouth) : "X";
  }
  return "X";
}
