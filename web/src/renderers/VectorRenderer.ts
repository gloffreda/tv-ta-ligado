import type { Mouth } from "../core/visemes";
import type { GestureKind, Renderer, RendererOptions, Scene } from "./Renderer";
import { dudaV, gloriaV, mouthSVG, orlandoV, STUDIO_DESK_V, STUDIO_V, WEATHER_FLOOR_V, WEATHER_V, type VectorActor } from "../sprites/vector";

const NS = "http://www.w3.org/2000/svg";

type Actor = {
  v: VectorActor;
  g: SVGGElement;
  head: SVGGElement | null;
  mouth: SVGGElement | null;
  eyes: SVGGElement[];
  pupils: SVGCircleElement[];
  brows: SVGGElement | null;
  shape: Mouth;
  look: number;
  lookUntil: number;
};

// Visual "humano" (vetor estilizado em SVG): mesmos visemas, piscar e gestos.
export class VectorRenderer implements Renderer {
  private svg: SVGSVGElement | null = null;
  private actors = new Map<string, Actor>();
  private scene: Scene = { scene: "estudio", cast: ["orlando", "duda"] };
  private speaker: string | null = null;
  private raf = 0;
  private t0 = performance.now();

  constructor(private opts: RendererOptions) {}

  async mount(el: HTMLElement) {
    const svg = document.createElementNS(NS, "svg");
    svg.setAttribute("viewBox", "0 0 160 90");
    svg.setAttribute("width", "100%");
    svg.setAttribute("height", "100%");
    svg.setAttribute("role", "img");
    svg.setAttribute("aria-label", "Estúdio em desenho vetorial: a bancada do TV Tá Ligado ao vivo");
    svg.dataset.renderer = "vector";
    svg.style.display = "block";
    el.appendChild(svg);
    this.svg = svg;
    this.build();
    const loop = () => {
      this.tick();
      this.raf = requestAnimationFrame(loop);
    };
    this.raf = requestAnimationFrame(loop);
  }

  private rig(id: string) {
    return this.opts.rigs[id]?.palette ?? {};
  }

  private build() {
    if (!this.svg) return;
    const tempo = this.scene.scene === "tempo";
    const parts: VectorActor[] = [];
    for (const id of this.scene.cast) {
      if (id === "orlando") parts.push(orlandoV(this.rig(id)));
      else if (id === "duda") parts.push(dudaV(this.rig(id)));
      else if (id === "gloria") parts.push(tempo ? gloriaV(this.rig(id), 46, 2) : gloriaV(this.rig(id)));
    }
    // conteúdo estático gerado só a partir das nossas constantes e de cores validadas
    this.svg.innerHTML =
      (tempo ? WEATHER_V : STUDIO_V) +
      parts.map((p) => `<g data-actor="${p.id}">${p.svg}</g>`).join("") +
      (tempo ? WEATHER_FLOOR_V : STUDIO_DESK_V);
    this.actors.clear();
    for (const v of parts) {
      const g = this.svg.querySelector<SVGGElement>(`g[data-actor="${v.id}"]`)!;
      const a: Actor = {
        v, g,
        head: g.querySelector('[data-part="head"]'),
        mouth: g.querySelector('[data-part="mouth"]'),
        eyes: [...g.querySelectorAll<SVGGElement>('[data-part="eye"]')],
        pupils: [...g.querySelectorAll<SVGCircleElement>('[data-part="pupil"]')],
        brows: g.querySelector('[data-part="brows"]'),
        shape: "X", look: 0, lookUntil: 0,
      };
      this.actors.set(v.id, a);
      this.paintMouth(a, "X");
    }
    this.setSpeaker(this.speaker);
  }

  private paintMouth(a: Actor, m: Mouth) {
    a.shape = m;
    if (a.mouth) {
      a.mouth.innerHTML = mouthSVG(m, a.v.lip);
      a.mouth.dataset.viseme = m;
    }
  }

  setScene(program: Scene) {
    this.scene = program;
    this.build();
  }

  setSpeaker(personaId: string | null) {
    this.speaker = personaId;
    const now = performance.now();
    for (const [id, a] of this.actors) {
      let look = 0;
      if (personaId && personaId !== id) {
        const other = this.actors.get(personaId)?.v.cx ?? a.v.cx;
        look = Math.sign(other - a.v.cx);
      }
      if (now > a.lookUntil) this.setLook(a, look);
    }
  }

  private setLook(a: Actor, look: number) {
    a.look = look;
    for (const p of a.pupils) p.setAttribute("cx", String(look * 0.6));
  }

  setViseme(personaId: string, viseme: Mouth) {
    const a = this.actors.get(personaId);
    if (a && a.shape !== viseme) this.paintMouth(a, viseme);
  }

  blink(personaId: string) {
    const a = this.actors.get(personaId);
    if (!a) return;
    for (const e of a.eyes) e.setAttribute("transform", e.getAttribute("transform")!.replace(/ scale\([^)]*\)/, "") + " scale(1 0.12)");
    setTimeout(() => {
      for (const e of a.eyes) e.setAttribute("transform", e.getAttribute("transform")!.replace(/ scale\([^)]*\)/, ""));
    }, 140);
  }

  gesture(personaId: string, kind: GestureKind) {
    const a = this.actors.get(personaId);
    if (!a || kind !== "deboche") return;
    const brow = a.brows?.querySelector<SVGPathElement>('[data-part="brow-r"]') ?? a.brows?.lastElementChild;
    brow?.setAttribute("transform", "translate(0 -0.9) rotate(-8 118 36)");
    if (!this.opts.reducedMotion) {
      a.lookUntil = performance.now() + 900;
      this.setLook(a, 1);
    }
    setTimeout(() => {
      brow?.removeAttribute("transform");
      a.lookUntil = 0;
      this.setSpeaker(this.speaker);
    }, 1200);
  }

  private tick() {
    const now = performance.now();
    for (const [id, a] of this.actors) {
      if (!a.head) continue;
      if (id === this.speaker && !this.opts.reducedMotion) {
        const t = (now - this.t0) / 1000;
        a.head.style.transform = `translateY(${(Math.sin(t * 5.5) * 0.35).toFixed(2)}px) rotate(${(Math.sin(t * 2.3) * 1.6).toFixed(2)}deg)`;
      } else {
        a.head.style.transform = "";
      }
    }
  }

  destroy() {
    cancelAnimationFrame(this.raf);
    this.svg?.remove();
    this.svg = null;
  }
}
