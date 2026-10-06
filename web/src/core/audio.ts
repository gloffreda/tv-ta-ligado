import type { Position } from "./buffer";
import type { Line } from "./types";
import { driftMs, needsCorrection, startPosition } from "./drift";

// Um elemento <audio> por fala (os arquivos são pequenos e imutáveis).
// Mudo: nada toca, mas tudo o mais segue o relógio. Com som: cada fala começa
// na posição exata do relógio; deriva acima de 150 ms é corrigida na próxima fala.
export class AudioEngine {
  muted = true;
  lastDrift = 0;
  corrections = 0;
  private cache = new Map<string, HTMLAudioElement>();
  private order: string[] = [];
  private current: { key: string; el: HTMLAudioElement } | null = null;
  private latency = 0; // atraso de partida observado (ms)
  private lastCheck = 0;

  preload(lines: Line[]) {
    for (const l of lines) this.get(l.audio_url);
  }

  private get(url: string): HTMLAudioElement {
    let el = this.cache.get(url);
    if (!el) {
      el = new Audio();
      el.preload = "auto";
      el.src = url;
      this.cache.set(url, el);
      this.order.push(url);
      while (this.order.length > 60) {
        const old = this.order.shift()!;
        if (this.current?.el === this.cache.get(old)) continue;
        this.cache.get(old)?.removeAttribute("src");
        this.cache.delete(old);
      }
    }
    return el;
  }

  setMuted(m: boolean) {
    this.muted = m;
    if (m) this.stop();
  }

  stop() {
    if (this.current) {
      this.current.el.pause();
      this.current = null;
    }
  }

  // Chamado a cada quadro com a posição do relógio.
  sync(pos: Position, now: number) {
    if (this.muted || !pos.item || !pos.line) {
      if (this.current && (!pos.line || this.muted)) this.stop();
      return;
    }
    const key = `${pos.item.id}:${pos.line.seq}`;
    if (this.current?.key !== key) {
      this.stop();
      const el = this.get(pos.line.audio_url);
      const start = startPosition(pos.posMs, needsCorrection(this.lastDrift) ? this.latency : 0);
      try {
        el.currentTime = start / 1000;
      } catch {
        /* metadados ainda não carregados */
      }
      el.muted = false;
      const p = el.play();
      if (p) p.catch(() => {});
      this.current = { key, el };
      this.lastCheck = now;
      return;
    }
    // Mede a deriva a cada 250 ms; erro grosseiro (aba dormiu) corrige já.
    if (now - this.lastCheck >= 250 && !this.current.el.paused) {
      this.lastCheck = now;
      const d = driftMs(this.current.el.currentTime, pos.posMs);
      this.lastDrift = d;
      if (needsCorrection(d)) {
        this.corrections++;
        this.latency = Math.max(0, -d);
        if (Math.abs(d) > 1000) this.current.el.currentTime = pos.posMs / 1000;
      }
    }
  }
}
