import type { Mouth } from "../core/visemes";
import type { Rig } from "../core/types";

// Cena: cenário do programa e elenco visível.
export type Scene = { scene: "estudio" | "tempo" | string; cast: string[]; title?: string };

export type GestureKind = "deboche" | "aceno";

export type RendererOptions = {
  reducedMotion: boolean;
  rigs: Record<string, Rig>; // rigs.pixel ou rigs.vector de cada persona (do YAML)
};

// Contrato dos dois visuais: mesmo canal, só muda o desenho.
export interface Renderer {
  mount(el: HTMLElement): Promise<void>;
  setScene(program: Scene): void;
  setSpeaker(personaId: string | null): void;
  setViseme(personaId: string, viseme: Mouth): void;
  blink(personaId: string): void;
  gesture(personaId: string, kind: GestureKind): void;
  destroy(): void;
}

export const SEATS: Record<string, { x: number }> = { left: { x: 0 }, right: { x: 1 }, center: { x: 0.5 } };
