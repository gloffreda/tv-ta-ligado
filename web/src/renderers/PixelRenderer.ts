import "pixi.js/unsafe-eval";
import { Application, Container, Graphics, Text } from "pixi.js";
import type { Mouth } from "../core/visemes";
import type { GestureKind, Renderer, RendererOptions, Scene } from "./Renderer";
import { duda, gloria, orlando, STUDIO_BACK, STUDIO_DESK, WEATHER_BACK, WEATHER_FLOOR, type Avatar, type HeadState, type R } from "../sprites/pixel";

const W = 160;
const H = 90;

function draw(g: Graphics, rects: R[]) {
  for (const [x, y, w, h, c] of rects) g.rect(x, y, w, h).fill(c);
}

type Actor = {
  av: Avatar;
  root: Container;
  head: Graphics;
  state: HeadState;
  dirty: boolean;
  blinkUntil: number;
  browUntil: number;
  lookUntil: number;
};

// Visual pixel art (PixiJS): resolução lógica 160×90, escala inteira.
export class PixelRenderer implements Renderer {
  private app = new Application();
  private el: HTMLElement | null = null;
  private world = new Container();
  private back = new Graphics();
  private front = new Graphics();
  private actorsLayer = new Container();
  private texts: Text[] = [];
  private actors = new Map<string, Actor>();
  private scene: Scene = { scene: "estudio", cast: ["orlando", "duda"] };
  private speaker: string | null = null;
  private ro: ResizeObserver | null = null;
  private k = 1;
  private t0 = performance.now();

  constructor(private opts: RendererOptions) {}

  async mount(el: HTMLElement) {
    this.el = el;
    // Canvas 2D: a cena é só retângulos e texto; mais leve que WebGL (e sem GPU, muito mais).
    await this.app.init({ width: W * 4, height: H * 4, antialias: false, background: "#12241F", resolution: 1, autoDensity: false, preference: "canvas" });
    const c = this.app.canvas;
    c.style.width = "100%";
    c.style.height = "100%";
    c.style.display = "block";
    c.style.imageRendering = "pixelated";
    c.setAttribute("role", "img");
    c.setAttribute("aria-label", "Estúdio em pixel art: a bancada do TV Tá Ligado ao vivo");
    c.dataset.renderer = "pixel";
    el.appendChild(c);
    this.world.addChild(this.back, this.actorsLayer, this.front);
    this.app.stage.addChild(this.world);
    this.build();
    this.ro = new ResizeObserver(() => this.resize());
    this.ro.observe(el);
    this.resize();
    // pixel art não precisa de 60 quadros: 30 bastam e poupam CPU (celular)
    this.app.ticker.maxFPS = 30;
    this.app.ticker.add(() => this.tick());
  }

  private resize() {
    if (!this.el) return;
    const dpr = window.devicePixelRatio || 1;
    const k = Math.max(1, Math.floor((this.el.clientWidth * dpr) / W));
    if (k === this.k && this.app.renderer.width === W * k) return;
    this.k = k;
    this.app.renderer.resize(W * k, H * k);
    this.world.scale.set(k);
    for (const t of this.texts) t.resolution = k;
  }

  private label(text: string, x: number, y: number, size: number, fill: string) {
    const t = new Text({ text, style: { fontFamily: "'Press Start 2P', monospace", fontSize: size, fill }, resolution: this.k });
    t.anchor.set(0.5, 0);
    t.position.set(x, y);
    t.roundPixels = true;
    this.texts.push(t);
    return t;
  }

  private rig(id: string) {
    return this.opts.rigs[id]?.palette ?? {};
  }

  private build() {
    this.back.clear();
    this.front.clear();
    for (const t of this.texts) t.destroy();
    this.texts = [];
    this.actorsLayer.removeChildren();
    this.actors.clear();
    const tempo = this.scene.scene === "tempo";
    draw(this.back, tempo ? WEATHER_BACK : STUDIO_BACK);
    if (tempo) {
      this.world.addChild(this.label("TEMPO", 128, 11, 5, "#0F1B18"));
    } else {
      this.world.addChild(this.label("TÁ", 27, 18, 5, "#0F1B18"), this.label("LIGADO", 133, 18.5, 4, "#FFFFFF"));
    }
    for (const id of this.scene.cast) {
      let av: Avatar | null = null;
      if (id === "orlando") av = orlando(this.rig(id));
      else if (id === "duda") av = duda(this.rig(id));
      else if (id === "gloria") av = tempo ? gloria(this.rig(id), 46, 2) : gloria(this.rig(id));
      if (!av) continue;
      const root = new Container();
      const body = new Graphics();
      draw(body, av.body);
      const head = new Graphics();
      root.addChild(body, head);
      this.actorsLayer.addChild(root);
      this.actors.set(id, { av, root, head, state: { mouth: "X", blink: false, look: 0, brow: false }, dirty: true, blinkUntil: 0, browUntil: 0, lookUntil: 0 });
    }
    draw(this.front, tempo ? WEATHER_FLOOR : STUDIO_DESK);
    if (!tempo) this.world.addChild(this.label("TV TÁ LIGADO", 80, 72.5, 4, "#F5C842"));
  }

  setScene(program: Scene) {
    this.scene = program;
    if (this.el) this.build();
  }

  setSpeaker(personaId: string | null) {
    this.speaker = personaId;
    for (const [id, a] of this.actors) {
      // quem ouve olha para quem fala
      let look: -1 | 0 | 1 = 0;
      if (personaId && personaId !== id) {
        const me = a.av.headBox.x, other = this.actors.get(personaId)?.av.headBox.x ?? me;
        look = other > me ? 1 : other < me ? -1 : 0;
      }
      if (performance.now() > a.lookUntil) a.state.look = look;
      a.dirty = true;
    }
  }

  setViseme(personaId: string, viseme: Mouth) {
    const a = this.actors.get(personaId);
    if (!a || a.state.mouth === viseme) return;
    a.state.mouth = viseme;
    a.dirty = true;
  }

  blink(personaId: string) {
    const a = this.actors.get(personaId);
    if (!a) return;
    a.blinkUntil = performance.now() + 140;
    a.state.blink = true;
    a.dirty = true;
  }

  gesture(personaId: string, kind: GestureKind) {
    const a = this.actors.get(personaId);
    if (!a || kind !== "deboche") return;
    const now = performance.now();
    a.browUntil = now + 1200;
    a.state.brow = true;
    if (!this.opts.reducedMotion) {
      a.lookUntil = now + 900;
      a.state.look = 1; // olhar de lado
    }
    a.dirty = true;
  }

  private tick() {
    const now = performance.now();
    for (const [id, a] of this.actors) {
      if (a.state.blink && now > a.blinkUntil) (a.state.blink = false), (a.dirty = true);
      if (a.state.brow && now > a.browUntil) (a.state.brow = false), (a.dirty = true);
      if (a.lookUntil && now > a.lookUntil) {
        a.lookUntil = 0;
        this.setSpeaker(this.speaker);
      }
      if (a.dirty) {
        a.head.clear();
        draw(a.head, a.av.head(a.state));
        a.dirty = false;
      }
      // leve movimento de cabeça de quem fala (sem movimento se reduzido)
      const talking = id === this.speaker && !this.opts.reducedMotion;
      a.head.y = talking ? Math.round(Math.sin((now - this.t0) / 180) * 0.6 - 0.4) : 0;
    }
  }

  destroy() {
    this.ro?.disconnect();
    this.app.destroy(true, { children: true });
    this.el = null;
  }
}
