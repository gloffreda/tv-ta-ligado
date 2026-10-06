import type { Item, Line } from "./types";

export type Position = {
  item: Item | null;
  line: Line | null;
  posMs: number; // dentro da fala
  itemPosMs: number;
  nextLine: Line | null;
};

// Buffer da linha do tempo no cliente (itens imutáveis, por id).
export class TimelineBuffer {
  private items = new Map<number, Item>();
  private sorted: Item[] = [];

  upsert(it: Item) {
    if (!it || it.status === "skipped") {
      if (it) this.items.delete(it.id);
    } else {
      this.items.set(it.id, it);
    }
    this.sorted = [...this.items.values()].sort((a, b) => Date.parse(a.starts_at) - Date.parse(b.starts_at));
  }

  replaceRange(items: Item[], fromMs: number, toMs: number) {
    for (const [id, it] of this.items) {
      const s = Date.parse(it.starts_at);
      if (s >= fromMs && s < toMs) this.items.delete(id);
    }
    for (const it of items) this.items.set(it.id, it);
    this.sorted = [...this.items.values()].sort((a, b) => Date.parse(a.starts_at) - Date.parse(b.starts_at));
  }

  prune(beforeMs: number) {
    let changed = false;
    for (const [id, it] of this.items) {
      if (Date.parse(it.ends_at) < beforeMs) {
        this.items.delete(id);
        changed = true;
      }
    }
    if (changed) this.sorted = this.sorted.filter((it) => this.items.has(it.id));
  }

  all(): Item[] {
    return this.sorted;
  }

  lastEnd(): number {
    const l = this.sorted[this.sorted.length - 1];
    return l ? Date.parse(l.ends_at) : 0;
  }

  at(t: number): Position {
    for (const it of this.sorted) {
      const s = Date.parse(it.starts_at);
      const e = Date.parse(it.ends_at);
      if (t < s || t >= e) continue;
      const ip = t - s;
      for (const l of it.lines ?? []) {
        if (ip >= l.offset_ms && ip < l.offset_ms + l.duration_ms) {
          return { item: it, line: l, posMs: ip - l.offset_ms, itemPosMs: ip, nextLine: null };
        }
        if (l.offset_ms > ip) return { item: it, line: null, posMs: 0, itemPosMs: ip, nextLine: l };
      }
      return { item: it, line: null, posMs: 0, itemPosMs: ip, nextLine: null };
    }
    return { item: null, line: null, posMs: 0, itemPosMs: 0, nextLine: null };
  }

  // Próximos n itens depois do que está no ar em t.
  upcoming(t: number, n: number): Item[] {
    return this.sorted.filter((it) => Date.parse(it.starts_at) > t).slice(0, n);
  }
}
