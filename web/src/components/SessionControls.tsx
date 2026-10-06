import { useRef, useState } from "react";
import { api } from "../core/api";
import type { SessionState } from "../core/types";

// "Ligar a TV": painel na própria página (sem confirm()), com a duração e o
// teto de gasto. O token NUNCA é guardado (nem localStorage, nem memória além
// deste pedido): a página pede toda vez.
export function OffAir({ session, onStarted }: { session: SessionState | null; onStarted: (stopKey: string) => void | Promise<void> }) {
  const [open, setOpen] = useState(false);
  const [token, setToken] = useState("");
  const [confirm, setConfirm] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const maxMin = session?.max_min ?? 60;
  const maxUSD = session?.max_usd ?? 2;
  const last = session?.last;

  async function go() {
    setBusy(true);
    setErr("");
    const t = token;
    setToken(""); // some do formulário na hora
    try {
      const r = await api.start(t, confirm);
      if (r.ok && r.stop_key) {
        setOpen(false);
        setConfirm("");
        onStarted(r.stop_key);
      } else if (r.status === 429) {
        setErr("Muitas tentativas. Espere um minuto.");
      } else {
        setErr("Não foi possível ligar (token ou confirmação inválidos).");
      }
    } catch {
      setErr("Sem conexão com o canal.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="offair" data-offair>
      <h2>FORA DO AR</h2>
      <p>O canal liga sob demanda.{last?.ended_at ? ` Última sessão encerrada (${reasonLabel(last.end_reason)}).` : ""}</p>
      <button type="button" className="cta" onClick={() => setOpen(true)} data-act="ligar">
        Ligar a TV
      </button>
      {open && (
        <div className="modal" role="dialog" aria-modal="true" aria-labelledby="ligar-t">
          <form
            className="modal-box"
            onSubmit={(e) => {
              e.preventDefault();
              if (!busy && confirm === "LIGAR" && token) void go();
            }}
          >
            <h3 id="ligar-t">Ligar a TV</h3>
            <p className="muted" style={{ margin: 0 }}>
              A sessão dura no máximo <strong>{maxMin} min</strong> e gasta no máximo <strong>US$ {maxUSD.toFixed(2)}</strong>. Desliga sozinha
              nesses limites ou depois de 5 min sem ninguém assistindo. Não renova sozinha.
            </p>
            <label className="f" htmlFor="tok">Token de administração</label>
            <input id="tok" className="f" type="password" autoComplete="off" value={token} onChange={(e) => setToken(e.target.value)} />
            <label className="f" htmlFor="conf">Digite LIGAR para confirmar</label>
            <input id="conf" className="f" type="text" autoComplete="off" autoCapitalize="characters" value={confirm} onChange={(e) => setConfirm(e.target.value)} />
            {err && <div className="err" role="alert">{err}</div>}
            <div style={{ display: "flex", gap: 10, flexWrap: "wrap" }}>
              <button type="submit" className="cta" disabled={busy || confirm !== "LIGAR" || !token} data-act="confirmar">
                Confirmar
              </button>
              <button type="button" className="btn" onClick={() => { setOpen(false); setToken(""); setConfirm(""); setErr(""); }}>
                Cancelar
              </button>
            </div>
          </form>
        </div>
      )}
    </div>
  );
}

export function reasonLabel(r?: string): string {
  switch (r) {
    case "button": return "desligada pelo botão";
    case "no_viewers": return "sem espectadores";
    case "max_time": return "tempo máximo";
    case "max_usd": return "teto de gasto";
    case "restart": return "reinício do sistema";
    default: return r ?? "";
  }
}

// "Desligar": livre, sem confirmação. Usa a chave da sessão (em memória); se a
// página foi recarregada, pede o token.
export function StopButton({ stopKey, onStopped }: { stopKey: string | null; onStopped: () => void }) {
  const [ask, setAsk] = useState(false);
  const [token, setToken] = useState("");
  const [err, setErr] = useState("");
  const busy = useRef(false);
  async function stop(cred: { stop_key?: string; token?: string }) {
    if (busy.current) return;
    busy.current = true;
    setErr("");
    try {
      const r = await api.stop(cred);
      if (r.ok) {
        setAsk(false);
        onStopped();
      } else setErr("Não foi possível desligar.");
    } finally {
      busy.current = false;
      setToken("");
    }
  }
  return (
    <>
      <button type="button" className="btn" data-act="desligar" onClick={() => (stopKey ? void stop({ stop_key: stopKey }) : setAsk(true))}>
        Desligar
      </button>
      {ask && (
        <form className="card" style={{ flexBasis: "100%" }} onSubmit={(e) => { e.preventDefault(); if (token) void stop({ token }); }}>
          <label className="f" htmlFor="tok2">Token para desligar</label>
          <input id="tok2" className="f" type="password" autoComplete="off" value={token} onChange={(e) => setToken(e.target.value)} />
          {err && <div className="err">{err}</div>}
          <button type="submit" className="cta red" style={{ marginTop: 8 }}>Desligar agora</button>
        </form>
      )}
    </>
  );
}
