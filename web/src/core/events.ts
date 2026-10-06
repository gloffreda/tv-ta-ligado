// SSE com reconexão e backoff exponencial (1 s → 30 s). Enquanto cai, o
// player continua com o que já está no buffer.
export type Handlers = { events: Record<string, (data: unknown) => void>; onState?: (s: "open" | "retry") => void };

export function backoffMs(attempt: number): number {
  const base = Math.min(30000, 1000 * 2 ** Math.max(0, attempt));
  return Math.round(base * (0.8 + Math.random() * 0.4));
}

export class EventStream {
  private es: EventSource | null = null;
  private attempt = 0;
  private timer: ReturnType<typeof setTimeout> | null = null;
  private closed = false;

  constructor(private url: string, private h: Handlers, private events: string[]) {}

  start() {
    this.closed = false;
    this.open();
  }

  private open() {
    if (this.closed) return;
    const es = new EventSource(this.url);
    this.es = es;
    es.onopen = () => {
      this.attempt = 0;
      this.h.onState?.("open");
    };
    es.onerror = () => {
      es.close();
      if (this.closed) return;
      this.h.onState?.("retry");
      const wait = backoffMs(this.attempt++);
      this.timer = setTimeout(() => this.open(), wait);
    };
    for (const ev of this.events) {
      es.addEventListener(ev, (e) => {
        try {
          this.h.events[ev]?.(JSON.parse((e as MessageEvent).data));
        } catch {
          /* evento malformado: ignora */
        }
      });
    }
  }

  stop() {
    this.closed = true;
    if (this.timer) clearTimeout(this.timer);
    this.es?.close();
  }
}
