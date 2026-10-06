package ingest

import (
	"strings"
	"testing"
	"time"

	"github.com/gloffreda/tv-ta-ligado/internal/config"
	"github.com/gloffreda/tv-ta-ligado/internal/facts"
)

func TestAlertFacts(t *testing.T) {
	loc := time.FixedZone("BRT", -3*3600)
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, loc)
	body := []byte(`{"hoje":[
	  {"id":"1","descricao":"Tempestade","severidade":"Perigo Potencial","estados":"Goiás,Minas Gerais,Paraná,Santa Catarina","regioes":"Centro-Oeste,Sudeste,Sul","inicio":"2026-10-05 08:55","fim":"2026-10-05 23:59","encerrado":"False"},
	  {"id":"2","descricao":"Baixa Umidade","severidade":"Perigo","estados":"Bahia","regioes":"Nordeste","inicio":"2026-10-05 10:00","fim":"2026-10-05 18:00","encerrado":"False"},
	  {"id":"3","descricao":"Chuvas Intensas","severidade":"Perigo","estados":"Pará","regioes":"Norte","inicio":"2026-10-04 10:00","fim":"2026-10-05 09:00","encerrado":"False"}],
	 "futuro":[]}`)
	fs, err := AlertFacts(body, config.Alerts{SourceName: "INMET", URL: "https://x.invalid/avisos", Max: 3}, now, loc)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 2 {
		t.Fatalf("o aviso vencido fica de fora: %d", len(fs))
	}
	if fs[0].Series != "inmet:2" || fs[0].Kind != facts.Alert {
		t.Fatalf("o mais grave primeiro: %+v", fs[0])
	}
	want := "O INMET emitiu aviso de baixa umidade, com grau de severidade perigo, para Bahia, válido até as 18h00 de 05/10/2026."
	if fs[0].Claim != want {
		t.Fatalf("claim:\n%s\n%s", fs[0].Claim, want)
	}
	if !strings.Contains(fs[1].Claim, "áreas de 4 estados das regiões Centro-Oeste, Sudeste e Sul") {
		t.Fatal(fs[1].Claim)
	}
	if !fs[0].ExpiresAt.Equal(time.Date(2026, 10, 5, 18, 0, 0, 0, loc)) {
		t.Fatal("expira no fim do aviso")
	}
}
