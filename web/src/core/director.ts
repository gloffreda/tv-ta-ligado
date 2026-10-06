import type { Frame, Player } from "./player";
import type { Item } from "./types";
import type { Mouth } from "./visemes";
import type { Renderer, Scene } from "../renderers/Renderer";

// Cena a partir do item no ar: o bloco "tempo" é da Glória; se ela fala no
// estúdio (plantão), entra na bancada também.
export function sceneFor(item: Item | null): Scene {
  if (item?.block === "tempo") return { scene: "tempo", cast: ["gloria"], title: "TEMPO" };
  const speakers = new Set((item?.lines ?? []).map((l) => l.speaker));
  const cast = ["orlando", "duda"];
  if (speakers.has("gloria")) cast.push("gloria");
  return { scene: "estudio", cast };
}

// Diretor: liga os quadros do player ao renderizador (boca, quem fala,
// piscar a cada 3–6 s, deboche ocasional da Duda no banter).
export class Director {
  private unsub: (() => void) | null = null;
  private scene: Scene | null = null;
  private speaker: string | null | undefined = undefined;
  private mouths = new Map<string, Mouth>();
  private blinkTimers = new Map<string, ReturnType<typeof setTimeout>>();
  private lastLine: string | null = null;

  constructor(private player: Player, private r: Renderer, private rand: () => number = Math.random) {}

  attach() {
    this.unsub = this.player.onFrame((f) => this.frame(f));
  }

  detach() {
    this.unsub?.();
    this.blinkTimers.forEach(clearTimeout);
    this.blinkTimers.clear();
  }

  private scheduleBlink(id: string) {
    const wait = 3000 + this.rand() * 3000;
    this.blinkTimers.set(
      id,
      setTimeout(() => {
        this.r.blink(id);
        this.scheduleBlink(id);
      }, wait),
    );
  }

  private frame(f: Frame) {
    const sc = sceneFor(f.pos.item);
    if (!this.scene || sc.scene !== this.scene.scene || sc.cast.join() !== this.scene.cast.join()) {
      this.scene = sc;
      this.r.setScene(sc);
      this.mouths.clear();
      for (const id of sc.cast) if (!this.blinkTimers.has(id)) this.scheduleBlink(id);
    }
    if (f.speaker !== this.speaker) {
      this.speaker = f.speaker;
      this.r.setSpeaker(f.speaker);
    }
    for (const id of sc.cast) {
      const m: Mouth = id === f.speaker ? f.mouth : "X";
      if (this.mouths.get(id) !== m) {
        this.mouths.set(id, m);
        this.r.setViseme(id, m);
      }
    }
    if (f.lineKey && f.lineKey !== this.lastLine) {
      this.lastLine = f.lineKey;
      if (f.pos.line?.type === "banter" && sc.cast.includes("duda") && this.rand() < 0.35) {
        this.r.gesture("duda", "deboche");
      }
    }
    const w = window as unknown as { __tvtl?: Record<string, unknown> };
    if (w.__tvtl) w.__tvtl.mouths = Object.fromEntries(this.mouths);
  }
}
