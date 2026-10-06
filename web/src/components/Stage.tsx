import { Component, useEffect, useRef, type ReactNode } from "react";
import type { Player } from "../core/player";
import { Director } from "../core/director";
import type { Renderer, RendererOptions } from "../renderers/Renderer";
import type { Rig } from "../core/types";

type Props = { player: Player; kind: "pixel" | "vector"; rigs: Record<string, Record<string, Rig>> };

// Monta o renderizador escolhido; trocar de visual não mexe no player (áudio,
// relógio e balões continuam).
export function Stage({ player, kind, rigs }: Props) {
  const ref = useRef<HTMLDivElement>(null);
  const rigKey = JSON.stringify(rigs);
  useEffect(() => {
    let r: Renderer | null = null;
    let d: Director | null = null;
    let dead = false;
    let mounted: Promise<void> = Promise.resolve();
    const reducedMotion = window.matchMedia?.("(prefers-reduced-motion: reduce)").matches ?? false;
    const opts: RendererOptions = {
      reducedMotion,
      rigs: Object.fromEntries(Object.entries(rigs).map(([id, rs]) => [id, rs[kind] ?? {}])),
    };
    (async () => {
      await document.fonts?.ready;
      const mod = kind === "pixel" ? await import("../renderers/PixelRenderer") : await import("../renderers/VectorRenderer");
      if (dead || !ref.current) return;
      r = kind === "pixel" ? new (mod as typeof import("../renderers/PixelRenderer")).PixelRenderer(opts) : new (mod as typeof import("../renderers/VectorRenderer")).VectorRenderer(opts);
      mounted = r.mount(ref.current);
      await mounted;
      if (dead) return;
      d = new Director(player, r);
      d.attach();
    })();
    return () => {
      dead = true;
      d?.detach();
      const rr = r;
      // só destrói depois de montado (o PixiJS quebra se destruído no meio do init)
      void mounted.then(() => rr?.destroy(), () => rr?.destroy());
    };
  }, [player, kind, rigKey]);
  return <div className="stage" ref={ref} data-stage={kind} />;
}

// Se o desenho falhar, o resto da página (balões, painéis, som) continua.
export class StageBoundary extends Component<{ children: ReactNode }, { failed: boolean }> {
  state = { failed: false };
  static getDerivedStateFromError() {
    return { failed: true };
  }
  render() {
    return this.state.failed ? <div className="stage" data-stage-failed /> : this.props.children;
  }
}
