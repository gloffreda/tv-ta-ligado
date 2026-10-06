import { useRef, useState } from "react";
import { api } from "../core/api";
import type { SessionState } from "../core/types";

// FORA DO AR. Quem está logado vê "Ligar a TV": o painel mostra a duração
// máxima e o teto de gasto e pede só "Confirmar" (dois cliques deliberados).
// Sem login (portão desligado, público), o botão nem aparece.
export function OffAir({ session, logged, onStarted }: { session: SessionState | null; logged: boolean; onStarted: (stopKey: string) => void | Promise<void> }) {
  const [open, setOpen] = useState(false);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const maxMin = session?.max_min ?? 60;
  const maxUSD = session?.max_usd ?? 2;
  const last = session?.last;

  async function go() {
    setBusy(true);
    setErr("");
    try {
      const r = await api.start();
      if (r.ok) {
        setOpen(false);
        onStarted(r.stop_key ?? "");
      } else if (r.status === 429) {
        setErr("Muitas tentativas. Espere um minuto.");
      } else if (r.status === 401) {
        location.replace("/entrar");
      } else {
        setErr("Não foi possível ligar agora.");
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
      {logged && (
        <button type="button" className="cta" onClick={() => setOpen(true)} data-act="ligar">
          Ligar a TV
        </button>
      )}
      {open && (
        <div className="modal" role="dialog" aria-modal="true" aria-labelledby="ligar-t">
          <div className="modal-box">
            <h3 id="ligar-t">Ligar a TV</h3>
            <p className="muted" style={{ margin: 0 }}>
              A sessão dura no máximo <strong>{maxMin} min</strong> e gasta no máximo <strong>US$ {maxUSD.toFixed(2)}</strong>. Desliga sozinha
              nesses limites ou depois de 5 min sem ninguém assistindo. Não renova sozinha.
            </p>
            {err && <div className="err" role="alert">{err}</div>}
            <div style={{ display: "flex", gap: 10, flexWrap: "wrap" }}>
              <button type="button" className="cta" disabled={busy} onClick={() => void go()} data-act="confirmar">
                Confirmar
              </button>
              <button type="button" className="btn" onClick={() => { setOpen(false); setErr(""); }}>
                Cancelar
              </button>
            </div>
          </div>
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

// "Desligar": livre, sem confirmação; o cookie de login basta.
export function StopButton({ stopKey, onStopped }: { stopKey: string | null; onStopped: () => void }) {
  const [err, setErr] = useState("");
  const busy = useRef(false);
  async function stop() {
    if (busy.current) return;
    busy.current = true;
    setErr("");
    try {
      const r = await api.stop(stopKey ? { stop_key: stopKey } : {});
      if (r.ok) onStopped();
      else setErr("Não foi possível desligar.");
    } finally {
      busy.current = false;
    }
  }
  return (
    <>
      <button type="button" className="btn" data-act="desligar" onClick={() => void stop()}>
        Desligar
      </button>
      {err && <span className="err">{err}</span>}
    </>
  );
}
