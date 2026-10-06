import type { Item } from "./types";

export const BLOCK_LABEL: Record<string, string> = {
  noticias: "Notícias",
  economia: "Economia traduzida",
  humor: "Que fase!",
  tempo: "Tempo com Glória Garoa",
  mercado: "Mercado",
  manchetes: "Manchetes",
  abertura: "Abertura",
};

export const NAMES: Record<string, string> = { orlando: "ORLANDO", duda: "DUDA", gloria: "GLÓRIA" };

export function itemLabel(it: Item | null | undefined): string {
  if (!it) return "";
  if (it.kind === "bumper") return "Vinheta";
  const b = BLOCK_LABEL[it.block ?? ""] ?? "Notícias";
  return it.kind === "replay" ? `${b} (reprise)` : b;
}

const fmt = new Intl.DateTimeFormat("pt-BR", { timeZone: "America/Sao_Paulo", hour: "2-digit", minute: "2-digit" });
export function hhmm(ms: number | string): string {
  return fmt.format(new Date(typeof ms === "string" ? Date.parse(ms) : ms));
}
