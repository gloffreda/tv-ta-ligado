// Sprites em pixel art gerados por código a partir dos desenhos da
// referência (SVG 160×90). Cada avatar: corpo fixo + cabeça que muda com a
// boca (A–H, X), o piscar, a direção do olhar e a sobrancelha (deboche).
import type { Mouth } from "../core/visemes";

export type R = [x: number, y: number, w: number, h: number, color: string];

export type HeadState = { mouth: Mouth; blink: boolean; look: -1 | 0 | 1; brow: boolean };

export type Avatar = {
  id: string;
  body: R[]; // em coordenadas da cena
  head: (s: HeadState) => R[];
  headBox: { x: number; y: number; w: number; h: number };
};

const DARK = "#1B1B1B";
const MOUTH_IN = "#2A1210";
const TEETH = "#F3EEDF";
const TONGUE = "#E5533D";

// Boca: 6×3 a partir de (x, y); lip = cor do lábio.
export function mouthRects(m: Mouth, x: number, y: number, lip: string): R[] {
  switch (m) {
    case "A": // M, B, P: fechada
      return [[x + 1, y + 1, 4, 1, lip]];
    case "B": // K, S, T: dentes cerrados
      return [[x + 1, y, 4, 1, lip], [x + 1, y + 1, 4, 1, TEETH]];
    case "C": // EH, AE: aberta
      return [[x + 1, y, 4, 1, lip], [x + 1, y + 1, 4, 1, MOUTH_IN], [x + 1, y + 2, 4, 1, lip]];
    case "D": // AA: bem aberta
      return [[x, y, 6, 1, TEETH], [x, y + 1, 6, 1, MOUTH_IN], [x + 1, y + 2, 4, 1, lip]];
    case "E": // AO, ER: arredondada
      return [[x + 2, y, 2, 1, lip], [x + 1, y + 1, 4, 1, MOUTH_IN], [x + 2, y + 2, 2, 1, lip]];
    case "F": // UW, OW, W: bico
      return [[x + 2, y, 2, 1, lip], [x + 2, y + 1, 2, 1, MOUTH_IN], [x + 2, y + 2, 2, 1, lip]];
    case "G": // F, V: dente no lábio
      return [[x + 1, y, 4, 1, TEETH], [x + 1, y + 1, 4, 1, lip]];
    case "H": // L: língua
      return [[x + 1, y, 4, 1, lip], [x + 1, y + 1, 4, 1, MOUTH_IN], [x + 2, y + 1, 2, 1, TONGUE], [x + 1, y + 2, 4, 1, lip]];
    default: // X: repouso
      return [[x + 1, y + 1, 4, 1, lip]];
  }
}

function eyes(xL: number, xR: number, y: number, s: HeadState, skinShade: string): R[] {
  if (s.blink) return [[xL, y + 1, 2, 1, skinShade], [xR, y + 1, 2, 1, skinShade]];
  const dx = s.look;
  return [[xL + dx, y, 2, 2, DARK], [xR + dx, y, 2, 2, DARK]];
}

export function orlando(p: Record<string, string> = {}): Avatar {
  const hair = p.hair ?? "#C9C6BE", skin = p.skin ?? "#D9A27A", suit = p.suit ?? "#22324F", tie = p.tie ?? "#C0392B", shirt = p.shirt ?? "#F3EEDF";
  return {
    id: "orlando",
    headBox: { x: 35, y: 30, w: 17, h: 17 },
    body: [[31, 47, 26, 16, suit], [42, 47, 4, 10, shirt], [43, 48, 2, 9, tie]],
    head: (s) => [
      [36, 30, 16, 6, hair], [35, 32, 2, 5, hair],
      [37, 34, 14, 13, skin],
      [39, s.brow ? 36 : 37, 4, 1, "#8E8A80"], [45, 37, 4, 1, "#8E8A80"],
      ...eyes(40, 46, 38, s, "#B9835F"),
      [40, 42, 8, 1, "#B8B4AA"], // bigode
      ...mouthRects(s.mouth, 41, 43, "#7A4B32"),
    ],
  };
}

export function duda(p: Record<string, string> = {}): Avatar {
  const hair = p.hair ?? "#3A2340", skin = p.skin ?? "#8C5A3C", jacket = p.jacket ?? "#F5C842", shirt = p.shirt ?? "#0F1B18";
  return {
    id: "duda",
    headBox: { x: 104, y: 29, w: 22, h: 18 },
    body: [[102, 47, 26, 16, jacket], [114, 47, 2, 10, shirt]],
    head: (s) => [
      [106, 29, 18, 8, hair], [104, 31, 4, 12, hair], [122, 31, 4, 12, hair],
      [108, 34, 14, 13, skin],
      ...(s.brow ? ([[116, 36, 3, 1, hair]] as R[]) : []), // sobrancelha erguida: deboche
      ...eyes(111, 117, 38, s, "#6E4630"),
      ...mouthRects(s.mouth, 112, 42, "#5A2E22"),
    ],
  };
}

// Glória Garoa: vestido rosa de gala, coque, brincos dourados.
export function gloria(p: Record<string, string> = {}, x0 = 0, y0 = 0): Avatar {
  const hair = p.hair ?? "#2A211A", skin = p.skin ?? "#B57A55", dress = p.dress ?? "#E58FB0", jewel = p.jewel ?? "#F5C842";
  const o = (r: R): R => [r[0] + x0, r[1] + y0, r[2], r[3], r[4]];
  return {
    id: "gloria",
    headBox: { x: 70 + x0, y: 24 + y0, w: 22, h: 21 },
    body: ([
      [68, 45, 26, 18, dress], [78, 45, 6, 3, skin], [79, 48, 4, 1, skin], // decote em V
      [77, 45, 1, 1, jewel], [84, 45, 1, 1, jewel], [80, 49, 2, 1, jewel], // colar
      [66, 50, 3, 10, dress], [93, 50, 3, 10, dress], // mangas
    ] as R[]).map(o),
    head: (s) =>
      ([
        [77, 24, 8, 4, hair], // coque
        [72, 28, 18, 6, hair], [70, 30, 4, 14, hair], [88, 30, 4, 14, hair],
        [74, 32, 14, 13, skin],
        [76, s.brow ? 34 : 35, 4, 1, hair], [82, 35, 4, 1, hair],
        ...(s.blink ? [] : ([[76, 36, 1, 1, DARK], [85, 36, 1, 1, DARK]] as R[])), // cílios
        ...eyes(77, 83, 37, s, "#93603F"),
        [73, 40, 1, 2, jewel], [88, 40, 1, 2, jewel], // brincos
        ...mouthRects(s.mouth, 78, 41, "#C2185B"),
      ] as R[]).map(o),
  };
}

// Cenário do estúdio (referência): parede, placas TÁ / LIGADO, TV de antena
// com palha de aço, bancada.
export const STUDIO_BACK: R[] = [
  [0, 0, 160, 90, "#1D3A32"], [0, 0, 160, 4, "#16302A"], [0, 52, 160, 2, "#16302A"],
  [10, 10, 34, 20, "#0F1B18"], [12, 12, 30, 16, "#F5C842"],
  [116, 10, 34, 20, "#0F1B18"], [118, 12, 30, 16, "#E5533D"],
  [67, 22, 26, 22, "#5B4A3A"], [70, 25, 17, 14, "#8FD3B6"], [72, 27, 4, 2, "#C9F0DF"],
  [88, 27, 3, 3, "#2A211A"], [88, 33, 3, 3, "#2A211A"], [73, 44, 4, 4, "#3B2F25"], [84, 44, 4, 4, "#3B2F25"],
  [74, 12, 1, 10, "#B9C2B5"], [73, 11, 1, 1, "#B9C2B5"], [72, 10, 1, 1, "#B9C2B5"],
  [86, 12, 1, 10, "#B9C2B5"], [87, 11, 1, 1, "#B9C2B5"], [88, 9, 1, 2, "#B9C2B5"],
  [86, 6, 5, 4, "#D9D3C4"], [87, 7, 1, 1, "#9A9486"], [89, 8, 1, 1, "#9A9486"], // palha de aço
  [73, 16, 3, 2, "#E5533D"],
];

export const STUDIO_DESK: R[] = [
  [0, 60, 160, 30, "#7A4E26"], [0, 60, 160, 4, "#A0672F"], [56, 68, 48, 12, "#5E3B1C"],
  [50, 54, 2, 6, "#2B2B2B"], [49, 52, 4, 3, "#444444"], [108, 54, 2, 6, "#2B2B2B"], [107, 52, 4, 3, "#444444"],
  [20, 62, 10, 2, "#3B3B3B"], [132, 61, 8, 3, "#F3EEDF"],
];

// Cenário do tempo: painel com o mapa e ícones de sol, nuvem e chuva.
export const WEATHER_BACK: R[] = [
  [0, 0, 160, 90, "#1D3A32"], [0, 0, 160, 4, "#16302A"],
  [8, 8, 92, 50, "#0F1B18"], [10, 10, 88, 46, "#12241F"],
  // mapa estilizado
  [34, 16, 26, 6, "#2C4740"], [28, 22, 40, 10, "#2C4740"], [30, 32, 38, 8, "#2C4740"], [36, 40, 26, 6, "#2C4740"],
  [42, 46, 14, 4, "#2C4740"], [46, 50, 6, 3, "#2C4740"], [62, 24, 10, 8, "#2C4740"],
  // sol
  [20, 14, 6, 6, "#F5C842"], [22, 12, 2, 1, "#F5C842"], [22, 21, 2, 1, "#F5C842"], [18, 16, 1, 2, "#F5C842"], [27, 16, 1, 2, "#F5C842"],
  // nuvem com chuva
  [74, 16, 14, 4, "#F3EEDF"], [77, 13, 7, 3, "#F3EEDF"], [76, 22, 1, 2, "#8FD3B6"], [80, 23, 1, 2, "#8FD3B6"], [84, 22, 1, 2, "#8FD3B6"],
  // nuvem pequena
  [48, 26, 8, 3, "#B9C2B5"], [50, 24, 4, 2, "#B9C2B5"],
  [104, 8, 48, 12, "#E58FB0"], // faixa "TEMPO"
];

export const WEATHER_FLOOR: R[] = [[0, 62, 160, 28, "#16302A"], [0, 62, 160, 2, "#2C4740"], [100, 64, 56, 3, "#0F1B18"]];
