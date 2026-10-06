package audit

import (
	"testing"

	"github.com/gloffreda/tv-ta-ligado/internal/check"
	"github.com/gloffreda/tv-ta-ligado/internal/config"
	"github.com/gloffreda/tv-ta-ligado/internal/testfix"
)

// Os casos existem nas quantidades pedidas e o que o estágio 1 já resolve não
// depende do juiz (este teste não chama LLM).
func TestCasesShape(t *testing.T) {
	cs, err := Load(testfix.Path("testdata", "adversarial"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"01_numeros.yaml": 15, "02_entidades.yaml": 10, "03_inferencias.yaml": 10, "04_banter_afirmacao.yaml": 10, "05_banter_zombaria.yaml": 5, "06_corretas.yaml": 10}
	got := map[string]int{}
	ids := map[string]bool{}
	for _, c := range cs {
		got[c.File]++
		if ids[c.ID] {
			t.Errorf("id repetido: %s", c.ID)
		}
		ids[c.ID] = true
		for _, id := range c.FactIDs {
			found := false
			for _, f := range c.Facts {
				found = found || f.ID == id
			}
			if !found {
				t.Errorf("%s cita fact_id %d que não está nos fatos", c.ID, id)
			}
		}
	}
	for f, n := range want {
		if got[f] != n {
			t.Errorf("%s: %d casos, want %d", f, got[f], n)
		}
	}
	if len(cs) != 60 {
		t.Fatalf("total %d", len(cs))
	}
	// As 10 corretas passam no estágio determinístico (senão seriam FP sem juiz).
	allow, _ := config.LoadAllowlist(testfix.Path("config"))
	names, _ := config.LoadFirstNames(testfix.Path("config"))
	lex := check.NewLexicon(allow, names)
	for _, c := range cs {
		if c.Expect != "pass" {
			continue
		}
		r := detOnly(c, c.toFacts(), lex)
		if !r.Passed {
			t.Errorf("%s deveria passar no estágio 1: %v", c.ID, r.Reasons)
		}
	}
}
