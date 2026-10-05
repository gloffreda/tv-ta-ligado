package ingest

import (
	"os"
	"strings"
	"testing"

	"github.com/mmcdole/gofeed"

	"github.com/gloffreda/tv-ta-ligado/internal/config"
	"github.com/gloffreda/tv-ta-ligado/internal/testfix"
)

func TestMarketFactPicksLatestUpToToday(t *testing.T) {
	s := config.BCBSeries{Code: 432, Label: "Selic", Min: 1, Max: 30, Entities: []string{"Banco Central"},
		Claim: "A meta da taxa Selic está em {valor}% ao ano (dado de {data})."}
	// A série 432 devolve datas futuras: elas precisam ser ignoradas.
	pts := []sgsPoint{{"03/10/2026", "13.75"}, {"05/10/2026", "13.50"}, {"02/11/2026", "13.25"}}
	f, err := MarketFact(s, pts, testfix.Now, testfix.Loc())
	if err != nil {
		t.Fatal(err)
	}
	if *f.Value != 13.5 || !strings.Contains(f.Claim, "13,50%") || !strings.Contains(f.Claim, "05/10/2026") {
		t.Fatalf("fato errado: %+v", f)
	}
	if f.SourceURL != "https://api.bcb.gov.br/dados/serie/bcdata.sgs.432/dados?formato=json&dataInicial=04/10/2026&dataFinal=05/10/2026" {
		t.Fatalf("link da fonte deve apontar para o dado citado: %s", f.SourceURL)
	}
	if !f.ExpiresAt.Equal(testfix.Now.Add(24 * 3600e9)) {
		t.Fatalf("validade de mercado deveria ser 24h: %s", f.ExpiresAt)
	}
}

func TestMarketFactRejectsWrongSeries(t *testing.T) {
	s := config.BCBSeries{Code: 1, Label: "Dólar", Min: 2, Max: 15, Claim: "{valor}"}
	if _, err := MarketFact(s, []sgsPoint{{"04/10/2026", "5.20"}, {"05/10/2026", "0.07"}}, testfix.Now, testfix.Loc()); err == nil {
		t.Fatal("valor fora da faixa deveria indicar código de série errado")
	}
}

func TestWeatherFact(t *testing.T) {
	var d omDaily
	mx, mn, pr := 27.4, 18.0, 40.0
	d.Daily.Time = []string{"2026-10-05"}
	d.Daily.Max, d.Daily.Min, d.Daily.Precip = []*float64{&mx}, []*float64{&mn}, []*float64{&pr}
	f, err := WeatherFact(config.Capital{City: "Vila Serena", UF: "VS"}, d, config.Weather{SourceName: "Open-Meteo", URL: "https://api.open-meteo.com/v1/forecast"}, testfix.Now, testfix.Loc())
	if err != nil {
		t.Fatal(err)
	}
	want := "Previsão para 05/10/2026 em Vila Serena (VS): máxima de 27,4 °C, mínima de 18 °C e 40% de chance de chuva, segundo o Open-Meteo."
	if f.Claim != want {
		t.Fatalf("claim=%q", f.Claim)
	}
}

func TestParseItemsLicense(t *testing.T) {
	b, err := os.ReadFile(testfix.Path("testdata", "rss_sample.xml"))
	if err != nil {
		t.Fatal(err)
	}
	feed, err := gofeed.NewParser().ParseString(string(b))
	if err != nil {
		t.Fatal(err)
	}
	cc := ParseItems(feed, config.RSSFeed{License: "cc-by", StoreBody: true})
	if len(cc) != 2 {
		t.Fatalf("itens sem título devem ser ignorados: %d", len(cc))
	}
	if cc[0].Body == nil || !strings.Contains(*cc[0].Body, "48,6 milhões") || strings.Contains(*cc[0].Body, "<p>") {
		t.Fatalf("fonte CC BY guarda o corpo em texto: %v", cc[0].Body)
	}
	// Dedupe por título normalizado: mesma chave para variações de caixa/pontuação.
	if cc[0].TitleHash != cc[1].TitleHash {
		t.Fatal("títulos equivalentes deveriam ter o mesmo hash")
	}
	none := ParseItems(feed, config.RSSFeed{License: "none", StoreBody: true})
	if none[0].Body != nil {
		t.Fatal("portal sem licença não pode guardar corpo")
	}
	if none[0].PublishedAt == nil || none[0].Summary == "" {
		t.Fatal("título, resumo, URL e data devem ser guardados")
	}
}
