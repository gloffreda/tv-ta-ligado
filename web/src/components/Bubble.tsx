import type { Line } from "../core/types";
import { NAMES } from "../core/labels";

const SIDE: Record<string, string> = { orlando: "left", duda: "right", gloria: "center" };

// Balão: o texto checado (nunca o spoken_text), apontando para quem fala.
export function Bubble({ line, lineKey, scene }: { line: Line | null; lineKey: string | null; scene: string }) {
  if (!line) return null;
  const side = scene === "tempo" ? "left" : SIDE[line.speaker] ?? "left";
  const src = line.type === "fact" ? line.sources?.[0] : undefined;
  return (
    <div className={`bubble ${side}`} data-line-key={lineKey ?? ""} data-speaker={line.speaker} data-type={line.type}>
      <div className="who">{NAMES[line.speaker] ?? line.speaker.toUpperCase()}</div>
      <div className="txt">{line.text}</div>
      {src && (
        <a className="src" href={src.url} target="_blank" rel="noopener noreferrer" data-source>
          FONTE · {src.name}
        </a>
      )}
    </div>
  );
}
