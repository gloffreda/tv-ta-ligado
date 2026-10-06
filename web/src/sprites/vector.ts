// Rostos vetoriais estilizados (SVG), mesma paleta e figurino do pixel:
// Orlando de terno azul e gravata vermelha, Duda de jaqueta amarela, Glória
// de vestido rosa de gala. Traço limpo, cores chapadas, sem fotorrealismo.
import type { Mouth } from "../core/visemes";

const hex = /^#[0-9a-fA-F]{3,8}$/;
export function color(v: string | undefined, def: string): string {
  return v && hex.test(v) ? v : def;
}

// Boca centrada em (0,0), ~6 de largura. Mesmas 9 formas do Rhubarb.
export function mouthSVG(m: Mouth, lip: string): string {
  const inside = "#2A1210", teeth = "#F3EEDF", tongue = "#E5533D";
  const s = `stroke="${lip}" stroke-width="0.7" stroke-linecap="round" fill="none"`;
  switch (m) {
    case "A":
      return `<path d="M-2.4 0 L2.4 0" ${s} stroke-width="0.9"/>`;
    case "B":
      return `<rect x="-2.4" y="-0.6" width="4.8" height="1.2" rx="0.5" fill="${teeth}" stroke="${lip}" stroke-width="0.5"/>`;
    case "C":
      return `<ellipse rx="2.4" ry="1.1" fill="${inside}" stroke="${lip}" stroke-width="0.6"/>`;
    case "D":
      return `<ellipse rx="2.9" ry="1.9" fill="${inside}" stroke="${lip}" stroke-width="0.6"/><rect x="-2.2" y="-1.7" width="4.4" height="0.8" fill="${teeth}"/>`;
    case "E":
      return `<ellipse rx="1.7" ry="1.4" fill="${inside}" stroke="${lip}" stroke-width="0.6"/>`;
    case "F":
      return `<ellipse rx="1" ry="1.1" fill="${inside}" stroke="${lip}" stroke-width="0.8"/>`;
    case "G":
      return `<rect x="-2" y="-0.9" width="4" height="0.9" fill="${teeth}"/><path d="M-2.4 0.3 Q0 1.1 2.4 0.3" ${s}/>`;
    case "H":
      return `<ellipse rx="2.3" ry="1.3" fill="${inside}" stroke="${lip}" stroke-width="0.6"/><ellipse cy="0.5" rx="1.2" ry="0.6" fill="${tongue}"/>`;
    default:
      return `<path d="M-2.4 0 Q0 0.5 2.4 0" ${s}/>`;
  }
}

export type VectorActor = {
  id: string;
  cx: number; // centro da cabeça (para o olhar)
  svg: string; // corpo + <g data-part="head"> com olhos, sobrancelhas e boca
  lip: string;
};

const eyesSVG = (xL: number, xR: number, y: number, iris = "#1B1B1B") => `
  <g data-part="eyes">
    <g data-part="eye" transform="translate(${xL} ${y})"><ellipse rx="1.5" ry="1.2" fill="#FFFFFF"/><circle data-part="pupil" r="0.8" fill="${iris}"/></g>
    <g data-part="eye" transform="translate(${xR} ${y})"><ellipse rx="1.5" ry="1.2" fill="#FFFFFF"/><circle data-part="pupil" r="0.8" fill="${iris}"/></g>
  </g>`;

export function orlandoV(p: Record<string, string> = {}): VectorActor {
  const hair = color(p.hair, "#C9C6BE"), skin = color(p.skin, "#D9A27A"), suit = color(p.suit, "#22324F"), tie = color(p.tie, "#C0392B"), shirt = color(p.shirt, "#F3EEDF");
  const lip = "#7A4B32";
  return {
    id: "orlando", cx: 44, lip,
    svg: `
    <path d="M30 64 Q30 49 44 47 Q58 49 58 64 Z" fill="${suit}"/>
    <path d="M40.5 47 L47.5 47 L44 55 Z" fill="${shirt}"/>
    <path d="M43 48.5 L45 48.5 L45.8 56 L44 58.5 L42.2 56 Z" fill="${tie}"/>
    <rect x="41.5" y="44" width="5" height="4" fill="${skin}"/>
    <g data-part="head" style="transform-box: fill-box; transform-origin: 50% 90%">
      <ellipse cx="36.6" cy="40" rx="1.6" ry="2.2" fill="${skin}"/><ellipse cx="51.4" cy="40" rx="1.6" ry="2.2" fill="${skin}"/>
      <ellipse cx="44" cy="40" rx="7.2" ry="8" fill="${skin}"/>
      <path d="M36.5 37 Q36 30.5 44 30.5 Q52 30.5 51.5 37 Q49 33.5 44 33.8 Q39 33.5 36.5 37 Z" fill="${hair}"/>
      <g data-part="brows"><path d="M39 36.4 L42.4 36" stroke="#8E8A80" stroke-width="0.8" stroke-linecap="round"/><path d="M45.6 36 L49 36.4" stroke="#8E8A80" stroke-width="0.8" stroke-linecap="round"/></g>
      ${eyesSVG(40.8, 47.2, 38.6)}
      <path d="M40.6 42.6 Q44 41.4 47.4 42.6 Q44 43.4 40.6 42.6 Z" fill="#B8B4AA"/>
      <g data-part="mouth" transform="translate(44 44.6)"></g>
    </g>`,
  };
}

export function dudaV(p: Record<string, string> = {}): VectorActor {
  const hair = color(p.hair, "#3A2340"), skin = color(p.skin, "#8C5A3C"), jacket = color(p.jacket, "#F5C842"), shirt = color(p.shirt, "#0F1B18");
  const lip = "#5A2E22";
  return {
    id: "duda", cx: 115, lip,
    svg: `
    <path d="M101 64 Q101 49 115 47 Q129 49 129 64 Z" fill="${jacket}"/>
    <path d="M112.5 47 L117.5 47 L116.2 58 L113.8 58 Z" fill="${shirt}"/>
    <path d="M110 48 L113 52 L111 60" stroke="#C9A12E" stroke-width="0.6" fill="none"/><path d="M120 48 L117 52 L119 60" stroke="#C9A12E" stroke-width="0.6" fill="none"/>
    <rect x="112.5" y="44" width="5" height="4" fill="${skin}"/>
    <g data-part="head" style="transform-box: fill-box; transform-origin: 50% 90%">
      <path d="M104 46 Q102 30 115 29 Q128 30 126 46 L122 46 L122 36 L108 36 L108 46 Z" fill="${hair}"/>
      <ellipse cx="115" cy="40" rx="7" ry="7.8" fill="${skin}"/>
      <path d="M107.6 37.5 Q109 31 115 31.5 Q121 31 122.4 37.5 Q118 33.6 112 35.4 Q109.5 36 107.6 37.5 Z" fill="${hair}"/>
      <g data-part="brows"><path d="M110 36.3 L113 36" stroke="${hair}" stroke-width="0.8" stroke-linecap="round"/><path data-part="brow-r" d="M117 36 L120 36.3" stroke="${hair}" stroke-width="0.8" stroke-linecap="round"/></g>
      ${eyesSVG(111.8, 118.2, 38.6, "#2B1A10")}
      <g data-part="mouth" transform="translate(115 43.6)"></g>
    </g>`,
  };
}

export function gloriaV(p: Record<string, string> = {}, dx = 0, dy = 0): VectorActor {
  const hair = color(p.hair, "#2A211A"), skin = color(p.skin, "#B57A55"), dress = color(p.dress, "#E58FB0"), jewel = color(p.jewel, "#F5C842");
  const lip = "#C2185B";
  return {
    id: "gloria", cx: 81 + dx, lip,
    svg: `<g transform="translate(${dx} ${dy})">
    <path d="M66 64 Q66 48 81 45.5 Q96 48 96 64 Z" fill="${dress}"/>
    <path d="M77 45.6 L85 45.6 L81 51 Z" fill="${skin}"/>
    <path d="M76.8 46 Q81 50.4 85.2 46" stroke="${jewel}" stroke-width="0.6" fill="none"/><circle cx="81" cy="49.6" r="0.7" fill="${jewel}"/>
    <path d="M68 52 Q72 50 74 56" stroke="#C9708F" stroke-width="0.6" fill="none"/><path d="M94 52 Q90 50 88 56" stroke="#C9708F" stroke-width="0.6" fill="none"/>
    <rect x="78.6" y="42" width="4.8" height="4" fill="${skin}"/>
    <g data-part="head" style="transform-box: fill-box; transform-origin: 50% 90%">
      <circle cx="81" cy="27" r="3.6" fill="${hair}"/>
      <path d="M71 44 Q69 29 81 29.2 Q93 29 91 44 L88 44 L88 34 L74 34 L74 44 Z" fill="${hair}"/>
      <ellipse cx="81" cy="38.4" rx="7" ry="7.8" fill="${skin}"/>
      <path d="M73.8 35.6 Q75 30.4 81 30.6 Q87 30.4 88.2 35.6 Q84.5 32.6 81 33 Q77.5 32.6 73.8 35.6 Z" fill="${hair}"/>
      <g data-part="brows"><path d="M76 34.8 L79 34.4" stroke="${hair}" stroke-width="0.7" stroke-linecap="round"/><path d="M83 34.4 L86 34.8" stroke="${hair}" stroke-width="0.7" stroke-linecap="round"/></g>
      ${eyesSVG(77.8, 84.2, 37)}
      <path d="M76.2 35.7 L75.6 35.2 M86 35.7 L86.6 35.2" stroke="#1B1B1B" stroke-width="0.4"/>
      <circle cx="73.8" cy="41.2" r="0.8" fill="${jewel}"/><circle cx="88.2" cy="41.2" r="0.8" fill="${jewel}"/>
      <g data-part="mouth" transform="translate(81 42.2)"></g>
    </g></g>`,
  };
}

export const STUDIO_V = `
  <rect width="160" height="90" fill="#1D3A32"/>
  <rect width="160" height="4" fill="#16302A"/><rect y="52" width="160" height="2" fill="#16302A"/>
  <rect x="10" y="10" width="34" height="20" rx="1.5" fill="#0F1B18"/><rect x="12" y="12" width="30" height="16" rx="1" fill="#F5C842"/>
  <text x="27" y="23" text-anchor="middle" font-family="'Press Start 2P', monospace" font-size="5" fill="#0F1B18">TÁ</text>
  <rect x="116" y="10" width="34" height="20" rx="1.5" fill="#0F1B18"/><rect x="118" y="12" width="30" height="16" rx="1" fill="#E5533D"/>
  <text x="133" y="23" text-anchor="middle" font-family="'Press Start 2P', monospace" font-size="4" fill="#FFFFFF">LIGADO</text>
  <rect x="67" y="22" width="26" height="22" rx="2.5" fill="#5B4A3A"/><rect x="70" y="25" width="17" height="14" rx="2" fill="#8FD3B6"/>
  <rect x="72" y="27" width="4" height="2" rx="1" fill="#C9F0DF"/>
  <circle cx="89.5" cy="28.5" r="1.5" fill="#2A211A"/><circle cx="89.5" cy="34.5" r="1.5" fill="#2A211A"/>
  <rect x="73" y="44" width="4" height="4" fill="#3B2F25"/><rect x="84" y="44" width="4" height="4" fill="#3B2F25"/>
  <path d="M75 22 L72 10" stroke="#B9C2B5" stroke-width="0.8"/><path d="M86 22 L88.5 9" stroke="#B9C2B5" stroke-width="0.8"/>
  <ellipse cx="88.5" cy="8" rx="2.6" ry="2" fill="#D9D3C4"/><path d="M87 7.6 Q88.5 6.6 90 8.4" stroke="#9A9486" stroke-width="0.4" fill="none"/>
  <rect x="73" y="16" width="3" height="2" rx="0.5" fill="#E5533D"/>`;

export const STUDIO_DESK_V = `
  <rect y="60" width="160" height="30" fill="#7A4E26"/><rect y="60" width="160" height="4" fill="#A0672F"/>
  <rect x="56" y="68" width="48" height="12" rx="1" fill="#5E3B1C"/>
  <text x="80" y="76.5" text-anchor="middle" font-family="'Press Start 2P', monospace" font-size="4" fill="#F5C842">TV TÁ LIGADO</text>
  <rect x="50" y="54" width="2" height="6" fill="#2B2B2B"/><ellipse cx="51" cy="53.4" rx="2" ry="1.6" fill="#444444"/>
  <rect x="108" y="54" width="2" height="6" fill="#2B2B2B"/><ellipse cx="109" cy="53.4" rx="2" ry="1.6" fill="#444444"/>
  <rect x="20" y="62" width="10" height="2" rx="0.5" fill="#3B3B3B"/><rect x="132" y="61" width="8" height="3" rx="0.5" fill="#F3EEDF"/>`;

export const WEATHER_V = `
  <rect width="160" height="90" fill="#1D3A32"/><rect width="160" height="4" fill="#16302A"/>
  <rect x="8" y="8" width="92" height="50" rx="2" fill="#0F1B18"/><rect x="10" y="10" width="88" height="46" rx="1.5" fill="#12241F"/>
  <path d="M34 16 L60 16 L68 22 L72 26 L70 32 L68 40 L62 46 L56 50 L50 53 L46 50 L40 46 L36 40 L30 34 L28 26 Z" fill="#2C4740"/>
  <circle cx="23" cy="17" r="3.4" fill="#F5C842"/>
  <g stroke="#F5C842" stroke-width="0.8"><path d="M23 11.6 V12.8"/><path d="M23 21.2 V22.4"/><path d="M17.6 17 H18.8"/><path d="M27.2 17 H28.4"/></g>
  <ellipse cx="81" cy="18" rx="7" ry="2.6" fill="#F3EEDF"/><ellipse cx="80.5" cy="15.6" rx="3.6" ry="2.4" fill="#F3EEDF"/>
  <g stroke="#8FD3B6" stroke-width="0.6"><path d="M76.5 22 L76 24"/><path d="M80.5 23 L80 25"/><path d="M84.5 22 L84 24"/></g>
  <ellipse cx="52" cy="26.5" rx="4" ry="1.6" fill="#B9C2B5"/><ellipse cx="52" cy="25.2" rx="2" ry="1.4" fill="#B9C2B5"/>
  <rect x="104" y="8" width="48" height="12" rx="1.5" fill="#E58FB0"/>
  <text x="128" y="16.2" text-anchor="middle" font-family="'Press Start 2P', monospace" font-size="5" fill="#0F1B18">TEMPO</text>`;

export const WEATHER_FLOOR_V = `<rect y="62" width="160" height="28" fill="#16302A"/><rect y="62" width="160" height="2" fill="#2C4740"/><rect x="100" y="64" width="56" height="3" rx="1" fill="#0F1B18"/>`;
