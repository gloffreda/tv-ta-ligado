import type { Item, NowResp, Schedule, SessionState, SessionInfo } from "./types";

async function getJSON<T>(url: string): Promise<T> {
  const r = await fetch(url, { cache: "no-store", credentials: "same-origin" });
  if (!r.ok) throw new Error(`${url}: ${r.status}`);
  return (await r.json()) as T;
}

export const api = {
  now: () => getJSON<NowResp>("/v1/now"),
  timeline: (fromMs: number, toMs: number) =>
    getJSON<{ server_time: string; items: Item[] | null }>(`/v1/timeline?from=${Math.floor(fromMs)}&to=${Math.floor(toMs)}`),
  schedule: () => getJSON<Schedule>("/v1/schedule"),
  session: () => getJSON<SessionState>("/v1/session"),
  // O token só sai daqui no corpo do pedido, junto com a confirmação; nunca é guardado.
  start: async (token: string, confirm: string) => {
    const r = await fetch("/v1/session/start", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ token, confirm }),
      credentials: "same-origin",
      cache: "no-store",
    });
    const body = (await r.json().catch(() => ({}))) as { session?: SessionInfo; stop_key?: string; error?: string };
    return { ok: r.ok, status: r.status, ...body };
  },
  stop: async (cred: { stop_key?: string; token?: string }) => {
    const r = await fetch("/v1/session/stop", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(cred),
      credentials: "same-origin",
      cache: "no-store",
    });
    return { ok: r.ok, status: r.status };
  },
};
