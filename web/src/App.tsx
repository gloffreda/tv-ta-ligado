import { useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { Player } from "./core/player";
import { badge } from "./core/badge";
import { load, save } from "./core/storage";
import { hhmm, itemLabel, NAMES } from "./core/labels";
import { sceneFor } from "./core/director";
import type { Source } from "./core/types";
import { Stage } from "./components/Stage";
import { Bubble } from "./components/Bubble";
import { Guide } from "./components/Guide";
import { OffAir, StopButton } from "./components/SessionControls";

type View = "pixel" | "vector";
const viewFromPath = (p: string): View => (p.replace(/\/+$/, "") === "/humano" ? "vector" : "pixel");

export function App() {
  const player = useMemo(() => new Player(), []);
  const snap = useSyncExternalStore(player.subscribe, player.getSnapshot);
  const [view, setView] = useState<View>(() => viewFromPath(location.pathname));
  const [captions, setCaptions] = useState(() => load("tvtl.captions", "on") !== "off");
  const [stopKey, setStopKey] = useState<string | null>(null);
  const [now, setNow] = useState(() => Date.now());
  const tv = useRef<HTMLDivElement>(null);

  useEffect(() => {
    void player.start();
    const t = setInterval(() => setNow(player.clock.now()), 15000);
    const onPop = () => setView(viewFromPath(location.pathname));
    window.addEventListener("popstate", onPop);
    return () => {
      player.stop();
      clearInterval(t);
      window.removeEventListener("popstate", onPop);
    };
  }, [player]);
  useEffect(() => setNow(player.clock.now()), [snap.offset, snap.item?.id, player]);

  const go = (v: View) => {
    if (v === view) return;
    history.pushState(null, "", v === "vector" ? "/humano" : "/");
    setView(v);
  };
  const toggleCaptions = () => {
    const c = !captions;
    setCaptions(c);
    save("tvtl.captions", c ? "on" : "off");
  };

  const sched = snap.schedule;
  const session = snap.session;
  const onAir = session ? session.on_air : true;
  const seal = badge(sched?.public_mode ?? "preview", sched?.blackout, now);
  const scene = sceneFor(snap.item).scene;
  const rigs = useMemo(
    () => Object.fromEntries(Object.entries(sched?.personas ?? {}).map(([id, p]) => [id, p.rigs ?? {}])),
    [sched?.personas],
  );
  const sources: Source[] = [];
  const seen = new Set<string>();
  for (const l of snap.item?.lines ?? []) {
    if (l.type !== "fact") continue;
    for (const s of l.sources ?? []) {
      const k = s.url + s.name;
      if (!seen.has(k)) seen.add(k), sources.push(s);
    }
  }
  const next = snap.next.find((n) => n.kind !== "bumper") ?? snap.next[0];

  return (
    <div className="page">
      <header className="top">
        <div className="wrap">
          <a href="#topo" className="logo">
            <span className="tl">TL</span>
            <span className="name">TV TÁ LIGADO</span>
          </a>
          <div className="switch" role="group" aria-label="Visual">
            <button type="button" aria-current={view === "pixel" ? "page" : undefined} onClick={() => go("pixel")} data-view="pixel">
              Pixel art
            </button>
            <button type="button" aria-current={view === "vector" ? "page" : undefined} onClick={() => go("vector")} data-view="vector">
              Humano
            </button>
          </div>
          <nav className="main" aria-label="Principal">
            <a href="#grade">Grade</a>
            <a href="#bancada">Bancada</a>
            <a href="#pauta">Mande sua pauta</a>
            <a href="#sobre">Sobre</a>
          </nav>
        </div>
      </header>

      <main id="topo">
        <section aria-label="Ao vivo" className="live">
          <div className="live-main">
            <div className="row">
              <div style={{ display: "flex", alignItems: "center", gap: 12 }}>
                <span className={`badge${seal === "AO VIVO" ? " live" : ""}`} data-badge>
                  <span className="dot" />
                  {seal}
                </span>
                <span className="vt" style={{ fontSize: 24 }}>Sempre ligada. Sempre checada.</span>
              </div>
              <span className="vt" style={{ fontSize: 22 }}>Canal 24 · notícias reais, bancada de IA</span>
            </div>

            <div className="cabinet">
              <div id="tv" ref={tv}>
                <Stage player={player} kind={view} rigs={rigs} />
                {captions && onAir && <Bubble line={snap.line} lineKey={snap.lineKey} scene={scene} />}
                {!onAir && <OffAir session={session} onStarted={(k) => setStopKey(k)} />}
                {onAir && snap.muted && (
                  <button type="button" className="unmute" onClick={() => player.setMuted(false)} data-act="mute">
                    Ligar o som
                  </button>
                )}
              </div>
              <div className="controls">
                <div className="left">
                  <button type="button" className="btn" aria-pressed={snap.muted} onClick={() => player.setMuted(!snap.muted)} id="btnMute">
                    {snap.muted ? "Som: desligado" : "Som: ligado"}
                  </button>
                  <button type="button" className="btn" aria-pressed={captions} onClick={toggleCaptions} id="btnCap">
                    {captions ? "Balões: ligados" : "Balões: desligados"}
                  </button>
                  <button type="button" className="btn" id="btnFull" onClick={() => tv.current?.requestFullscreen?.()}>
                    Tela cheia
                  </button>
                  {session?.active && <StopButton stopKey={stopKey} onStopped={() => { setStopKey(null); void player.refreshSession(); }} />}
                </div>
                <div className="leds" aria-hidden="true">
                  <span className={snap.connected ? "on" : ""} />
                  <span className={onAir ? "on" : ""} />
                  <span className="on" />
                </div>
              </div>
            </div>

            <div className="panels">
              <div className="card">
                <div className="kicker" style={{ color: "var(--red)" }}>NO AR</div>
                <div className="big" data-panel="noar">
                  {!onAir ? "Fora do ar" : snap.item ? itemLabel(snap.item) : "Estreia em breve"}
                </div>
                {onAir && sched?.now?.name && <div className="small">{sched.now.name}</div>}
              </div>
              <div className="card">
                <div className="kicker" style={{ color: "var(--muted)" }}>A SEGUIR</div>
                <div className="big" data-panel="aseguir">{onAir && next ? itemLabel(next) : "Notícias, economia e humor"}</div>
                {onAir && next && <div className="small">às {hhmm(next.starts_at)}</div>}
              </div>
            </div>
          </div>

          <aside id="fontes" className="fontes" aria-label="Fontes do segmento no ar">
            <div>
              <h2 className="px" style={{ fontSize: 13, color: "var(--yellow)" }}>Fontes deste bloco</h2>
              <p className="muted" style={{ margin: "8px 0 0" }}>Cada fato dito no ar aponta para a reportagem de origem. As piadas são nossas; os fatos, conferíveis.</p>
            </div>
            {!onAir || !snap.item ? (
              <p style={{ margin: 0, fontFamily: "var(--vt)", fontSize: 22 }}>As fontes de cada bloco aparecem aqui quando o canal estiver no ar.</p>
            ) : sources.length === 0 ? (
              <p style={{ margin: 0, fontFamily: "var(--vt)", fontSize: 22 }}>Este trecho não traz fatos novos.</p>
            ) : (
              <ul className="srcs" data-panel="fontes">
                {sources.map((s) => (
                  <li key={s.url + s.name}>
                    <div className="veh">{s.name}</div>
                    <a href={s.url} target="_blank" rel="noopener noreferrer">{s.title || s.url.replace(/^https?:\/\//, "").slice(0, 60)}</a>
                  </li>
                ))}
              </ul>
            )}
            <div className="note">Conteúdo gerado por inteligência artificial: roteiro, vozes e apresentadores são sintéticos.</div>
          </aside>
        </section>

        <div className="sr" aria-live="polite" data-live-region>
          {onAir && snap.line ? `${NAMES[snap.line.speaker] ?? snap.line.speaker}: ${snap.line.text}` : ""}
        </div>

        <section id="grade" aria-labelledby="grade-t" style={{ display: "flex", flexDirection: "column", gap: 20 }}>
          <div className="row" style={{ alignItems: "baseline" }}>
            <h2 id="grade-t" className="px" style={{ fontSize: 20, lineHeight: 1.5 }}>A grade</h2>
            <span className="vt" style={{ fontSize: 22 }}>Horário de Brasília</span>
          </div>
          <Guide schedule={sched} now={now} />
          <div className="grid4">
            <div className="tile"><h3>Bom dia, tá ligado?</h3><p>As manchetes da manhã, o dólar de ontem e o tempo nas capitais. Orlando já tomou café; Duda, não.</p></div>
            <div className="tile"><h3>Notícias</h3><p>Orlando conduz, Duda comenta. Tudo checado antes de ir ao ar.</p></div>
            <div className="tile"><h3>Economia traduzida</h3><p>Selic, inflação e câmbio explicados sem economês. Não é recomendação de investimento.</p></div>
            <div className="tile"><h3>Que fase!</h3><p>Duda pega as notícias mais estranhas do dia. Piada com a situação, nunca com pessoas.</p></div>
            <div className="tile"><h3>Tempo com Glória Garoa</h3><p>Às 6h50, 12h50, 18h50 e 21h50: as capitais por região e os alertas oficiais do INMET, lidos sem piada.</p></div>
          </div>
        </section>

        <section id="bancada" aria-labelledby="bancada-t" style={{ display: "flex", flexDirection: "column", gap: 20 }}>
          <h2 id="bancada-t" className="px" style={{ fontSize: 20, lineHeight: 1.5 }}>A bancada</h2>
          <div className="cast">
            <article>
              <svg viewBox="0 0 16 16" width="88" height="88" shapeRendering="crispEdges" aria-hidden="true" style={{ flex: "none", background: "#1D3A32" }}>
                <rect x="3" y="2" width="10" height="3" fill="#C9C6BE" /><rect x="4" y="4" width="8" height="7" fill="#D9A27A" /><rect x="5" y="6" width="1" height="1" fill="#1B1B1B" /><rect x="10" y="6" width="1" height="1" fill="#1B1B1B" /><rect x="6" y="9" width="4" height="1" fill="#B8B4AA" /><rect x="2" y="11" width="12" height="5" fill="#22324F" /><rect x="7" y="11" width="2" height="4" fill="#C0392B" />
              </svg>
              <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
                <h3>Orlando Pimenta</h3>
                <span className="vt" style={{ fontSize: 22, color: "var(--yellow)" }}>Âncora · 40 anos de bancada</span>
                <p className="muted">Terno impecável, desconfia de tudo que é digital e chama o algoritmo de “a máquina”.</p>
                <p className="quote">“Isso está checado? Então eu leio.”</p>
              </div>
            </article>
            <article>
              <svg viewBox="0 0 16 16" width="88" height="88" shapeRendering="crispEdges" aria-hidden="true" style={{ flex: "none", background: "#1D3A32" }}>
                <rect x="3" y="1" width="10" height="4" fill="#3A2340" /><rect x="2" y="3" width="2" height="7" fill="#3A2340" /><rect x="12" y="3" width="2" height="7" fill="#3A2340" /><rect x="4" y="4" width="8" height="7" fill="#8C5A3C" /><rect x="5" y="6" width="1" height="1" fill="#1B1B1B" /><rect x="10" y="6" width="1" height="1" fill="#1B1B1B" /><rect x="6" y="9" width="4" height="1" fill="#F3EEDF" /><rect x="2" y="11" width="12" height="5" fill="#F5C842" /><rect x="7" y="11" width="2" height="5" fill="#0F1B18" />
              </svg>
              <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
                <h3>Duda Faísca</h3>
                <span className="vt" style={{ fontSize: 22, color: "var(--yellow)" }}>Correspondente digital · 26 anos</span>
                <p className="muted">Veio da internet, traduz o noticiário para quem chegou agora e quer a cadeira do Orlando.</p>
                <p className="quote">“Tá ligado?”</p>
              </div>
            </article>
            <article>
              <svg viewBox="0 0 16 16" width="88" height="88" shapeRendering="crispEdges" aria-hidden="true" style={{ flex: "none", background: "#1D3A32" }}>
                <rect x="6" y="0" width="4" height="2" fill="#2A211A" /><rect x="3" y="2" width="10" height="3" fill="#2A211A" /><rect x="2" y="3" width="2" height="8" fill="#2A211A" /><rect x="12" y="3" width="2" height="8" fill="#2A211A" /><rect x="4" y="4" width="8" height="7" fill="#B57A55" /><rect x="5" y="6" width="1" height="1" fill="#1B1B1B" /><rect x="10" y="6" width="1" height="1" fill="#1B1B1B" /><rect x="6" y="9" width="4" height="1" fill="#C2185B" /><rect x="3" y="8" width="1" height="1" fill="#F5C842" /><rect x="12" y="8" width="1" height="1" fill="#F5C842" /><rect x="2" y="11" width="12" height="5" fill="#E58FB0" /><rect x="7" y="11" width="2" height="2" fill="#B57A55" />
              </svg>
              <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
                <h3>Glória Garoa</h3>
                <span className="vt" style={{ fontSize: 22, color: "var(--yellow)" }}>Meteorologia · vestido de gala</span>
                <p className="muted">A mais técnica da bancada. Trata a previsão como tapete vermelho e lê alerta oficial sem piada.</p>
                <p className="quote">“Leva o guarda-chuva. Mas leva com estilo.”</p>
              </div>
            </article>
          </div>
        </section>

        <section id="pauta" aria-labelledby="pauta-t" style={{ display: "flex", flexWrap: "wrap", gap: 16 }}>
          <div className="card" style={{ flex: "1 1 420px", padding: 24, display: "flex", flexDirection: "column", gap: 14 }}>
            <h2 id="pauta-t" className="px" style={{ fontSize: 16 }}>Mande sua pauta</h2>
            <p className="muted">Sugira um assunto e a bancada pode comentar ao vivo no quadro “Você pediu”. Um assunto a cada 2 minutos; só entra no ar o que tiver fonte.</p>
            <label className="f" htmlFor="pauta-tema">Assunto</label>
            <input id="pauta-tema" className="f" type="text" placeholder="Ex.: por que o dólar subiu hoje?" disabled />
            <label className="f" htmlFor="pauta-nome">Seu nome (opcional)</label>
            <input id="pauta-nome" className="f" type="text" disabled />
            <button type="button" className="cta" disabled style={{ alignSelf: "flex-start" }}>Abre na estreia</button>
          </div>
        </section>

        <section id="sobre" aria-labelledby="sobre-t" className="card" style={{ padding: 24, display: "flex", flexDirection: "column", gap: 12 }}>
          <h2 id="sobre-t" className="px" style={{ fontSize: 16 }}>Sobre a TV Tá Ligado</h2>
          <p className="muted" style={{ fontSize: 17 }}>A TV Tá Ligado é um canal de notícias 24 horas feito por inteligência artificial. As manchetes são reais e cada uma é creditada ao veículo que a publicou. Os apresentadores são personagens fictícios: o roteiro é escrito por IA, as vozes são sintéticas e cada afirmação de fato é conferida contra a fonte antes de ir ao ar. Sem fonte, não vai ao ar.</p>
          <p className="muted" style={{ fontSize: 17 }}>As opiniões e piadas da bancada são humor, não reportagem. Nada aqui é recomendação de investimento, aconselhamento jurídico ou médico. Confira a fonte original antes de compartilhar.</p>
          <p className="muted" style={{ fontSize: 17 }}>Correções e pedidos de remoção: pelos nossos canais oficiais.</p>
        </section>
      </main>

      <footer className="bottom">
        <div className="wrap">
          <span>© 2026 TV Tá Ligado</span>
          <div className="links">
            <a href="#topo">YouTube</a>
            <a href="#topo">Instagram</a>
            <a href="#topo">X</a>
          </div>
        </div>
      </footer>
    </div>
  );
}
