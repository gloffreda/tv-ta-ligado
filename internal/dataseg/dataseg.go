// Package dataseg monta segmentos de dados (tempo, mercado, manchetes) com
// modelos determinísticos, sem LLM. Cada fala passa pelo estágio
// determinístico do checador antes de ir ao ar; fala reprovada sai.
package dataseg

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gloffreda/tv-ta-ligado/internal/check"
	"github.com/gloffreda/tv-ta-ligado/internal/facts"
	"github.com/gloffreda/tv-ta-ligado/internal/store"
)

// Blocos de dados (nome do bloco no segmento).
const (
	Weather   = "tempo"
	Market    = "mercado"
	Headlines = "manchetes"
)

var ErrNoData = errors.New("sem dados válidos para o segmento")

type Builder struct {
	Store *store.Store
	Lex   *check.Lexicon
	Loc   *time.Location
	Now   func() time.Time
	Rand  *rand.Rand
	// Manchetes: janela das matérias e quanto tempo até repetir uma matéria.
	HeadlineWindow time.Duration
	HeadlineReuse  time.Duration
	HeadlineCount  int
}

func (b *Builder) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

func (b *Builder) rng() *rand.Rand {
	if b.Rand == nil {
		b.Rand = rand.New(rand.NewPCG(uint64(b.now().UnixNano()), 7))
	}
	return b.Rand
}

func (b *Builder) pick(xs []string) string { return xs[b.rng().IntN(len(xs))] }

// Greeting: "Bom dia" (5h–11h59), "Boa tarde" (12h–17h59), "Boa noite".
func Greeting(t time.Time) string {
	switch h := t.Hour(); {
	case h >= 5 && h < 12:
		return "Bom dia"
	case h >= 12 && h < 18:
		return "Boa tarde"
	}
	return "Boa noite"
}

func fill(tmpl string, kv ...string) string {
	r := strings.NewReplacer(kv...)
	return r.Replace(tmpl)
}

type Result struct {
	SegmentID int64
	Block     string
	Lines     int
	Dropped   []string
}

// Build monta o segmento do bloco de dados pedido.
func (b *Builder) Build(ctx context.Context, block string) (Result, error) {
	now := b.now()
	var lines []check.Line
	var fs []facts.Fact
	var err error
	switch block {
	case Weather:
		lines, fs, err = b.weather(ctx, now)
	case Market:
		lines, fs, err = b.market(ctx, now)
	case Headlines:
		lines, fs, err = b.headlines(ctx, now)
	default:
		return Result{}, fmt.Errorf("bloco de dados desconhecido: %q", block)
	}
	if err != nil {
		return Result{Block: block}, err
	}
	return b.save(ctx, block, lines, fs, now)
}

// save checa cada fala (estágio determinístico) e grava o segmento aprovado.
func (b *Builder) save(ctx context.Context, block string, lines []check.Line, fs []facts.Fact, now time.Time) (Result, error) {
	res := Result{Block: block}
	known, err := b.Store.AllEntities(ctx)
	if err != nil {
		return res, err
	}
	fm := map[int64]facts.Fact{}
	for _, f := range fs {
		fm[f.ID] = f
	}
	env := &check.Env{Facts: fm, SegmentFacts: fs, KnownEntities: known, Lex: b.Lex, Now: now, Loc: b.Loc, Sensitive: facts.AnySensitive(fs)}
	type checked struct {
		l   check.Line
		res check.StageResult
	}
	var ok []checked
	nFacts := 0
	for _, l := range lines {
		r := check.Deterministic(l, env)
		if !r.Passed {
			res.Dropped = append(res.Dropped, l.Text+" — "+strings.Join(r.Reasons, "; "))
			slog.Warn("dados: fala reprovada", "bloco", block, "fala", l.Text, "motivos", r.Reasons)
			continue
		}
		if l.Type == check.TypeFact {
			nFacts++
		}
		ok = append(ok, checked{l, r})
	}
	if nFacts == 0 {
		_ = b.Store.Event(ctx, "data_segment_failed", map[string]any{"block": block, "dropped": res.Dropped})
		return res, ErrNoData
	}
	segID, err := b.Store.CreateDataSegment(ctx, block, now)
	if err != nil {
		return res, err
	}
	res.SegmentID = segID
	if env.Sensitive {
		_ = b.Store.SetSegmentSensitive(ctx, segID, true)
	}
	for i, c := range ok {
		id, err := b.Store.InsertLine(ctx, store.Line{SegmentID: segID, Seq: i + 1, Speaker: c.l.Speaker, Type: c.l.Type, Text: c.l.Text, FactIDs: c.l.FactIDs})
		if err != nil {
			return res, err
		}
		_ = b.Store.LogCheck(ctx, id, 1, c.res.Stage, true, map[string]any{"text": c.l.Text, "fact_ids": c.l.FactIDs, "origin": "data"})
	}
	res.Lines = len(ok)
	if len(res.Dropped) > 0 {
		_ = b.Store.Event(ctx, "data_lines_dropped", map[string]any{"segment_id": segID, "block": block, "dropped": res.Dropped})
	}
	return res, b.Store.FinishSegment(ctx, segID, "approved", "")
}

// ---- tempo (Glória) ----

var ufRegion = map[string]string{
	"AC": "Norte", "AP": "Norte", "AM": "Norte", "PA": "Norte", "RO": "Norte", "RR": "Norte", "TO": "Norte",
	"AL": "Nordeste", "BA": "Nordeste", "CE": "Nordeste", "MA": "Nordeste", "PB": "Nordeste", "PE": "Nordeste", "PI": "Nordeste", "RN": "Nordeste", "SE": "Nordeste",
	"DF": "Centro-Oeste", "GO": "Centro-Oeste", "MT": "Centro-Oeste", "MS": "Centro-Oeste",
	"ES": "Sudeste", "MG": "Sudeste", "RJ": "Sudeste", "SP": "Sudeste",
	"PR": "Sul", "RS": "Sul", "SC": "Sul",
}

var regionOrder = []string{"Norte", "Nordeste", "Centro-Oeste", "Sudeste", "Sul"}

var reWeather = regexp.MustCompile(`máxima de (-?[\d,]+) °C, mínima de (-?[\d,]+) °C e (\d+)% de chance de chuva`)

// weatherNumbers lê máxima, mínima e chance de chuva do texto do fato (literal).
func weatherNumbers(f facts.Fact) (mx, mn, p string, ok bool) {
	m := reWeather.FindStringSubmatch(f.Claim)
	if m == nil {
		return "", "", "", false
	}
	return m[1], m[2], m[3], true
}

func cityUF(f facts.Fact) (city, uf string) {
	for _, e := range f.Entities {
		if e.Type != facts.Place {
			continue
		}
		if city == "" {
			city = e.Name
		} else if uf == "" {
			uf = e.Name
		}
	}
	return
}

func (b *Builder) weather(ctx context.Context, now time.Time) ([]check.Line, []facts.Fact, error) {
	ws, err := b.Store.LatestFactsByKind(ctx, facts.Weather, now)
	if err != nil {
		return nil, nil, err
	}
	alerts, err := b.Store.LatestFactsByKind(ctx, facts.Alert, now)
	if err != nil {
		return nil, nil, err
	}
	byRegion := map[string][]facts.Fact{}
	for _, f := range ws {
		if _, _, _, ok := weatherNumbers(f); !ok {
			continue
		}
		_, uf := cityUF(f)
		if r, ok := ufRegion[uf]; ok {
			byRegion[r] = append(byRegion[r], f)
		}
	}
	g := Greeting(now.In(b.Loc))
	lines := []check.Line{{Speaker: "gloria", Type: check.TypeBanter, Text: fill(b.pick(weatherOpen), "{s}", g)}}
	var used []facts.Fact
	rain := false
	for _, r := range regionOrder {
		cs := byRegion[r]
		if len(cs) == 0 {
			continue
		}
		sort.Slice(cs, func(i, j int) bool { return cs[i].ID < cs[j].ID })
		f := cs[b.rng().IntN(len(cs))]
		mx, mn, p, _ := weatherNumbers(f)
		city, _ := cityUF(f)
		var pn int
		fmt.Sscanf(p, "%d", &pn)
		rain = rain || pn >= 50
		lines = append(lines, check.Line{Speaker: "gloria", Type: check.TypeFact, FactIDs: []int64{f.ID},
			Text: fill(b.pick(weatherCity), "{r}", regionLead[r], "{c}", city, "{max}", mx, "{min}", mn, "{p}", p)})
		used = append(used, f)
	}
	if len(used) == 0 {
		return nil, nil, ErrNoData
	}
	sort.Slice(alerts, func(i, j int) bool { return alerts[i].ID < alerts[j].ID })
	for i, a := range alerts {
		if i >= 2 {
			break
		}
		lines = append(lines,
			check.Line{Speaker: "gloria", Type: check.TypeBanter, Text: b.pick(alertIntro)},
			check.Line{Speaker: "gloria", Type: check.TypeFact, FactIDs: []int64{a.ID}, Text: a.Claim})
		used = append(used, a)
	}
	end := b.pick(weatherClose)
	if rain {
		end = weatherCloseRain
	}
	lines = append(lines, check.Line{Speaker: "gloria", Type: check.TypeBanter, Text: end})
	return lines, used, nil
}

// ---- mercado (Orlando) ----

func (b *Builder) market(ctx context.Context, now time.Time) ([]check.Line, []facts.Fact, error) {
	ms, err := b.Store.LatestFactsByKind(ctx, facts.Market, now)
	if err != nil {
		return nil, nil, err
	}
	if len(ms) == 0 {
		return nil, nil, ErrNoData
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].Series < ms[j].Series })
	g := Greeting(now.In(b.Loc))
	lines := []check.Line{{Speaker: "orlando", Type: check.TypeBanter, Text: fill(b.pick(marketOpen), "{s}", g)}}
	leads := append([]string{}, marketLead...)
	b.rng().Shuffle(len(leads), func(i, j int) { leads[i], leads[j] = leads[j], leads[i] })
	for i, f := range ms {
		lines = append(lines, check.Line{Speaker: "orlando", Type: check.TypeFact, FactIDs: []int64{f.ID}, Text: leads[i%len(leads)] + f.Claim})
	}
	lines = append(lines, check.Line{Speaker: "orlando", Type: check.TypeBanter, Text: marketClose})
	return lines, ms, nil
}

// ---- manchetes (Orlando e Duda alternando) ----

func outlet(name string) (upper, lower string) {
	art, ok := outletArticle[name]
	if !ok {
		return name, name // sem artigo conhecido: "Fonte X informa"
	}
	return strings.ToUpper(art) + " " + name, art + " " + name
}

func (b *Builder) headlines(ctx context.Context, now time.Time) ([]check.Line, []facts.Fact, error) {
	n := b.HeadlineCount
	if n <= 0 {
		n = 4
	}
	win, reuse := b.HeadlineWindow, b.HeadlineReuse
	if win <= 0 {
		win = 6 * time.Hour
	}
	if reuse <= 0 {
		reuse = 3 * time.Hour
	}
	hs, err := b.Store.RecentHeadlineFacts(ctx, now, win, reuse, n)
	if err != nil {
		return nil, nil, err
	}
	if len(hs) == 0 {
		return nil, nil, ErrNoData
	}
	speakers := []string{"orlando", "duda"}
	if b.rng().IntN(2) == 1 {
		speakers[0], speakers[1] = speakers[1], speakers[0]
	}
	g := Greeting(now.In(b.Loc))
	lead := speakers[0]
	lines := []check.Line{{Speaker: lead, Type: check.TypeBanter, Text: fill(b.pick(headlinesOpen[lead]), "{s}", g)}}
	for i, f := range hs {
		up, low := outlet(f.SourceName)
		tmpl := b.pick(headlineLead)
		if _, ok := outletArticle[f.SourceName]; !ok {
			tmpl = "{V} informa: "
		}
		text := fill(tmpl, "{V}", up, "{v}", low) + f.Claim
		lines = append(lines, check.Line{Speaker: speakers[i%2], Type: check.TypeFact, FactIDs: []int64{f.ID}, Text: text})
	}
	last := speakers[len(hs)%2]
	closing := b.pick(headlinesClose[last])
	if facts.AnySensitive(hs) {
		closing = headlinesClose["orlando"][0] // modo sério: fecho neutro
		last = "orlando"
	}
	lines = append(lines, check.Line{Speaker: last, Type: check.TypeBanter, Text: closing})
	return lines, hs, nil
}
