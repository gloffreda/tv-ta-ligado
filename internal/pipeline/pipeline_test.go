package pipeline

// Testes de integração: Postgres efêmero (serviço postgres-test, tmpfs) e
// LLM mockado com respostas gravadas em testdata/llm. Nunca tocam o banco principal.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gloffreda/tv-ta-ligado/internal/config"
	"github.com/gloffreda/tv-ta-ligado/internal/facts"
	"github.com/gloffreda/tv-ta-ligado/internal/llm"
	"github.com/gloffreda/tv-ta-ligado/internal/report"
	"github.com/gloffreda/tv-ta-ligado/internal/store"
	"github.com/gloffreda/tv-ta-ligado/internal/testfix"
)

const mockModel = "mock-model"

func testStore(t *testing.T) *store.Store {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL não definido (rode `make test`)")
	}
	if !strings.Contains(url, "postgres-test") && os.Getenv("TVTL_ALLOW_ANY_TEST_DB") == "" {
		t.Fatalf("recusando rodar testes fora do postgres-test: %s", url)
	}
	ctx := context.Background()
	st, err := store.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if _, err := st.DB.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return st
}

type fixture struct {
	st       *store.Store
	articles map[string]int64
	facts    map[int64]int64 // id da fixture -> id no banco
}

func seed(t *testing.T, st *store.Store) fixture {
	ctx := context.Background()
	fx := fixture{st: st, articles: map[string]int64{}, facts: map[int64]int64{}}
	src, err := st.UpsertSource(ctx, "Agência Fictícia", "rss", "https://exemplo.invalid/rss", "cc-by")
	if err != nil {
		t.Fatal(err)
	}
	var arts []struct{ Key, URL, Title, Summary, Body string }
	b, _ := os.ReadFile(testfix.Path("testdata", "articles.json"))
	if err := json.Unmarshal(b, &arts); err != nil {
		t.Fatal(err)
	}
	pub := testfix.Now.Add(-2 * time.Hour)
	for _, a := range arts {
		body := a.Body
		if _, err := st.InsertArticle(ctx, store.Article{SourceID: src, URL: a.URL, Title: a.Title, TitleHash: a.Key, Summary: a.Summary, Body: &body, PublishedAt: &pub}); err != nil {
			t.Fatal(err)
		}
		var id int64
		_ = st.DB.QueryRow(ctx, `SELECT id FROM articles WHERE url=$1`, a.URL).Scan(&id)
		fx.articles[a.Key] = id
	}
	// Fatos fictícios de clima e um fato de outra notícia (entidade conhecida).
	for _, f := range testfix.Facts(t) {
		if f.Kind == facts.Weather || f.ID == 10 {
			id, err := st.UpsertFact(ctx, f)
			if err != nil {
				t.Fatal(err)
			}
			fx.facts[f.ID] = id
		}
	}
	return fx
}

func testEnv() config.Env {
	return config.Env{
		ModelFast: mockModel, ModelSmart: mockModel, Generate: true, Location: testfix.Loc(),
		MaxDailyUSD: 100, MemoryHalfLife: 7,
		Prices: map[string]config.Price{mockModel: {InputPerMTok: 1, OutputPerMTok: 5}},
	}
}

func newPipeline(st *store.Store, mock llm.Client, env config.Env, now time.Time) (*Pipeline, *llm.Metered) {
	m := &llm.Metered{Inner: mock, Prices: env.Prices, MaxDailyUSD: env.MaxDailyUSD, Ledger: st, Loc: env.Location, Now: func() time.Time { return now }}
	return &Pipeline{
		Store: st, LLM: m, Budget: m, Env: env, ConfigDir: testfix.Path("config"),
		Now: func() time.Time { return now }, CandidateWindow: 36 * time.Hour, ReuseWindow: 6 * time.Hour,
	}, m
}

var factLineRe = regexp.MustCompile(`(?m)^\[(\d+)\] `)

// fill troca {{n}} pelo id do n-ésimo fato listado no prompt e {{a1}} pelos artigos.
func fill(tmpl, prompt string, arts map[string]int64) string {
	ids := factLineRe.FindAllStringSubmatch(prompt, -1)
	out := tmpl
	for i := len(ids); i >= 1; i-- {
		out = strings.ReplaceAll(out, fmt.Sprintf("{{%d}}", i), ids[i-1][1])
	}
	for k, v := range arts {
		out = strings.ReplaceAll(out, "{{"+k+"}}", fmt.Sprint(v))
	}
	return out
}

// recorded responde com as gravações de testdata/llm conforme o propósito.
func recorded(t *testing.T, fx fixture) *llm.Mock {
	m := llm.NewMock(nil)
	m.Handler = func(r llm.Request) (string, error) {
		switch r.Purpose {
		case "rundown":
			return fill(testfix.LLM(t, "rundown.json"), "", fx.articles), nil
		case "extract":
			if strings.Contains(r.Prompt, "panda") {
				return testfix.LLM(t, "extract_a2.json"), nil
			}
			return testfix.LLM(t, "extract_a1.json"), nil
		case "script":
			return fill(testfix.LLM(t, "script_noticias.json"), r.Prompt, nil), nil
		case "rewrite":
			if strings.Contains(r.Prompt, "15 anos") {
				return fill(testfix.LLM(t, "rewrite_panda.json"), r.Prompt, nil), nil
			}
			return testfix.LLM(t, "rewrite_iluminacao.json"), nil
		case "judge":
			return testfix.LLM(t, "judge_ok.json"), nil
		case "memory":
			return testfix.LLM(t, "memory.json"), nil
		}
		return "", fmt.Errorf("propósito inesperado %q", r.Purpose)
	}
	return m
}

func TestGenerateEndToEnd(t *testing.T) {
	st := testStore(t)
	fx := seed(t, st)
	mock := recorded(t, fx)
	p, _ := newPipeline(st, mock, testEnv(), testfix.Now)
	ctx := context.Background()

	res, err := p.Generate(ctx, "noticias")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "approved" {
		t.Fatalf("status=%s motivo=%s", res.Status, res.Reason)
	}
	segs, err := st.Segments(ctx, "approved", 1)
	if err != nil || len(segs) != 1 {
		t.Fatalf("segmentos aprovados: %v %v", segs, err)
	}
	seg := segs[0]
	if len(seg.Lines) != 16 {
		t.Fatalf("falas=%d", len(seg.Lines))
	}
	byseq := map[int]store.LineView{}
	for _, l := range seg.Lines {
		byseq[l.Seq] = l
	}
	if l := byseq[10]; l.Status != "rewritten" || l.OriginalText == nil || !strings.Contains(l.Text, "12 anos") {
		t.Fatalf("fala 10 deveria ter sido reescrita: %+v", l)
	}
	if l := byseq[14]; l.Status != "dropped" || l.RejectReason == nil || !strings.Contains(*l.RejectReason, "inexistente") {
		t.Fatalf("fala 14 deveria ter sido cortada: %+v", l)
	}
	for _, l := range seg.Lines {
		if l.Type == "fact" && l.Status != "dropped" && len(l.Sources) == 0 {
			t.Fatalf("fala fact %d sem fonte", l.Seq)
		}
	}
	if mock.CallsFor("rewrite") != 2 {
		t.Fatalf("reescritas=%d, want 2", mock.CallsFor("rewrite"))
	}

	// O fato com número inventado ("4 faixas", "R$ 50 milhões") foi descartado.
	var bad int
	_ = st.DB.QueryRow(ctx, `SELECT count(*) FROM facts WHERE claim LIKE '%4 faixas%'`).Scan(&bad)
	if bad != 0 {
		t.Fatal("fato com número fora da fonte não pode ser gravado")
	}

	// check_log: cada fala passou pelo determinístico; as aprovadas pelo juiz.
	var logs, judges int
	_ = st.DB.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE stage='judge') FROM check_log`).Scan(&logs, &judges)
	if logs < 16 || judges < 14 {
		t.Fatalf("check_log=%d juiz=%d", logs, judges)
	}

	// Custo: toda chamada registrada e atribuída ao segmento.
	var calls, orphan int
	var sum float64
	_ = st.DB.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE segment_id IS NULL), COALESCE(sum(cost_usd),0)::float8 FROM llm_calls`).Scan(&calls, &orphan, &sum)
	if calls != len(mock.Calls) || orphan != 0 {
		t.Fatalf("llm_calls=%d (órfãs %d), chamadas=%d", calls, orphan, len(mock.Calls))
	}
	if seg.CostUSD <= 0 || absf(seg.CostUSD-sum) > 1e-6 {
		t.Fatalf("custo do segmento %.6f != soma %.6f", seg.CostUSD, sum)
	}

	// Memória: a que cita pessoa real é descartada.
	var mems int
	_ = st.DB.QueryRow(ctx, `SELECT count(*) FROM persona_memory`).Scan(&mems)
	if mems != 2 {
		t.Fatalf("memórias=%d, want 2", mems)
	}
	var leaked int
	_ = st.DB.QueryRow(ctx, `SELECT count(*) FROM persona_memory WHERE content LIKE '%Marta%'`).Scan(&leaked)
	if leaked != 0 {
		t.Fatal("memória sobre pessoa real não pode ser gravada")
	}

	// O roteirista recebeu só fatos da pauta e as memórias (nenhuma ainda).
	for _, c := range mock.Calls {
		if c.Purpose == "script" && strings.Contains(c.Prompt, "Caio Brandão") {
			t.Fatal("o prompt do roteirista não pode conter fatos fora da pauta")
		}
	}

	// Uma segunda geração do mesmo bloco não reusa os artigos (janela de reuso).
	cands, _ := st.CandidateArticles(ctx, testfix.Now.Add(-36*time.Hour), testfix.Now.Add(-6*time.Hour), 10)
	if len(cands) != 0 {
		t.Fatalf("artigos já pautados não deveriam voltar: %d", len(cands))
	}
}

func TestReportAfterRun(t *testing.T) {
	st := testStore(t)
	fx := seed(t, st)
	p, _ := newPipeline(st, recorded(t, fx), testEnv(), testfix.Now)
	started := time.Now().Add(-time.Second)
	if _, err := p.Generate(context.Background(), "noticias"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path, err := report.Write(context.Background(), dir, st, report.Run{Command: "generate", Args: []string{"--block", "noticias"}, Started: started, Finished: time.Now()}, testfix.Loc(), started.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	md := string(b)
	for _, want := range []string{"tvtl generate --block noticias", "| noticias | approved |", "Falas cortadas no segmento", "inexistente", "Gasto com LLM hoje",
		"| headline | 7 | 0 | 7 |", "Fatos extraídos pelo LLM: 7 · aceitos pela validação literal: 6 · descartados: 1 (taxa de descarte 14.3%)"} {
		if !strings.Contains(md, want) {
			t.Errorf("relatório sem %q:\n%s", want, md)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("um único arquivo por execução, veio %d", len(entries))
	}
}

func TestExtractPendingIgnoresGenerateOff(t *testing.T) {
	st := testStore(t)
	fx := seed(t, st)
	mock := recorded(t, fx)
	env := testEnv()
	env.Generate = false
	p, _ := newPipeline(st, mock, env, testfix.Now)
	r, err := p.ExtractPending(context.Background(), ExtractLimit)
	if err != nil {
		t.Fatal(err)
	}
	if r.Articles != 2 || r.Extracted != 7 || r.Kept != 6 || mock.CallsFor("extract") != 2 {
		t.Fatalf("relatório=%+v chamadas=%d", r, mock.CallsFor("extract"))
	}
	// Segunda rodada não reprocessa.
	if r2, _ := p.ExtractPending(context.Background(), ExtractLimit); r2.Articles != 0 || mock.CallsFor("extract") != 2 {
		t.Fatalf("artigos já processados não podem ser reextraídos: %+v", r2)
	}
}

func TestExtractPendingLimitAndOrder(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	src, _ := st.UpsertSource(ctx, "Agência Fictícia", "rss", "https://exemplo.invalid/rss", "cc-by")
	for i := 0; i < 45; i++ {
		pub := testfix.Now.Add(-time.Duration(i) * time.Minute)
		_, _ = st.InsertArticle(ctx, store.Article{SourceID: src, URL: fmt.Sprintf("https://exemplo.invalid/n%d", i), Title: fmt.Sprintf("Notícia %d", i), TitleHash: fmt.Sprint(i), Summary: "Texto sem números.", PublishedAt: &pub})
	}
	mock := llm.NewMock(nil)
	var order []string
	mock.Handler = func(r llm.Request) (string, error) {
		order = append(order, r.Prompt)
		return `{"facts":[]}`, nil
	}
	p, _ := newPipeline(st, mock, testEnv(), testfix.Now)
	r, _ := p.ExtractPending(ctx, ExtractLimit)
	if r.Articles != 40 || len(order) != 40 {
		t.Fatalf("limite de 40 por ciclo: %+v", r)
	}
	if !strings.Contains(order[0], "Notícia 0\n") || !strings.Contains(order[39], "Notícia 39\n") {
		t.Fatal("mais recentes primeiro")
	}
}

func TestExtractPendingErrorsAreReported(t *testing.T) {
	st := testStore(t)
	fx := seed(t, st)
	_ = fx
	mock := llm.NewMock(nil)
	mock.Handler = func(r llm.Request) (string, error) { return "", llm.ErrNoAPIKey }
	p, _ := newPipeline(st, mock, testEnv(), testfix.Now)
	started := time.Now().Add(-time.Second)
	r, err := p.ExtractPending(context.Background(), ExtractLimit)
	if err != nil {
		t.Fatal(err)
	}
	if r.Failed != 1 || r.Stopped == "" {
		t.Fatalf("chave ausente deve parar o ciclo na 1ª falha: %+v", r)
	}
	md, _ := report.Build(context.Background(), st, report.Run{Command: "ingest", Started: started, Finished: time.Now()}, testfix.Loc(), started)
	if !strings.Contains(md, "extract_failed") || !strings.Contains(md, "ANTHROPIC_API_KEY ausente") {
		t.Fatalf("erro de LLM deve aparecer nos Avisos:\n%s", md)
	}
	var pending int
	_ = st.DB.QueryRow(context.Background(), `SELECT count(*) FROM articles WHERE facts_extracted_at IS NULL`).Scan(&pending)
	if pending != 2 {
		t.Fatal("artigo com falha deve continuar pendente para a próxima rodada")
	}
}

func TestExtractPendingBlackoutAndBudget(t *testing.T) {
	st := testStore(t)
	fx := seed(t, st)
	mock := recorded(t, fx)
	inside := time.Date(2026, 10, 25, 12, 0, 0, 0, testfix.Loc())
	p, _ := newPipeline(st, mock, testEnv(), inside)
	if r, _ := p.ExtractPending(context.Background(), ExtractLimit); r.Stopped == "" || len(mock.Calls) != 0 {
		t.Fatalf("bloqueio eleitoral pausa a extração: %+v", r)
	}
	env := testEnv()
	env.MaxDailyUSD = 0.001 // cada chamada custa US$ 0,002
	p2, _ := newPipeline(st, mock, env, testfix.Now)
	r, _ := p2.ExtractPending(context.Background(), ExtractLimit)
	if len(mock.Calls) != 1 || !strings.Contains(r.Stopped, "teto") {
		t.Fatalf("teto diário para a extração: %+v chamadas=%d", r, len(mock.Calls))
	}
}

func absf(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func TestBlackoutBlocksGeneration(t *testing.T) {
	st := testStore(t)
	fx := seed(t, st)
	mock := recorded(t, fx)
	inside := time.Date(2026, 10, 25, 12, 0, 0, 0, testfix.Loc())
	p, _ := newPipeline(st, mock, testEnv(), inside)
	_, err := p.Generate(context.Background(), "noticias")
	if !errors.Is(err, ErrBlackout) {
		t.Fatalf("err=%v, want ErrBlackout", err)
	}
	var n int
	_ = st.DB.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM segments) + (SELECT count(*) FROM rundowns)`).Scan(&n)
	if n != 0 || len(mock.Calls) != 0 {
		t.Fatalf("nada pode ser gerado no bloqueio: linhas=%d chamadas=%d", n, len(mock.Calls))
	}
	// Logo depois da janela, gera normalmente.
	after := time.Date(2026, 10, 27, 0, 0, 0, 0, testfix.Loc())
	if _, ok := mustBlackout(t).Active(after); ok {
		t.Fatal("o fim da janela é exclusivo")
	}
}

func mustBlackout(t *testing.T) config.Blackout {
	b, err := config.LoadBlackout(testfix.Path("config"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBlackoutWindows(t *testing.T) {
	b := mustBlackout(t)
	loc := testfix.Loc()
	cases := map[time.Time]bool{
		time.Date(2026, 10, 21, 23, 59, 59, 0, loc):    false,
		time.Date(2026, 10, 22, 0, 0, 0, 0, loc):       true,
		time.Date(2026, 10, 25, 18, 0, 0, 0, loc):      true,
		time.Date(2026, 10, 26, 23, 59, 59, 0, loc):    true,
		time.Date(2026, 10, 27, 0, 0, 0, 0, loc):       false,
		time.Date(2026, 10, 22, 2, 30, 0, 0, time.UTC): false, // 21/10 23:30 em Brasília
	}
	for at, want := range cases {
		if _, got := b.Active(at); got != want {
			t.Errorf("%s: ativo=%v, want %v", at, got, want)
		}
	}
}

func TestBudgetStopsGeneration(t *testing.T) {
	st := testStore(t)
	fx := seed(t, st)
	mock := recorded(t, fx) // cada chamada: 1000 in + 200 out = US$ 0,002
	env := testEnv()
	env.MaxDailyUSD = 0.003
	p, _ := newPipeline(st, mock, env, testfix.Now)
	ctx := context.Background()

	res, err := p.Generate(ctx, "noticias")
	if !errors.Is(err, llm.ErrBudget) {
		t.Fatalf("err=%v, want ErrBudget", err)
	}
	if len(mock.Calls) != 2 {
		t.Fatalf("o teto deveria parar na 3ª chamada; chamadas=%d", len(mock.Calls))
	}
	seg, _ := st.Segment(ctx, res.SegmentID)
	if seg.Status != "rejected" {
		t.Fatalf("segmento interrompido deveria ficar rejected: %s", seg.Status)
	}
	var warned int
	_ = st.DB.QueryRow(ctx, `SELECT count(*) FROM system_events WHERE kind='budget_exceeded'`).Scan(&warned)
	if warned != 1 {
		t.Fatalf("aviso de teto registrado %d vezes", warned)
	}
	// Próxima tentativa nem começa.
	before := len(mock.Calls)
	if _, err := p.Generate(ctx, "economia"); !errors.Is(err, llm.ErrBudget) {
		t.Fatalf("err=%v", err)
	}
	var segs int
	_ = st.DB.QueryRow(ctx, `SELECT count(*) FROM segments`).Scan(&segs)
	if segs != 1 || len(mock.Calls) != before {
		t.Fatalf("nada novo deveria ser criado: segmentos=%d chamadas=%d", segs, len(mock.Calls))
	}
	// No dia seguinte o orçamento zera.
	p2, _ := newPipeline(st, mock, env, testfix.Now.Add(24*time.Hour))
	if err := p2.Guard(ctx); err != nil {
		t.Fatalf("novo dia deveria liberar: %v", err)
	}
}

func TestGenerateOff(t *testing.T) {
	st := testStore(t)
	env := testEnv()
	env.Generate = false
	p, _ := newPipeline(st, llm.NewMock(nil), env, testfix.Now)
	if _, err := p.Generate(context.Background(), "noticias"); !errors.Is(err, ErrGenerateOff) {
		t.Fatalf("err=%v", err)
	}
}

func TestInvalidScriptTwiceRejects(t *testing.T) {
	st := testStore(t)
	fx := seed(t, st)
	mock := recorded(t, fx)
	inner := mock.Handler
	mock.Handler = func(r llm.Request) (string, error) {
		if r.Purpose == "script" {
			return `{"block": "noticias", "lines": [{"speaker": "orlando"}]}`, nil
		}
		return inner(r)
	}
	p, _ := newPipeline(st, mock, testEnv(), testfix.Now)
	res, err := p.Generate(context.Background(), "noticias")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "rejected" || mock.CallsFor("script") != 2 {
		t.Fatalf("status=%s chamadas de roteiro=%d", res.Status, mock.CallsFor("script"))
	}
	seg, _ := st.Segment(context.Background(), res.SegmentID)
	if seg.Attempts != 2 {
		t.Fatalf("attempts=%d", seg.Attempts)
	}
}

func TestSegmentRejectedAboveThreshold(t *testing.T) {
	st := testStore(t)
	fx := seed(t, st)
	mock := recorded(t, fx)
	inner := mock.Handler
	// O juiz reprova tudo que fala da ponte: 4 de 9 falas fact caem (44%).
	mock.Handler = func(r llm.Request) (string, error) {
		if r.Purpose == "judge" && strings.Contains(r.Prompt, "ponte") {
			return testfix.LLM(t, "judge_unsupported.json"), nil
		}
		if r.Purpose == "rewrite" && strings.Contains(r.Prompt, "ponte") {
			return `{"text": "A ponte é uma ponte.", "fact_ids": []}`, nil
		}
		return inner(r)
	}
	p, _ := newPipeline(st, mock, testEnv(), testfix.Now)
	res, err := p.Generate(context.Background(), "noticias")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "rejected" || !strings.Contains(res.Reason, "falas fact cortadas") {
		t.Fatalf("status=%s motivo=%s", res.Status, res.Reason)
	}
	var mems int
	_ = st.DB.QueryRow(context.Background(), `SELECT count(*) FROM persona_memory`).Scan(&mems)
	if mems != 0 {
		t.Fatal("segmento rejeitado não gera memória")
	}
}

func TestDraftThenCheck(t *testing.T) {
	st := testStore(t)
	fx := seed(t, st)
	p, _ := newPipeline(st, recorded(t, fx), testEnv(), testfix.Now)
	ctx := context.Background()
	d, err := p.Draft(ctx, "noticias")
	if err != nil || d.Status != "draft" {
		t.Fatalf("draft: %+v %v", d, err)
	}
	r, err := p.CheckDraft(ctx, d.SegmentID)
	if err != nil || r.Status != "approved" {
		t.Fatalf("check: %+v %v", r, err)
	}
	if _, err := p.CheckDraft(ctx, d.SegmentID); err == nil {
		t.Fatal("segmento já checado não pode ser checado de novo")
	}
}
