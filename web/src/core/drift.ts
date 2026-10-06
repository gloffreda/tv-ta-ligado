// Deriva entre o áudio e o relógio do canal.
export const DRIFT_LIMIT_MS = 150;

// drift > 0: o áudio está adiantado em relação ao relógio.
export function driftMs(audioCurrentTimeSec: number, expectedPosMs: number): number {
  return audioCurrentTimeSec * 1000 - expectedPosMs;
}

export function needsCorrection(drift: number, limit = DRIFT_LIMIT_MS): boolean {
  return Math.abs(drift) > limit;
}

// Na próxima fala, o áudio começa exatamente na posição do relógio; se a
// deriva medida foi grande, compensa a latência de partida observada.
export function startPosition(expectedPosMs: number, lastStartLatencyMs: number): number {
  return Math.max(0, expectedPosMs + Math.min(Math.max(lastStartLatencyMs, 0), 400));
}
