// Sincronia de relógio: 5 chamadas a /v1/now, descarta a pior (maior RTT) e
// usa a mediana das estimativas restantes (hora do servidor + RTT/2).

export type ClockSample = { sentAt: number; receivedAt: number; serverMs: number };

export function median(xs: number[]): number {
  const s = [...xs].sort((a, b) => a - b);
  const m = s.length >> 1;
  return s.length % 2 ? s[m] : (s[m - 1] + s[m]) / 2;
}

// offset tal que horaDoServidor ≈ horaLocal + offset.
export function estimateOffset(samples: ClockSample[]): { offset: number; rtt: number } {
  if (samples.length === 0) return { offset: 0, rtt: 0 };
  const withRtt = samples.map((s) => ({ ...s, rtt: s.receivedAt - s.sentAt }));
  withRtt.sort((a, b) => a.rtt - b.rtt);
  const kept = withRtt.length > 1 ? withRtt.slice(0, withRtt.length - 1) : withRtt; // sem a pior
  const offsets = kept.map((s) => s.serverMs + s.rtt / 2 - s.receivedAt);
  return { offset: median(offsets), rtt: median(kept.map((s) => s.rtt)) };
}

export async function syncClock(fetchNow: () => Promise<string>, n = 5, now: () => number = () => Date.now()) {
  const samples: ClockSample[] = [];
  for (let i = 0; i < n; i++) {
    const sentAt = now();
    try {
      const st = await fetchNow();
      const receivedAt = now();
      samples.push({ sentAt, receivedAt, serverMs: Date.parse(st) });
    } catch {
      /* amostra perdida */
    }
  }
  return estimateOffset(samples);
}

// Relógio do canal: Date.now() + offset, mas monotônico (performance.now).
export class ChannelClock {
  private base = Date.now();
  private perf = typeof performance !== "undefined" ? performance.now() : 0;
  constructor(public offset = 0) {}
  now(): number {
    const p = typeof performance !== "undefined" ? performance.now() : 0;
    return this.base + (p - this.perf) + this.offset;
  }
}
