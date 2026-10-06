import { useEffect, useRef, useState } from "react";
import type { Program, Schedule } from "../core/types";
import { hhmm } from "../core/labels";

const PX_PER_HOUR = 150;
const DAY = 24 * 3600 * 1000;

// Guia de TV horizontal: 24 h em linha, duas faixas (programas e tempo da
// Glória), linha vermelha "AGORA" no horário de Brasília.
export function Guide({ schedule, now }: { schedule: Schedule | null; now: number }) {
  const box = useRef<HTMLDivElement>(null);
  const [scrolled, setScrolled] = useState(false);
  const progs = schedule?.programs ?? [];
  const dayStart = progs.length ? Date.parse(progs[0].starts_at) : now - (now % DAY);
  const dayEnd = dayStart + DAY;
  const x = (ms: number) => ((ms - dayStart) / 3600000) * PX_PER_HOUR;
  const inDay = (p: Program) => Date.parse(p.starts_at) < dayEnd && Date.parse(p.ends_at) > dayStart;
  const nowX = x(now);
  useEffect(() => {
    if (!scrolled && box.current && progs.length) {
      box.current.scrollLeft = Math.max(0, nowX - box.current.clientWidth / 3);
      setScrolled(true);
    }
  }, [scrolled, progs.length, nowX]);
  if (!schedule) return <p className="vt">Carregando a grade…</p>;
  const isNow = (p: Program) => now >= Date.parse(p.starts_at) && now < Date.parse(p.ends_at);
  const hours = Array.from({ length: 24 }, (_, h) => dayStart + h * 3600000);
  return (
    <div className="guide" ref={box} tabIndex={0} aria-label="Grade de hoje, horário de Brasília">
      <div className="guide-in" style={{ width: 24 * PX_PER_HOUR }}>
        {hours.map((h) => (
          <div key={h} className="hour" style={{ left: x(h) }} aria-hidden="true">
            {hhmm(h)}
          </div>
        ))}
        <div className="lane" style={{ top: 22 }}>
          <span className="lane-label">PROGRAMAS</span>
          {progs.filter(inDay).map((p) => (
            <div key={p.starts_at} className={`slot${isNow(p) ? " now" : ""}`} style={{ left: x(Date.parse(p.starts_at)), width: x(Date.parse(p.ends_at)) - x(Date.parse(p.starts_at)) - 2 }} data-now={isNow(p) || undefined}>
              <div className="t">{p.name}</div>
              <div className="h">{hhmm(p.starts_at)} · {p.about ?? ""}</div>
            </div>
          ))}
        </div>
        <div className="lane" style={{ top: 100 }}>
          <span className="lane-label">TEMPO</span>
          {(schedule.weather ?? []).filter(inDay).map((p) => (
            <div key={p.starts_at} className={`slot weather${isNow(p) ? " now" : ""}`} style={{ left: x(Date.parse(p.starts_at)), width: Math.max(x(Date.parse(p.ends_at)) - x(Date.parse(p.starts_at)), 120) }} title={p.name}>
              <div className="t">{hhmm(p.starts_at)} Glória</div>
            </div>
          ))}
        </div>
        {now >= dayStart && now < dayEnd && (
          <div className="nowline" style={{ left: nowX }} aria-label={`Agora: ${hhmm(now)}`}>
            <span>AGORA {hhmm(now)}</span>
          </div>
        )}
      </div>
    </div>
  );
}
