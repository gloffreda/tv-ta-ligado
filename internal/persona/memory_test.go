package persona

import (
	"context"
	"testing"

	"github.com/gloffreda/tv-ta-ligado/internal/check"
	"github.com/gloffreda/tv-ta-ligado/internal/config"
	"github.com/gloffreda/tv-ta-ligado/internal/facts"
	"github.com/gloffreda/tv-ta-ligado/internal/llm"
	"github.com/gloffreda/tv-ta-ligado/internal/testfix"
)

func TestMemoryNeverAboutRealPeople(t *testing.T) {
	ps, err := Load(testfix.Path("config"))
	if err != nil {
		t.Fatal(err)
	}
	m := llm.NewMock(map[string][]string{"memory": {`{"memories":[
	  {"persona":"duda","kind":"feud","content":"Duda diz que a cadeira do Orlando range de velha."},
	  {"persona":"orlando","kind":"opinion","content":"Orlando admira o Caio Brandão."},
	  {"persona":"duda","kind":"joke","content":"Duda imitou o Pedro Bial no intervalo."}]}`, `{"memories":[
	  {"persona":"orlando","kind":"running_gag","content":"Orlando sempre lembra do Banco Central quando vê a Duda em São Paulo."},
	  {"persona":"duda","kind":"joke","content":"Duda chamou o girassol de Chernobyl de faxineiro."}]}`}})
	allow, _ := config.LoadAllowlist(testfix.Path("config"))
	names, _ := config.LoadFirstNames(testfix.Path("config"))
	ex := &Extractor{LLM: m, Model: "mock", Lex: check.NewLexicon(allow, names)}
	known := []facts.Entity{{Name: "Caio Brandão", Type: facts.Person}, {Name: "Chernobyl", Type: facts.Place}}
	lines := []check.Line{{Speaker: "duda", Type: "banter", Text: "oi"}}
	kept, dropped, err := ex.Extract(context.Background(), lines, ps, known)
	if err != nil {
		t.Fatal(err)
	}
	k2, d2, err := ex.Extract(context.Background(), lines, ps, known)
	if err != nil {
		t.Fatal(err)
	}
	kept, dropped = append(kept, k2...), append(dropped, d2...)
	// Mantidas: a rixa e a que cita lugar/organização da allowlist.
	// Fora: pessoa conhecida, pessoa por prenome e lugar fora da allowlist.
	if len(kept) != 2 || len(dropped) != 3 || kept[0].Kind != "feud" || kept[1].Kind != "running_gag" {
		t.Fatalf("kept=%+v dropped=%v", kept, dropped)
	}
}
