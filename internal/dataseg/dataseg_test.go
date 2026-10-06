package dataseg

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/gloffreda/tv-ta-ligado/internal/check"
	"github.com/gloffreda/tv-ta-ligado/internal/config"
	"github.com/gloffreda/tv-ta-ligado/internal/facts"
	"github.com/gloffreda/tv-ta-ligado/internal/ingest"
	"github.com/gloffreda/tv-ta-ligado/internal/store"
	"github.com/gloffreda/tv-ta-ligado/internal/testfix"
)

var loc = time.FixedZone("BRT", -3*3600)
var now = time.Date(2026, 10, 6, 19, 0, 0, 0, loc)

func lexicon(t *testing.T) *check.Lexicon {
	allow, err := config.LoadAllowlist(testfix.Path("config"))
	if err != nil {
		t.Fatal(err)
	}
	names, err := config.LoadFirstNames(testfix.Path("config"))
	if err != nil {
		t.Fatal(err)
	}
	return check.NewLexicon(allow, names)
}

func seed(t *testing.T, st *store.Store) {
	ctx := context.Background()
	w := config.Weather{SourceName: "Open-Meteo", URL: "https://api.open-meteo.com/v1/forecast"}
	caps := []config.Capital{{City: "São Paulo", UF: "SP"}, {City: "Manaus", UF: "AM"}, {City: "Recife", UF: "PE"}, {City: "Brasília", UF: "DF"}, {City: "Porto Alegre", UF: "RS"}}
	for i, c := range caps {
		mx, mn, p := 30.5+float64(i), 18.0+float64(i), float64(10*i+20)
		claim := fmt.Sprintf("Previsão para 06/10/2026 em %s (%s): máxima de %s °C, mínima de %s °C e %s%% de chance de chuva, segundo o Open-Meteo.",
			c.City, c.UF, facts.FormatBR(mx, 1), facts.FormatBR(mn, 0), facts.FormatBR(p, 0))
		f := facts.Fact{Kind: facts.Weather, Claim: claim, Entities: []facts.Entity{{Name: c.City, Type: facts.Place}, {Name: c.UF, Type: facts.Place}, {Name: w.SourceName, Type: facts.Org}},
			Value: &mx, Unit: "°C", AsOf: now, SourceName: w.SourceName, SourceURL: w.URL + "?" + c.UF, Series: "weather:" + c.City, ExpiresAt: now.Add(12 * time.Hour)}
		if _, err := st.UpsertFact(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	al := ingest.AlertFact("9", "Tempestade", "Perigo", []string{"Bahia"}, []string{"Nordeste"}, now.Add(-time.Hour), now.Add(5*time.Hour),
		config.Alerts{SourceName: "INMET", URL: "https://apiprevmet3.inmet.gov.br/avisos/ativos"}, now, loc)
	if _, err := st.UpsertFact(ctx, al); err != nil {
		t.Fatal(err)
	}
	v := 5.48
	if _, err := st.UpsertFact(ctx, facts.Fact{Kind: facts.Market, Claim: "O dólar comercial (venda) ficou em R$ 5,48 em 05/10/2026, segundo o Banco Central.",
		Entities: []facts.Entity{{Name: "Banco Central", Type: facts.Org}}, Value: &v, Unit: "BRL", AsOf: now, SourceName: "Banco Central (SGS)", SourceURL: "https://bcb.invalid/1", Series: "bcb:1", ExpiresAt: now.Add(24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	src, err := st.UpsertSource(ctx, "g1", "rss", "https://g1.invalid/rss", "none")
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range []string{
		"A prefeitura de Vila Serena abriu 120 vagas em cursos gratuitos de programação.",
		"O metrô de São Paulo vai funcionar até mais tarde no sábado (10).",
		"Um incêndio deixou 4 feridos em um prédio residencial de Pedra Alta.",
	} {
		url := fmt.Sprintf("https://g1.invalid/%d", i)
		if _, err := st.InsertArticle(ctx, store.Article{SourceID: src, URL: url, Title: c, TitleHash: url, PublishedAt: &now}); err != nil {
			t.Fatal(err)
		}
		var aid int64
		if err := st.DB.QueryRow(ctx, `SELECT id FROM articles WHERE url=$1`, url).Scan(&aid); err != nil {
			t.Fatal(err)
		}
		ents := []facts.Entity{{Name: "Vila Serena", Type: facts.Place}, {Name: "São Paulo", Type: facts.Place}, {Name: "Pedra Alta", Type: facts.Place}}
		f := facts.Fact{ArticleID: &aid, Kind: facts.Headline, Claim: c, Entities: ents[i : i+1], AsOf: now, SourceName: "g1", SourceURL: url, ExpiresAt: now.Add(48 * time.Hour), Sensitive: i == 2}
		if _, err := st.DB.Exec(ctx, `SELECT 1`); err != nil {
			t.Fatal(err)
		}
		if _, err := st.UpsertFact(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	// a criação dos fatos usa now() do banco; trazemos para o relógio do teste
	if _, err := st.DB.Exec(ctx, `UPDATE facts SET created_at=$1`, now.Add(-10*time.Minute)); err != nil {
		t.Fatal(err)
	}
}

func TestTemplatesHaveEightVariants(t *testing.T) {
	lists := map[string][]string{"weatherOpen": weatherOpen, "weatherCity": weatherCity, "alertIntro": alertIntro, "weatherClose": weatherClose,
		"marketOpen": marketOpen, "marketLead": marketLead, "headlineLead": headlineLead,
		"headlinesOpen.orlando": headlinesOpen["orlando"], "headlinesOpen.duda": headlinesOpen["duda"],
		"headlinesClose.orlando": headlinesClose["orlando"], "headlinesClose.duda": headlinesClose["duda"]}
	for k, l := range lists {
		if len(l) < 8 {
			t.Errorf("%s: %d variações (mínimo 8)", k, len(l))
		}
	}
}

func TestBuildDataSegments(t *testing.T) {
	st := testfix.DB(t, "dataseg")
	seed(t, st)
	ctx := context.Background()
	b := &Builder{Store: st, Lex: lexicon(t), Loc: loc, Now: func() time.Time { return now }, Rand: rand.New(rand.NewPCG(1, 2))}
	for _, block := range []string{Weather, Market, Headlines} {
		for round := 0; round < 4; round++ { // várias sementes: todas as variações passam no checador
			b.Rand = rand.New(rand.NewPCG(uint64(round), 9))
			res, err := b.Build(ctx, block)
			if block == Headlines && round > 0 {
				if err != ErrNoData {
					t.Fatalf("manchetes repetidas antes do intervalo: %v %+v", err, res)
				}
				continue
			}
			if err != nil {
				t.Fatalf("%s: %v", block, err)
			}
			if len(res.Dropped) > 0 {
				t.Fatalf("%s: falas reprovadas no checador: %v", block, res.Dropped)
			}
			lines, err := st.LinesOf(ctx, res.SegmentID)
			if err != nil {
				t.Fatal(err)
			}
			var all []string
			for _, l := range lines {
				all = append(all, l.Speaker+": "+l.Text)
			}
			text := strings.Join(all, "\n")
			switch block {
			case Weather:
				if !strings.HasPrefix(text, "gloria: Boa noite") || !strings.Contains(text, "O INMET emitiu aviso de tempestade") || !strings.Contains(text, "No Sudeste") {
					t.Fatalf("tempo:\n%s", text)
				}
			case Market:
				if !strings.HasSuffix(text, "orlando: Isso não é recomendação de investimento.") || !strings.Contains(text, "R$ 5,48") {
					t.Fatalf("mercado:\n%s", text)
				}
			case Headlines:
				if !strings.Contains(text, "g1") || !strings.Contains(text, "orlando:") || !strings.Contains(text, "duda:") {
					t.Fatalf("manchetes alternadas e com crédito:\n%s", text)
				}
				if strings.Contains(text, "Tá ligado?") {
					t.Fatalf("notícia sensível: fecho neutro, sem bordão:\n%s", text)
				}
			}
		}
	}
	var origin string
	var sens bool
	if err := st.DB.QueryRow(ctx, `SELECT origin, sensitive FROM segments WHERE block='manchetes'`).Scan(&origin, &sens); err != nil {
		t.Fatal(err)
	}
	if origin != "data" || !sens {
		t.Fatalf("origin=%s sensitive=%v", origin, sens)
	}
}

func TestEveryWeatherVariantPassesChecker(t *testing.T) {
	f := facts.Fact{ID: 1, Kind: facts.Weather, Claim: "Previsão para 06/10/2026 em São Paulo (SP): máxima de 30,5 °C, mínima de 18 °C e 40% de chance de chuva, segundo o Open-Meteo.",
		Entities: []facts.Entity{{Name: "São Paulo", Type: facts.Place}, {Name: "SP", Type: facts.Place}, {Name: "Open-Meteo", Type: facts.Org}}, AsOf: now, ExpiresAt: now.Add(time.Hour)}
	env := &check.Env{Facts: map[int64]facts.Fact{1: f}, SegmentFacts: []facts.Fact{f}, KnownEntities: f.Entities, Lex: lexicon(t), Now: now, Loc: loc}
	for _, tmpl := range weatherCity {
		for _, r := range regionLead {
			text := fill(tmpl, "{r}", r, "{c}", "São Paulo", "{max}", "30,5", "{min}", "18", "{p}", "40")
			if res := check.Deterministic(check.Line{Speaker: "gloria", Type: check.TypeFact, Text: text, FactIDs: []int64{1}}, env); !res.Passed {
				t.Errorf("%q: %v", text, res.Reasons)
			}
		}
	}
	for _, list := range [][]string{weatherOpen, alertIntro, weatherClose, marketOpen, headlinesOpen["orlando"], headlinesOpen["duda"], headlinesClose["orlando"], headlinesClose["duda"], {weatherCloseRain, marketClose}} {
		for _, tmpl := range list {
			text := fill(tmpl, "{s}", "Boa noite")
			if res := check.Deterministic(check.Line{Speaker: "gloria", Type: check.TypeBanter, Text: text}, env); !res.Passed {
				t.Errorf("%q: %v", text, res.Reasons)
			}
		}
	}
}
