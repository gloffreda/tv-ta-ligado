import "@fontsource/press-start-2p";
import "@fontsource/vt323";
import "@fontsource/atkinson-hyperlegible/400.css";
import "@fontsource/atkinson-hyperlegible/700.css";
import "./styles.css";
import { useState } from "react";
import { createRoot } from "react-dom/client";
import { api } from "./core/api";

// Página de login no visual do site. A senha só sai no corpo do POST.
function Entrar() {
  const [pw, setPw] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  async function go(e: React.FormEvent) {
    e.preventDefault();
    if (!pw || busy) return;
    setBusy(true);
    setErr("");
    try {
      const r = await api.login(pw);
      setPw("");
      if (r.status === 204) {
        location.replace("/");
        return;
      }
      setErr(r.status === 429 ? r.error || "Muitas tentativas. Espere um pouco." : r.status === 401 ? "Senha incorreta." : "Não foi possível entrar agora.");
    } catch {
      setErr("Sem conexão com o canal.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="page" style={{ display: "flex", flexDirection: "column" }}>
      <header className="top">
        <div className="wrap">
          <span className="logo">
            <span className="tl">TL</span>
            <span className="name">TV TÁ LIGADO</span>
          </span>
        </div>
      </header>
      <main style={{ flex: 1, alignItems: "center", justifyContent: "center" }}>
        <form className="modal-box" style={{ position: "static", width: "min(440px, 100%)" }} onSubmit={go} data-login>
          <h1 className="px" style={{ fontSize: 14, color: "var(--yellow)", margin: 0, lineHeight: 1.6 }}>Entrar</h1>
          <p className="muted" style={{ margin: 0 }}>O canal está em teste fechado. Digite a senha para assistir.</p>
          <label className="f" htmlFor="senha">Senha</label>
          <input id="senha" className="f" type="password" autoComplete="current-password" autoFocus value={pw} onChange={(e) => setPw(e.target.value)} />
          {err && <div className="err" role="alert">{err}</div>}
          <button type="submit" className="cta" disabled={busy || !pw} style={{ alignSelf: "flex-start" }}>
            Entrar
          </button>
        </form>
      </main>
      <footer className="bottom">
        <div className="wrap">
          <span>© 2026 TV Tá Ligado</span>
        </div>
      </footer>
    </div>
  );
}

createRoot(document.getElementById("root")!).render(<Entrar />);
