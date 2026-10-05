// Package ingest busca as fontes (RSS, Banco Central, Open-Meteo) e grava
// artigos e fatos de mercado/clima.
package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"

	"github.com/gloffreda/tv-ta-ligado/internal/config"
	"github.com/gloffreda/tv-ta-ligado/internal/facts"
	"github.com/gloffreda/tv-ta-ligado/internal/store"
	"github.com/gloffreda/tv-ta-ligado/internal/textutil"
)

type Ingester struct {
	Store *store.Store
	HTTP  *http.Client
	Feeds config.Feeds
	UA    string
	Loc   *time.Location
	Now   func() time.Time
	// MaxAge: itens de RSS mais velhos que isso são ignorados.
	MaxAge time.Duration
}

type Report struct {
	Articles     int
	Duplicates   int
	MarketFacts  int
	WeatherFacts int // capitais com previsão gravada (nova ou reconfirmada)
	Failures     []string
}

func (in *Ingester) now() time.Time {
	if in.Now != nil {
		return in.Now()
	}
	return time.Now()
}

// RunOnce faz uma rodada completa. Falha de uma fonte não derruba as outras.
func (in *Ingester) RunOnce(ctx context.Context) Report {
	var r Report
	for _, f := range in.Feeds.RSS {
		n, d, err := in.rss(ctx, f)
		r.Articles += n
		r.Duplicates += d
		if err != nil {
			in.fail(ctx, &r, f.Name, err)
		}
	}
	for _, s := range in.Feeds.BCB {
		if err := in.bcb(ctx, s); err != nil {
			in.fail(ctx, &r, fmt.Sprintf("BCB série %d", s.Code), err)
		} else {
			r.MarketFacts++
		}
	}
	if len(in.Feeds.Weather.Capitals) > 0 {
		n, err := in.weather(ctx)
		r.WeatherFacts = n
		if err != nil {
			in.fail(ctx, &r, in.Feeds.Weather.SourceName, err)
		}
	}
	return r
}

func (in *Ingester) fail(ctx context.Context, r *Report, source string, err error) {
	slog.Warn("fonte falhou", "fonte", source, "erro", err)
	r.Failures = append(r.Failures, source+": "+err.Error())
	_ = in.Store.Event(ctx, "source_failed", map[string]string{"source": source, "error": err.Error()})
}

func (in *Ingester) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", in.UA)
	resp, err := in.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

// ---- RSS ----

const summaryMax = 500

func (in *Ingester) rss(ctx context.Context, f config.RSSFeed) (inserted, dup int, err error) {
	srcID, err := in.Store.UpsertSource(ctx, f.Name, "rss", f.URL, f.License)
	if err != nil {
		return 0, 0, err
	}
	body, err := in.get(ctx, f.URL)
	if err != nil {
		return 0, 0, err
	}
	feed, err := gofeed.NewParser().ParseString(string(body))
	if err != nil {
		return 0, 0, fmt.Errorf("parse: %w", err)
	}
	cutoff := in.now().Add(-in.MaxAge)
	for _, it := range ParseItems(feed, f) {
		if it.PublishedAt != nil && it.PublishedAt.Before(cutoff) {
			continue
		}
		it.SourceID = srcID
		ok, err := in.Store.InsertArticle(ctx, it)
		if err != nil {
			return inserted, dup, err
		}
		if ok {
			inserted++
		} else {
			dup++
		}
	}
	return inserted, dup, nil
}

// ParseItems converte itens do feed em artigos. Só fontes CC BY guardam corpo.
func ParseItems(feed *gofeed.Feed, f config.RSSFeed) []store.Article {
	var out []store.Article
	for _, it := range feed.Items {
		title := strings.TrimSpace(textutil.StripHTML(it.Title))
		link := NormalizeURL(it.Link)
		if title == "" || link == "" {
			continue
		}
		full := textutil.StripHTML(firstNonEmpty(it.Content, it.Description))
		a := store.Article{URL: link, Title: title, TitleHash: textutil.TitleHash(title), Summary: truncate(firstParagraph(full), summaryMax)}
		if f.StoreBody && strings.EqualFold(f.License, "cc-by") && full != "" {
			a.Body = &full
		}
		if it.PublishedParsed != nil {
			t := *it.PublishedParsed
			a.PublishedAt = &t
		} else if it.UpdatedParsed != nil {
			t := *it.UpdatedParsed
			a.PublishedAt = &t
		}
		out = append(out, a)
	}
	return out
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func firstParagraph(s string) string {
	for _, p := range strings.Split(s, "\n\n") {
		if p = strings.TrimSpace(p); len(p) > 40 {
			return p
		}
	}
	return strings.TrimSpace(s)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	cut := string(r[:n])
	if i := strings.LastIndexAny(cut, ".!?"); i > n/2 {
		return cut[:i+1]
	}
	return cut + "…"
}

// ---- Banco Central (SGS) ----

type sgsPoint struct {
	Data  string `json:"data"`
	Valor string `json:"valor"`
}

const (
	sgsLast  = "https://api.bcb.gov.br/dados/serie/bcdata.sgs.%d/dados/ultimos/20?formato=json"
	sgsRange = "https://api.bcb.gov.br/dados/serie/bcdata.sgs.%d/dados?formato=json&dataInicial=%s&dataFinal=%s"
)

// SGSPointURL é o link clicável da fonte: termina no dado citado. A API
// rejeita intervalo de um dia só, então começa no dia anterior.
func SGSPointURL(code int, d time.Time) string {
	return fmt.Sprintf(sgsRange, code, d.AddDate(0, 0, -1).Format("02/01/2006"), d.Format("02/01/2006"))
}

// Política do BCB: timeout de 20 s por tentativa, 3 tentativas com backoff.
var (
	BCBTimeout  = 20 * time.Second
	BCBAttempts = 3
	BCBBackoff  = []time.Duration{2 * time.Second, 4 * time.Second}
)

// getRetry tenta a lista de URLs (uma por tentativa; a última se repete) com
// timeout e backoff. Devolve o corpo da primeira que responder.
func (in *Ingester) getRetry(ctx context.Context, urls []string) ([]byte, error) {
	var lastErr error
	for i := 0; i < BCBAttempts; i++ {
		if i > 0 {
			wait := BCBBackoff[min(i-1, len(BCBBackoff)-1)]
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
		}
		u := urls[min(i, len(urls)-1)]
		cctx, cancel := context.WithTimeout(ctx, BCBTimeout)
		body, err := in.get(cctx, u)
		cancel()
		if err == nil {
			return body, nil
		}
		lastErr = fmt.Errorf("tentativa %d/%d: %w", i+1, BCBAttempts, err)
		slog.Warn("BCB falhou", "url", u, "erro", lastErr)
	}
	return nil, lastErr
}

func (in *Ingester) bcb(ctx context.Context, s config.BCBSeries) error {
	if _, err := in.Store.UpsertSource(ctx, "Banco Central (SGS)", "bcb", "https://api.bcb.gov.br/dados/serie/bcdata.sgs.{codigo}/dados", "public"); err != nil {
		return err
	}
	// "ultimos/N" aceita no máximo 20 pontos, e a série 432 publica mais de
	// 20 datas futuras; por isso tenta antes a consulta por intervalo até hoje.
	today := in.now().In(in.Loc)
	rangeURL := fmt.Sprintf(sgsRange, s.Code, today.AddDate(0, 0, -120).Format("02/01/2006"), today.Format("02/01/2006"))
	body, err := in.getRetry(ctx, []string{rangeURL, rangeURL, fmt.Sprintf(sgsLast, s.Code)})
	if err != nil {
		return in.keepLast(ctx, s, err)
	}
	var pts []sgsPoint
	if err := json.Unmarshal(body, &pts); err != nil {
		return in.keepLast(ctx, s, fmt.Errorf("json: %w", err))
	}
	f, err := MarketFact(s, pts, in.now(), in.Loc)
	if err != nil {
		return in.keepLast(ctx, s, err)
	}
	_, err = in.Store.UpsertFact(ctx, f)
	return err
}

// keepLast: a série falhou; o último valor válido (dentro da validade) segue
// valendo, com aviso. Sem valor válido, a falha é devolvida.
func (in *Ingester) keepLast(ctx context.Context, s config.BCBSeries, cause error) error {
	series := fmt.Sprintf("bcb:%d", s.Code)
	market, err := in.Store.LatestFactsByKind(ctx, facts.Market, in.now())
	if err != nil {
		return cause
	}
	for _, f := range market {
		if f.Series == series {
			_ = in.Store.Event(ctx, "bcb_stale", map[string]any{
				"series": series, "kept_fact_id": f.ID, "as_of": f.AsOf.In(in.Loc).Format("02/01/2006"),
				"expires_at": f.ExpiresAt.In(in.Loc).Format("02/01/2006 15:04"), "error": cause.Error(),
				"note": "mantido o último valor válido",
			})
			slog.Warn("BCB indisponível; mantido o último valor válido", "serie", series, "as_of", f.AsOf.In(in.Loc).Format("02/01/2006"))
			return nil
		}
	}
	return fmt.Errorf("%w (sem valor válido anterior)", cause)
}

// MarketFact monta o fato a partir da resposta da API SGS.
func MarketFact(s config.BCBSeries, pts []sgsPoint, now time.Time, loc *time.Location) (facts.Fact, error) {
	today := now.In(loc)
	var best *sgsPoint
	var bestDate time.Time
	for i := range pts {
		d, err := time.ParseInLocation("02/01/2006", pts[i].Data, loc)
		if err != nil || d.After(today) {
			continue
		}
		if best == nil || d.After(bestDate) {
			best, bestDate = &pts[i], d
		}
	}
	if best == nil {
		return facts.Fact{}, fmt.Errorf("série %d sem dado até hoje", s.Code)
	}
	var v float64
	if _, err := fmt.Sscanf(best.Valor, "%g", &v); err != nil {
		return facts.Fact{}, fmt.Errorf("valor %q: %w", best.Valor, err)
	}
	// Confirma o código pela faixa plausível do indicador.
	if v < s.Min || v > s.Max {
		return facts.Fact{}, fmt.Errorf("série %d: valor %v fora da faixa [%v,%v] de %s — código errado?", s.Code, v, s.Min, s.Max, s.Label)
	}
	claim := strings.NewReplacer(
		"{valor}", facts.FormatBR(v, facts.Decimals(best.Valor)),
		"{data}", bestDate.Format("02/01/2006"),
		"{mes_ano}", facts.MonthYear(bestDate),
	).Replace(s.Claim)
	return facts.Fact{
		Kind: facts.Market, Claim: claim, Entities: orgs(s.Entities), Value: &v, Unit: s.Unit,
		AsOf: bestDate, SourceName: "Banco Central (SGS)", SourceURL: SGSPointURL(s.Code, bestDate),
		Series: fmt.Sprintf("bcb:%d", s.Code), ExpiresAt: now.Add(facts.TTL(facts.Market)),
	}, nil
}

// ---- Clima (Open-Meteo) ----

type omDaily struct {
	Daily struct {
		Time   []string   `json:"time"`
		Max    []*float64 `json:"temperature_2m_max"`
		Min    []*float64 `json:"temperature_2m_min"`
		Precip []*float64 `json:"precipitation_probability_max"`
	} `json:"daily"`
}

func cityURL(base string, c config.Capital) string {
	return fmt.Sprintf("%s?latitude=%.4f&longitude=%.4f&daily=temperature_2m_max,temperature_2m_min,precipitation_probability_max&timezone=America%%2FSao_Paulo&forecast_days=1", base, c.Lat, c.Lon)
}

func (in *Ingester) weather(ctx context.Context) (int, error) {
	w := in.Feeds.Weather
	if _, err := in.Store.UpsertSource(ctx, w.SourceName, "weather", w.URL, "cc-by"); err != nil {
		return 0, err
	}
	lats, lons := make([]string, len(w.Capitals)), make([]string, len(w.Capitals))
	for i, c := range w.Capitals {
		lats[i], lons[i] = fmt.Sprintf("%.4f", c.Lat), fmt.Sprintf("%.4f", c.Lon)
	}
	url := fmt.Sprintf("%s?latitude=%s&longitude=%s&daily=temperature_2m_max,temperature_2m_min,precipitation_probability_max&timezone=America%%2FSao_Paulo&forecast_days=1",
		w.URL, strings.Join(lats, ","), strings.Join(lons, ","))
	body, err := in.get(ctx, url)
	if err != nil {
		return 0, err
	}
	var res []omDaily
	if err := json.Unmarshal(body, &res); err != nil {
		return 0, fmt.Errorf("json: %w", err)
	}
	if len(res) != len(w.Capitals) {
		return 0, fmt.Errorf("esperava %d capitais, vieram %d", len(w.Capitals), len(res))
	}
	n := 0
	for i, c := range w.Capitals {
		f, err := WeatherFact(c, res[i], w, in.now(), in.Loc)
		if err != nil {
			slog.Warn("clima sem dado", "cidade", c.City, "erro", err)
			_ = in.Store.Event(ctx, "source_failed", map[string]string{"source": w.SourceName + " — " + c.City, "error": err.Error()})
			continue
		}
		if _, err := in.Store.UpsertFact(ctx, f); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func WeatherFact(c config.Capital, d omDaily, w config.Weather, now time.Time, loc *time.Location) (facts.Fact, error) {
	if len(d.Daily.Time) == 0 || d.Daily.Max[0] == nil || d.Daily.Min[0] == nil || d.Daily.Precip[0] == nil {
		return facts.Fact{}, fmt.Errorf("previsão incompleta")
	}
	day, err := time.ParseInLocation("2006-01-02", d.Daily.Time[0], loc)
	if err != nil {
		return facts.Fact{}, err
	}
	mx, mn, pr := *d.Daily.Max[0], *d.Daily.Min[0], *d.Daily.Precip[0]
	claim := fmt.Sprintf("Previsão para %s em %s (%s): máxima de %s °C, mínima de %s °C e %s%% de chance de chuva, segundo o %s.",
		day.Format("02/01/2006"), c.City, c.UF, fmtTemp(mx), fmtTemp(mn), facts.FormatBR(pr, 0), w.SourceName)
	return facts.Fact{
		Kind: facts.Weather, Claim: claim, Entities: []facts.Entity{{Name: c.City, Type: facts.Place}, {Name: c.UF, Type: facts.Place}, {Name: w.SourceName, Type: facts.Org}}, Value: &mx, Unit: "°C",
		AsOf: day, SourceName: w.SourceName, SourceURL: cityURL(w.URL, c),
		Series: "weather:" + c.City, ExpiresAt: now.Add(facts.TTL(facts.Weather)),
	}, nil
}

// orgs: as entidades das séries do BCB são instituições e índices.
func orgs(names []string) []facts.Entity {
	out := make([]facts.Entity, len(names))
	for i, n := range names {
		out[i] = facts.Entity{Name: n, Type: facts.Org}
	}
	return out
}

func fmtTemp(v float64) string {
	if v == float64(int(v)) {
		return facts.FormatBR(v, 0)
	}
	return facts.FormatBR(v, 1)
}
