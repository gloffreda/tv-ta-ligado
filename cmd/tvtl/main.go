// tvtl: pipeline de texto checado do TV Tá Ligado.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"
	_ "time/tzdata"

	"github.com/mmcdole/gofeed"

	"github.com/gloffreda/tv-ta-ligado/internal/audit"
	"github.com/gloffreda/tv-ta-ligado/internal/check"
	"github.com/gloffreda/tv-ta-ligado/internal/config"
	"github.com/gloffreda/tv-ta-ligado/internal/facts"
	"github.com/gloffreda/tv-ta-ligado/internal/ingest"
	"github.com/gloffreda/tv-ta-ligado/internal/lipsync"
	"github.com/gloffreda/tv-ta-ligado/internal/llm"
	"github.com/gloffreda/tv-ta-ligado/internal/persona"
	"github.com/gloffreda/tv-ta-ligado/internal/pipeline"
	"github.com/gloffreda/tv-ta-ligado/internal/report"
	"github.com/gloffreda/tv-ta-ligado/internal/rundown"
	"github.com/gloffreda/tv-ta-ligado/internal/store"
	"github.com/gloffreda/tv-ta-ligado/internal/timeline"
	"github.com/gloffreda/tv-ta-ligado/internal/tts"
)

const usage = `uso: tvtl <comando> [opções]

  migrate                     aplica migrações
  ingest                      uma rodada de ingestão (RSS, BCB, clima), sem LLM
  glossary                    valida as fontes e carrega config/glossary.yaml
  feeds-check                 valida as URLs de config/feeds.yaml
  rundown  --block B          mostra a pauta que seria montada (não grava)
  write    --block B          pauta + fatos + roteiro; grava segmento draft
  check    --segment ID       checa um segmento draft e decide o destino
  generate --block B          write + check
  run                         loop: ingestão a cada 5 min e blocos conforme schedule.yaml
  show     --last N [--status approved|rejected]
  stats                       falas cortadas e custo médio por bloco
  audit [--dir D] [--banter-model M]   auditoria adversarial com o juiz real
  tts-bench [--providers a,b] [--seconds 60]   fator de tempo real dos TTS locais
  audition [--out audition]   audição às cegas das vozes candidatas
  voice --segment ID          sintetiza as falas de um segmento aprovado
  serve [--listen :8080]      API da linha do tempo (/v1/now, /v1/timeline, /v1/events, /media)
  render --from now --minutes 15 --out out   MP3 + legendas.srt da linha do tempo
  debug-proxy --listen :5432 --target postgres:5432
`

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := dispatch(ctx, os.Args[1], os.Args[2:]); err != nil {
		slog.Error("falhou", "comando", os.Args[1], "erro", err)
		os.Exit(1)
	}
}

type app struct {
	env     config.Env
	store   *store.Store
	metered *llm.Metered
	sched   *timeline.Scheduler // só no run (para o relatório)
}

func newApp(ctx context.Context) (*app, error) {
	env, err := config.LoadEnv()
	if err != nil {
		return nil, err
	}
	st, err := store.Open(ctx, env.DatabaseURL)
	if err != nil {
		return nil, err
	}
	// Migrações são idempotentes: todo comando garante o esquema atual.
	if _, err := st.Migrate(ctx); err != nil {
		st.Close()
		return nil, err
	}
	var inner llm.Client = missingKey{}
	if env.AnthropicAPIKey != "" {
		inner = llm.NewAnthropic(env.AnthropicAPIKey)
		slog.Info("ANTHROPIC_API_KEY: presente")
	} else {
		slog.Warn("ANTHROPIC_API_KEY: ausente")
	}
	m := &llm.Metered{Inner: inner, Prices: env.Prices, MaxDailyUSD: env.MaxDailyUSD, Ledger: st, Loc: env.Location,
		Now: func() time.Time { return time.Now().Add(env.ClockOffset) }}
	if env.ClockOffset != 0 {
		slog.Warn("CLOCK_OFFSET ativo: esta instância roda deslocada no tempo", "offset", env.ClockOffset)
	}
	return &app{env: env, store: st, metered: m}, nil
}

type missingKey struct{}

func (missingKey) Complete(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{}, llm.ErrNoAPIKey
}

func (a *app) pipeline() *pipeline.Pipeline {
	return &pipeline.Pipeline{
		Store: a.store, LLM: a.metered, Budget: a.metered, Env: a.env, ConfigDir: a.env.ConfigDir, Now: a.now,
		CandidateWindow: 36 * time.Hour, ReuseWindow: 6 * time.Hour,
	}
}

func (a *app) ingester() (*ingest.Ingester, error) {
	feeds, err := config.LoadFeeds(a.env.ConfigDir)
	if err != nil {
		return nil, err
	}
	return &ingest.Ingester{Store: a.store, HTTP: &http.Client{Timeout: 30 * time.Second}, Feeds: feeds,
		UA: a.env.UserAgent, Loc: a.env.Location, MaxAge: 48 * time.Hour, Now: a.now}, nil
}

func dispatch(ctx context.Context, cmd string, args []string) (err error) {
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	block := fs.String("block", "", "bloco (noticias|economia|humor)")
	segment := fs.Int64("segment", 0, "id do segmento")
	last := fs.Int("last", 5, "quantos segmentos")
	status := fs.String("status", "approved", "status dos segmentos")
	listen := fs.String("listen", ":5432", "endereço de escuta")
	auditDir := fs.String("dir", "testdata/adversarial", "casos da auditoria")
	banterModel := fs.String("banter-model", "", "modelo do juiz para banter (auditoria)")
	rhubarb := fs.String("rhubarb", "/opt/rhubarb/rhubarb", "binário do Rhubarb")
	providers := fs.String("providers", "chatterbox,kokoro,piper", "provedores do benchmark")
	seconds := fs.Int("seconds", 60, "segundos de áudio por provedor no benchmark")
	outDir := fs.String("out", "audition", "pasta de saída (audition, render)")
	from := fs.String("from", "now", "início do render: now, -15m, +5m ou RFC 3339")
	minutes := fs.Int("minutes", 15, "minutos do render")
	target := fs.String("target", "postgres:5432", "destino")
	_ = fs.Parse(args)

	switch cmd {
	case "debug-proxy":
		return debugProxy(ctx, *listen, *target)
	case "lipsync-server":
		return lipsync.Serve(ctx, *listen, *rhubarb)
	case "tts-bench":
		return ttsBench(ctx, *providers, *seconds)
	case "feeds-check":
		return feedsCheck(ctx)
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	}

	a, err := newApp(ctx)
	if err != nil {
		return err
	}
	defer a.store.Close()

	// Comandos que usam LLM falham já no início, antes de ingerir, se não há chave.
	switch cmd {
	case "ingest", "run", "rundown", "write", "check", "generate", "glossary", "audit":
		if a.env.AnthropicAPIKey == "" {
			return fmt.Errorf("ANTHROPIC_API_KEY ausente: defina a chave no .env (veja .env.example) e rode de novo; o comando %q não foi executado", cmd)
		}
	}

	// Relatório em output/ ao fim de cada execução (exceto consultas).
	started := time.Now()
	if cmd != "show" && cmd != "stats" && cmd != "migrate" {
		defer func() {
			a.writeReport(report.Run{Command: cmd, Args: args, Started: started, Finished: time.Now(), Err: err})
		}()
	}
	switch cmd {
	case "audit":
		return a.audit(ctx, *auditDir, *banterModel)
	case "audition":
		return a.audition(ctx, *outDir)
	case "serve":
		return a.serve(ctx, *listen)
	case "render":
		return a.render(ctx, *from, *minutes, *outDir)
	case "voice":
		v, err := a.voicer(ctx)
		if err != nil {
			return err
		}
		personas, err := persona.Load(a.env.ConfigDir)
		if err != nil {
			return err
		}
		n, err := v.VoiceSegment(ctx, *segment, personas)
		fmt.Printf("segmento %d: %d falas sintetizadas\n", *segment, n)
		return err
	}
	err = a.exec(ctx, cmd, *block, *segment, *last, *status)
	return err
}

// audit roda a auditoria adversarial com o juiz real e grava o resultado em output/.
func (a *app) audit(ctx context.Context, dir, banterModel string) error {
	cases, err := audit.Load(dir)
	if err != nil {
		return err
	}
	sched, err := config.LoadSchedule(a.env.ConfigDir)
	if err != nil {
		return err
	}
	lex, _, err := a.pipeline().Lexicon(sched)
	if err != nil {
		return err
	}
	if banterModel == "" {
		banterModel = a.env.JudgeBanterModel
	}
	judge := &check.Judge{LLM: a.metered, Model: a.env.ModelSmart, BanterModel: banterModel}
	start := time.Now()
	rs, err := audit.Run(ctx, cases, lex, judge, 6)
	if err != nil {
		slog.Warn("auditoria: falha de LLM em algum caso (contado como reprovação)", "erro", err)
	}
	cost, _ := a.store.CostSince(ctx, start)
	title := fmt.Sprintf("Auditoria adversarial — juiz fact: %s · juiz banter: %s", a.env.ModelSmart, banterModel)
	md := audit.Report(rs, title, cost["judge"])
	fmt.Println(md)
	_ = os.MkdirAll(a.env.OutputDir, 0o755)
	path := fmt.Sprintf("%s/auditoria-%s.md", a.env.OutputDir, time.Now().In(a.env.Location).Format("20060102-150405"))
	if err := os.WriteFile(path, []byte(md), 0o644); err == nil {
		slog.Info("auditoria", "arquivo", path)
	}
	_, fn, fp, _ := audit.Matrix(rs)
	if fn > 0 || fp > 2 {
		return fmt.Errorf("auditoria fora da meta: %d falsos negativos, %d falsos positivos", fn, fp)
	}
	return nil
}

func (a *app) writeReport(r report.Run) {
	if a.sched != nil {
		r.MinBuffer, _ = a.sched.MinBuffer()
		r.BufferGoal = a.env.BufferMin
	}
	if sched, err := config.LoadSchedule(a.env.ConfigDir); err == nil {
		r.Every = map[string]time.Duration{}
		for _, b := range sched.Blocks {
			r.Every[b.Name] = b.Every.Duration
		}
	}
	now := r.Finished.In(a.env.Location)
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, a.env.Location)
	path, err := report.Write(context.Background(), a.env.OutputDir, a.store, r, a.env.Location, day)
	if err != nil {
		slog.Warn("relatório não gravado", "erro", err)
		return
	}
	slog.Info("relatório", "arquivo", path)
}

func (a *app) exec(ctx context.Context, cmd, blockName string, segment int64, last int, status string) error {
	switch cmd {
	case "migrate":
		applied, err := a.store.Migrate(ctx)
		if err != nil {
			return err
		}
		slog.Info("migrações", "aplicadas", applied)
	case "ingest":
		in, err := a.ingester()
		if err != nil {
			return err
		}
		a.ingestOnce(ctx, in)
	case "glossary":
		in, err := a.ingester()
		if err != nil {
			return err
		}
		return a.loadGlossary(ctx, in)
	case "rundown":
		return a.showRundown(ctx, blockName)
	case "write":
		res, err := a.pipeline().Draft(ctx, blockName)
		if err != nil {
			return err
		}
		fmt.Printf("segmento %d: %s %s\n", res.SegmentID, res.Status, res.Reason)
	case "check":
		res, err := a.pipeline().CheckDraft(ctx, segment)
		if err != nil {
			return err
		}
		fmt.Printf("segmento %d: %s %s\n", res.SegmentID, res.Status, res.Reason)
	case "generate":
		res, err := a.pipeline().Generate(ctx, blockName)
		if err != nil {
			return err
		}
		fmt.Printf("segmento %d: %s %s\n", res.SegmentID, res.Status, res.Reason)
	case "run":
		return a.run(ctx)
	case "show":
		segs, err := a.store.Segments(ctx, status, last)
		if err != nil {
			return err
		}
		printSegments(os.Stdout, segs, a.env.Location)
	case "stats":
		return a.stats(ctx)
	default:
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("comando desconhecido: %s", cmd)
	}
	return nil
}

func (a *app) showRundown(ctx context.Context, blockName string) error {
	sched, err := config.LoadSchedule(a.env.ConfigDir)
	if err != nil {
		return err
	}
	b, ok := sched.Block(blockName)
	if !ok {
		return fmt.Errorf("bloco %q não existe", blockName)
	}
	now := time.Now()
	cands, err := a.store.CandidateArticles(ctx, now.Add(-36*time.Hour), now.Add(-6*time.Hour), 200)
	if err != nil {
		return err
	}
	market, _ := a.store.LatestFactsByKind(ctx, facts.Market, now)
	weather, _ := a.store.LatestFactsByKind(ctx, facts.Weather, now)
	plan, err := (&rundown.Planner{LLM: a.metered, Model: a.env.ModelFast}).Plan(ctx, b, cands, market, weather, now.In(a.env.Location))
	if err != nil {
		return err
	}
	fmt.Printf("Pauta de %s (%d candidatas):\n", blockName, len(cands))
	for i, art := range plan.Articles {
		fmt.Printf("  %d. [%d] %s (%s)\n", i+1, art.ID, art.Title, art.SourceName)
	}
	for _, f := range append(plan.Market, plan.Weather...) {
		fmt.Printf("  · [fato %d] %s\n", f.ID, f.Claim)
	}
	return nil
}

// ingestOnce: uma rodada de ingestão. Sem LLM: guarda título, resumo e (CC BY)
// corpo; os fatos são extraídos só das matérias que a pauta escolher.
func (a *app) ingestOnce(ctx context.Context, in *ingest.Ingester) {
	r := in.RunOnce(ctx)
	slog.Info("ingestão", "artigos_novos", r.Articles, "duplicados", r.Duplicates, "mercado", r.MarketFacts, "clima", r.WeatherFacts, "falhas", len(r.Failures))
}

// loadGlossary valida as fontes e grava o glossário.
func (a *app) loadGlossary(ctx context.Context, in *ingest.Ingester) error {
	terms, err := config.LoadGlossary(a.env.ConfigDir)
	if err != nil {
		return err
	}
	r := in.LoadGlossary(ctx, terms, pipeline.GlossarySeries)
	slog.Info("glossário", "carregados", len(r.Loaded), "fora", len(r.Rejected))
	for t, why := range r.Rejected {
		slog.Warn("glossário: termo fora", "termo", t, "motivo", why)
	}
	return nil
}

func (a *app) stats(ctx context.Context) error {
	st, err := a.store.Stats(ctx)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "bloco\tsegmentos\taprovados\tfalas\tcortadas\ttaxa_corte\treescritas\tcusto_medio_usd")
	for _, b := range st {
		rate := 0.0
		if b.Lines > 0 {
			rate = 100 * float64(b.Dropped) / float64(b.Lines)
		}
		fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%d\t%.1f%%\t%d\t%.4f\n", b.Block, b.Segments, b.Approved, b.Lines, b.Dropped, rate, b.Rewritten, b.AvgCostUSD)
	}
	return w.Flush()
}

var speakerNames = map[string]string{"orlando": "ORLANDO", "duda": "DUDA", "gloria": "GLÓRIA"}

func printSegments(w io.Writer, segs []store.SegmentView, loc *time.Location) {
	if len(segs) == 0 {
		fmt.Fprintln(w, "Nenhum segmento.")
		return
	}
	for _, s := range segs {
		fmt.Fprintf(w, "\n══ Segmento #%d · %s · %s · %s · US$ %.4f · %d tentativa(s) de roteiro\n",
			s.ID, s.Block, s.Status, s.CreatedAt.In(loc).Format("02/01/2006 15:04"), s.CostUSD, s.Attempts)
		if s.Reason != nil {
			fmt.Fprintf(w, "   motivo: %s\n", *s.Reason)
		}
		var dropped []store.LineView
		for _, l := range s.Lines {
			if l.Status == "dropped" {
				dropped = append(dropped, l)
				continue
			}
			tag := map[string]string{"fact": "fato", "banter": "papo"}[l.Type]
			if l.Status == "rewritten" {
				tag += " · reescrita"
			}
			name := speakerNames[l.Speaker]
			if name == "" {
				name = strings.ToUpper(l.Speaker)
			}
			fmt.Fprintf(w, "  %02d %-8s [%s] %s\n", l.Seq, name, tag, l.Text)
			seen := map[string]bool{} // vários fatos do mesmo artigo: uma linha de fonte
			for _, f := range l.Sources {
				if !seen[f.SourceURL] {
					seen[f.SourceURL] = true
					fmt.Fprintf(w, "        ↳ fonte: %s — %s\n", f.SourceName, f.SourceURL)
				}
			}
		}
		if len(dropped) > 0 {
			fmt.Fprintln(w, "  ✂ falas cortadas:")
			for _, l := range dropped {
				reason := ""
				if l.RejectReason != nil {
					reason = *l.RejectReason
				}
				text := l.Text
				if l.OriginalText != nil {
					text = *l.OriginalText
				}
				fmt.Fprintf(w, "  %02d %-8s %q\n        motivo: %s\n", l.Seq, speakerNames[l.Speaker], text, reason)
			}
		}
	}
}

func feedsCheck(ctx context.Context) error {
	env, err := config.LoadEnv()
	if err != nil {
		return err
	}
	feeds, err := config.LoadFeeds(env.ConfigDir)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "fonte\tstatus\titens\tmais_recente\tcom_corpo\turl")
	bad := 0
	for _, f := range feeds.RSS {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, f.URL, nil)
		req.Header.Set("User-Agent", env.UserAgent)
		resp, err := client.Do(req)
		if err != nil {
			fmt.Fprintf(w, "%s\tERRO %v\t\t\t\t%s\n", f.Name, err, f.URL)
			bad++
			continue
		}
		feed, err := gofeed.NewParser().Parse(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != 200 {
			fmt.Fprintf(w, "%s\tERRO HTTP %d %v\t\t\t\t%s\n", f.Name, resp.StatusCode, err, f.URL)
			bad++
			continue
		}
		var newest time.Time
		for _, it := range feed.Items {
			if it.PublishedParsed != nil && it.PublishedParsed.After(newest) {
				newest = *it.PublishedParsed
			}
		}
		arts := ingest.ParseItems(feed, f)
		withBody := 0
		for _, a := range arts {
			if a.Body != nil {
				withBody++
			}
		}
		age := "?"
		if !newest.IsZero() {
			age = time.Since(newest).Round(time.Minute).String()
		}
		fmt.Fprintf(w, "%s\tok\t%d\t%s atrás\t%d\t%s\n", f.Name, len(arts), age, withBody, f.URL)
	}
	w.Flush()
	if bad > 0 {
		return fmt.Errorf("%d feed(s) com falha", bad)
	}
	return nil
}

func debugProxy(ctx context.Context, listen, target string) error {
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}
	go func() { <-ctx.Done(); ln.Close() }()
	slog.Info("debug-proxy", "listen", listen, "target", target)
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go func(c net.Conn) {
			defer c.Close()
			t, err := net.DialTimeout("tcp", target, 5*time.Second)
			if err != nil {
				slog.Warn("debug-proxy", "erro", err)
				return
			}
			defer t.Close()
			go io.Copy(t, c)
			io.Copy(c, t)
		}(c)
	}
}

// localURL: endereço do container de um provedor local (TTS_<NOME>_URL).
func localURL(name string) string {
	if u := os.Getenv("TTS_" + strings.ToUpper(name) + "_URL"); u != "" {
		return u
	}
	return "http://tts-" + name + ":8080"
}

// ttsBench mede o fator de tempo real de cada provedor local.
func ttsBench(ctx context.Context, list string, seconds int) error {
	var rows []tts.BenchResult
	for _, name := range strings.Split(list, ",") {
		name = strings.TrimSpace(name)
		l := &tts.Local{ProviderName: name, URL: localURL(name), HTTP: &http.Client{Timeout: 10 * time.Minute}}
		voices, err := l.Voices(ctx)
		if err != nil || len(voices) == 0 {
			rows = append(rows, tts.BenchResult{Provider: name, Err: fmt.Sprintf("indisponível: %v", err)})
			continue
		}
		r := tts.Bench(ctx, l, tts.Voice{Provider: name, Name: voices[0], Rate: 1}, time.Duration(seconds)*time.Second)
		slog.Info("benchmark", "resultado", r.String(), "erro", r.Err)
		rows = append(rows, r)
	}
	fmt.Println("| provedor | voz | áudio (s) | processamento (s) | RTF | min de áudio/hora | requisições | erro |")
	fmt.Println("|---|---|---|---|---|---|---|---|")
	for _, r := range rows {
		fmt.Printf("| %s | %s | %.1f | %.1f | %.2f | %.0f | %d | %s |\n", r.Provider, r.Voice, r.AudioSec, r.ProcSec, r.RTF, r.MinPerHour, r.Requests, r.Err)
	}
	return nil
}
