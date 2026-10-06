import { api } from "./api";
import { AudioEngine } from "./audio";
import { TimelineBuffer, type Position } from "./buffer";
import { ChannelClock, syncClock } from "./clock";
import { EventStream } from "./events";
import type { Item, Line, Schedule, SessionState } from "./types";
import { visemeAt, type Mouth } from "./visemes";

export type Frame = {
  now: number;
  pos: Position;
  speaker: string | null;
  mouth: Mouth;
  lineKey: string | null;
};

export type Snapshot = {
  item: Item | null;
  line: Line | null;
  lineKey: string | null;
  next: Item[];
  session: SessionState | null;
  schedule: Schedule | null;
  connected: boolean;
  muted: boolean;
  offset: number;
  ready: boolean;
};

const PRELOAD_ITEMS = 2;

// Player: relógio, buffer, SSE, áudio. Nada de desenho aqui.
export class Player {
  clock = new ChannelClock();
  buffer = new TimelineBuffer();
  audio = new AudioEngine();
  private stream: EventStream | null = null;
  private timers: ReturnType<typeof setInterval>[] = [];
  private raf = 0;
  private frameSubs = new Set<(f: Frame) => void>();
  private subs = new Set<() => void>();
  private snap: Snapshot = {
    item: null, line: null, lineKey: null, next: [], session: null, schedule: null,
    connected: false, muted: true, offset: 0, ready: false,
  };

  getSnapshot = () => this.snap;
  subscribe = (fn: () => void) => {
    this.subs.add(fn);
    return () => this.subs.delete(fn);
  };
  onFrame(fn: (f: Frame) => void) {
    this.frameSubs.add(fn);
    return () => this.frameSubs.delete(fn);
  }

  private set(p: Partial<Snapshot>) {
    this.snap = { ...this.snap, ...p };
    for (const fn of this.subs) fn();
  }

  async start() {
    const { offset } = await syncClock(async () => (await api.now()).server_time);
    this.clock = new ChannelClock(offset);
    this.set({ offset });
    await Promise.all([this.refreshTimeline(), this.refreshSchedule(), this.refreshSession()]);
    this.stream = new EventStream(
      "/v1/events",
      {
        events: {
          item_started: (d) => this.buffer.upsert((d as { item: Item }).item),
          item_scheduled: (d) => this.buffer.upsert(d as Item),
          line_started: () => {},
          session: (d) => this.setSession(d as SessionState),
        },
        onState: (s) => this.set({ connected: s === "open" }),
      },
      ["item_started", "item_scheduled", "line_started", "session"],
    );
    this.stream.start();
    this.timers.push(setInterval(() => void this.refreshTimeline(), 30000));
    this.timers.push(setInterval(() => void this.refreshSchedule(), 5 * 60000));
    this.timers.push(setInterval(() => void this.resync(), 10 * 60000));
    this.set({ ready: true });
    const loop = () => {
      this.tick();
      this.raf = requestAnimationFrame(loop);
    };
    this.raf = requestAnimationFrame(loop);
  }

  stop() {
    cancelAnimationFrame(this.raf);
    this.timers.forEach(clearInterval);
    this.stream?.stop();
    this.audio.stop();
  }

  private async resync() {
    try {
      const { offset } = await syncClock(async () => (await api.now()).server_time);
      this.clock.offset = offset;
      this.set({ offset });
    } catch {
      /* mantém o deslocamento anterior */
    }
  }

  async refreshTimeline() {
    const now = this.clock.now();
    try {
      const r = await api.timeline(now - 60000, now + 15 * 60000);
      this.buffer.replaceRange(r.items ?? [], now - 60000, now + 15 * 60000);
    } catch {
      /* segue com o buffer */
    }
  }

  async refreshSchedule() {
    try {
      this.set({ schedule: await api.schedule() });
    } catch {
      /* mantém a anterior */
    }
  }

  async refreshSession() {
    try {
      this.setSession(await api.session());
    } catch {
      /* sem estado */
    }
  }

  setSession(s: SessionState) {
    const was = this.snap.session?.on_air;
    this.set({ session: s });
    if (was === false && s.on_air) void this.refreshTimeline();
  }

  setMuted(m: boolean) {
    this.audio.setMuted(m);
    this.set({ muted: m });
    if (!m) this.tick(); // liga o som já no ponto exato da fala atual
  }

  private lastPreload = "";

  tick() {
    const now = this.clock.now();
    const pos = this.buffer.at(now);
    const lineKey = pos.item && pos.line ? `${pos.item.id}:${pos.line.seq}` : null;
    this.audio.sync(pos, now);
    const mouth = pos.line ? visemeAt(pos.line.visemes, pos.posMs) : "X";
    const f: Frame = { now, pos, speaker: pos.line?.speaker ?? null, mouth, lineKey };
    for (const fn of this.frameSubs) fn(f);
    if (lineKey !== this.snap.lineKey || pos.item?.id !== this.snap.item?.id) {
      const next = this.buffer.upcoming(now, PRELOAD_ITEMS);
      this.set({ item: pos.item, line: pos.line, lineKey, next });
      const key = [pos.item?.id, ...next.map((n) => n.id)].join(",");
      if (key !== this.lastPreload) {
        this.lastPreload = key;
        // pré-carrega áudio (e já tem os visemas) do item no ar e dos próximos 2
        for (const it of [pos.item, ...next]) if (it?.lines) this.audio.preload(it.lines);
      }
      this.buffer.prune(now - 5 * 60000);
      if (this.buffer.lastEnd() - now < 5 * 60000) void this.refreshTimeline();
    }
    expose(this, f);
  }
}

// Ganchos para os testes ponta a ponta (somente leitura).
function expose(p: Player, f: Frame) {
  const w = window as unknown as { __tvtl?: Record<string, unknown> };
  w.__tvtl = {
    now: f.now,
    offset: p.clock.offset,
    lineKey: f.lineKey,
    speaker: f.speaker,
    mouth: f.mouth,
    text: f.pos.line?.text ?? null,
    muted: p.audio.muted,
    drift: p.audio.lastDrift,
    corrections: p.audio.corrections,
  };
}
