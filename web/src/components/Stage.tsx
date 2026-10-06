import { useEffect, useRef } from "react";
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
      await r.mount(ref.current);
      if (dead) {
        r.destroy();
        return;
      }
      d = new Director(player, r);
      d.attach();
    })();
    return () => {
      dead = true;
      d?.detach();
      r?.destroy();
    };
  }, [player, kind, rigKey]);
  return <div className="stage" ref={ref} data-stage={kind} />;
}
