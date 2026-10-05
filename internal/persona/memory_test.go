package persona

import (
	"context"
	"testing"

	"github.com/gloffreda/tv-ta-ligado/internal/check"
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
	  {"persona":"duda","kind":"joke","content":"Duda imitou o Pedro Bial no intervalo."}]}`}})
	ex := &Extractor{LLM: m, Model: "mock", NameExceptions: []string{"Orlando", "Duda"}}
	kept, dropped, err := ex.Extract(context.Background(), []check.Line{{Speaker: "duda", Type: "banter", Text: "oi"}}, ps, []string{"Caio Brandão"})
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 1 || len(dropped) != 2 || kept[0].Kind != "feud" {
		t.Fatalf("kept=%+v dropped=%v", kept, dropped)
	}
}
