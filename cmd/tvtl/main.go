// tvtl: pipeline de texto checado do TV Tá Ligado.
package main

import (
	"context"
	"errors"
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

	"github.com/gloffreda/tv-ta-ligado/internal/config"
	"github.com/gloffreda/tv-ta-ligado/internal/facts"
	"github.com/gloffreda/tv-ta-ligado/internal/ingest"
	"github.com/gloffreda/tv-ta-ligado/internal/llm"
	"github.com/gloffreda/tv-ta-ligado/internal/pipeline"
	"github.com/gloffreda/tv-ta-ligado/internal/report"
	"github.com/gloffreda/tv-ta-ligado/internal/rundown"
	"github.com/gloffreda/tv-ta-ligado/internal/store"
)

const usage = `uso: tvtl <comando> [opções]

  migrate                     aplica migrações
  ingest                      uma rodada de ingestão (RSS, BCB, clima)
  feeds-check                 valida as URLs de config/feeds.yaml
  rundown  --block B          mostra a pauta que seria montada (não grava)
  write    --block B          pauta + fatos + roteiro; grava segmento draft
  check    --segment ID       checa um segmento draft e decide o destino
  generate --block B          write + check
  run                         loop: ingestão a cada 5 min e blocos conforme schedule.yaml
  show     --last N [--status approved|rejected]
  stats                       falas cortadas e custo médio por bloco
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
	}
	m := &llm.Metered{Inner: inner, Prices: env.Prices, MaxDailyUSD: env.MaxDailyUSD, Ledger: st, Loc: env.Location}
	return &app{env: env, store: st, metered: m}, nil
}

type missingKey struct{}

func (missingKey) Complete(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{}, errors.New("ANTHROPIC_API_KEY ausente: geração desativada")
}

func (a *app) pipeline() *pipeline.Pipeline {
	return &pipeline.Pipeline{
		Store: a.store, LLM: a.metered, Budget: a.metered, Env: a.env, ConfigDir: a.env.ConfigDir,
		CandidateWindow: 36 * time.Hour, ReuseWindow: 6 * time.Hour,
	}
}

func (a *app) ingester() (*ingest.Ingester, error) {
	feeds, err := config.LoadFeeds(a.env.ConfigDir)
	if err != nil {
		return nil, err
	}
	return &ingest.Ingester{Store: a.store, HTTP: &http.Client{Timeout: 30 * time.Second}, Feeds: feeds,
		UA: a.env.UserAgent, Loc: a.env.Location, MaxAge: 48 * time.Hour}, nil
}

func dispatch(ctx context.Context, cmd string, args []string) (err error) {
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	block := fs.String("block", "", "bloco (noticias|economia|humor)")
	segment := fs.Int64("segment", 0, "id do segmento")
	last := fs.Int("last", 5, "quantos segmentos")
	status := fs.String("status", "approved", "status dos segmentos")
	listen := fs.String("listen", ":5432", "endereço de escuta")
	target := fs.String("target", "postgres:5432", "destino")
	_ = fs.Parse(args)

	switch cmd {
	case "debug-proxy":
		return debugProxy(ctx, *listen, *target)
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

	// Relatório em output/ ao fim de cada execução (exceto consultas).
	started := time.Now()
	if cmd != "show" && cmd != "stats" && cmd != "migrate" {
		defer func() {
			a.writeReport(report.Run{Command: cmd, Args: args, Started: started, Finished: time.Now(), Err: err})
		}()
	}
	err = a.exec(ctx, cmd, *block, *segment, *last, *status)
	return err
}

func (a *app) writeReport(r report.Run) {
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
		r := in.RunOnce(ctx)
		slog.Info("ingestão", "artigos_novos", r.Articles, "duplicados", r.Duplicates, "mercado", r.MarketFacts, "clima", r.WeatherFacts, "falhas", len(r.Failures))
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

// run é o loop principal.
func (a *app) run(ctx context.Context) error {
	if _, err := a.store.Migrate(ctx); err != nil {
		return err
	}
	in, err := a.ingester()
	if err != nil {
		return err
	}
	if a.env.AnthropicAPIKey == "" {
		slog.Warn("ANTHROPIC_API_KEY ausente: só a ingestão vai rodar")
	}
	slog.Info("tvtl run", "ingestao_a_cada", a.env.IngestInterval, "generate", a.env.Generate, "teto_usd_dia", a.env.MaxDailyUSD,
		"model_fast", a.env.ModelFast, "model_smart", a.env.ModelSmart)

	doIngest := func() {
		r := in.RunOnce(ctx)
		slog.Info("ingestão", "artigos_novos", r.Articles, "duplicados", r.Duplicates, "mercado", r.MarketFacts, "clima", r.WeatherFacts, "falhas", len(r.Failures))
	}
	doIngest()
	ingestT := time.NewTicker(a.env.IngestInterval)
	defer ingestT.Stop()
	genT := time.NewTicker(time.Minute)
	defer genT.Stop()
	p := a.pipeline()
	cycle := func() {
		start := time.Now()
		if n := a.generateDue(ctx, p); n > 0 {
			a.writeReport(report.Run{Command: "run-ciclo", Started: start, Finished: time.Now(),
				Notes: []string{fmt.Sprintf("Ciclo do loop com %d segmento(s) gerado(s)", n)}})
		}
	}
	cycle()
	for {
		select {
		case <-ctx.Done():
			slog.Info("encerrando")
			return nil
		case <-ingestT.C:
			doIngest()
		case <-genT.C:
			cycle()
		}
	}
}

// generateDue gera um segmento para cada bloco vencido segundo schedule.yaml
// e devolve quantos segmentos foram tentados.
func (a *app) generateDue(ctx context.Context, p *pipeline.Pipeline) (n int) {
	if a.env.AnthropicAPIKey == "" {
		return 0
	}
	if err := p.Guard(ctx); err != nil {
		slog.Debug("geração suspensa", "motivo", err)
		return 0
	}
	sched, err := config.LoadSchedule(a.env.ConfigDir)
	if err != nil {
		slog.Error("schedule.yaml", "erro", err)
		return 0
	}
	now := time.Now()
	for _, b := range sched.Blocks {
		if ctx.Err() != nil {
			return n
		}
		lastOK, okFound, err := a.store.LastSegmentAt(ctx, b.Name, "approved")
		if err != nil {
			slog.Error("agenda", "erro", err)
			return n
		}
		if okFound && now.Sub(lastOK) < b.Every.Duration {
			continue
		}
		lastTry, tryFound, _ := a.store.LastSegmentAt(ctx, b.Name, "rejected", "draft")
		if tryFound && now.Sub(lastTry) < sched.RetryAfter.Duration {
			continue
		}
		start := time.Now()
		res, err := p.Generate(ctx, b.Name)
		if res.SegmentID != 0 {
			n++
		}
		if err != nil {
			if errors.Is(err, llm.ErrBudget) || errors.Is(err, pipeline.ErrBlackout) || errors.Is(err, pipeline.ErrGenerateOff) {
				slog.Warn("geração parada", "motivo", err)
				return n
			}
			slog.Error("geração falhou", "bloco", b.Name, "segmento", res.SegmentID, "erro", err)
			continue
		}
		dropped := 0
		for _, o := range res.Outcomes {
			if o.Status == "dropped" {
				dropped++
			}
		}
		slog.Info("segmento", "bloco", b.Name, "id", res.SegmentID, "status", res.Status, "motivo", res.Reason,
			"falas", len(res.Outcomes), "cortadas", dropped, "duracao", time.Since(start).Round(time.Second))
	}
	return n
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

var speakerNames = map[string]string{"orlando": "ORLANDO", "duda": "DUDA"}

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
			for _, f := range l.Sources {
				fmt.Fprintf(w, "        ↳ fonte: %s — %s\n", f.SourceName, f.SourceURL)
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
