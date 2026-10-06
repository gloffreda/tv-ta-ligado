import type { Item, NowResp, Schedule, SessionState, SessionInfo } from "./types";

// Sem login (portão ligado ou login expirado): volta para /entrar.
function toLogin() {
  if (location.pathname !== "/entrar") location.replace("/entrar");
}

async function getJSON<T>(url: string): Promise<T> {
  const r = await fetch(url, { cache: "no-store", credentials: "same-origin" });
  if (r.status === 401) toLogin();
  if (!r.ok) throw new Error(`${url}: ${r.status}`);
  return (await r.json()) as T;
}

export const api = {
  now: () => getJSON<NowResp>("/v1/now"),
  timeline: (fromMs: number, toMs: number) =>
    getJSON<{ server_time: string; items: Item[] | null }>(`/v1/timeline?from=${Math.floor(fromMs)}&to=${Math.floor(toMs)}`),
  schedule: () => getJSON<Schedule>("/v1/schedule"),
  session: () => getJSON<SessionState>("/v1/session"),
  me: () => getJSON<{ gate: boolean; logged: boolean }>("/v1/auth/me"),
  login: async (password: string) => {
    const r = await fetch("/v1/auth/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ password }),
      credentials: "same-origin",
      cache: "no-store",
    });
    const body = r.status === 204 ? {} : ((await r.json().catch(() => ({}))) as { error?: string });
    return { status: r.status, error: (body as { error?: string }).error };
  },
  logout: () => fetch("/v1/auth/logout", { method: "POST", credentials: "same-origin", cache: "no-store" }),
  // Logado no site: o cookie basta (o servidor confere também o Origin).
  start: async () => {
    const r = await fetch("/v1/session/start", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: "{}",
      credentials: "same-origin",
      cache: "no-store",
    });
    const body = (await r.json().catch(() => ({}))) as { session?: SessionInfo; stop_key?: string; error?: string };
    return { ok: r.ok, status: r.status, ...body };
  },
  stop: async (cred: { stop_key?: string } = {}) => {
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
