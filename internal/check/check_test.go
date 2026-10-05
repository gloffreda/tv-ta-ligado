package check

import (
	"context"
	"strings"
	"testing"

	"github.com/gloffreda/tv-ta-ligado/internal/llm"
	"github.com/gloffreda/tv-ta-ligado/internal/testfix"
)

func env(t *testing.T) *Env {
	fm := testfix.FactMap(t)
	var known []string
	for _, f := range fm {
		known = append(known, f.Entities...)
	}
	return &Env{
		Facts: fm, KnownEntities: known, Now: testfix.Now, Loc: testfix.Loc(),
		NameExceptions: []string{"Orlando", "Duda", "Orlando Pimenta", "Duda Faísca", "Brasil", "Isso"},
	}
}

func TestDeterministic(t *testing.T) {
	e := env(t)
	cases := []struct {
		name   string
		line   Line
		ok     bool
		reason string // trecho esperado no motivo
	}{
		{"aceita fala correta", Line{"orlando", TypeFact, "O dólar comercial fechou em R$ 5,21 no dia 2 de outubro, segundo o Banco Central.", []int64{1}}, true, ""},
		{"aceita valor exato e percentual", Line{"orlando", TypeFact, "A meta da Selic está em 13,75% ao ano, definida pelo Copom.", []int64{2}}, true, ""},
		{"aceita queda sem sinal", Line{"duda", TypeFact, "O IPCA teve deflação de 0,32% em agosto.", []int64{3}}, true, ""},
		{"aceita magnitude", Line{"orlando", TypeFact, "Monte Verde exportou US$ 1,2 bilhão em café no trimestre.", []int64{11}}, true, ""},
		{"aceita mil por extenso de número", Line{"orlando", TypeFact, "A Companhia Estrela do Sul vai contratar 2.500 trabalhadores.", []int64{8}}, true, ""},
		{"aceita placar e dia da semana", Line{"duda", TypeFact, "Unidos da Serra venceu o Atlético Lagoa por 3 a 1 no domingo.", []int64{9}}, true, ""},
		{"aceita clima arredondado", Line{"orlando", TypeFact, "Em Vila Serena, máxima de 27 graus e 40% de chance de chuva.", []int64{4}}, true, ""},
		{"reprova número ausente", Line{"orlando", TypeFact, "O dólar comercial fechou em R$ 5,43, segundo o Banco Central.", []int64{1}}, false, "5,43"},
		{"reprova arredondamento errado", Line{"orlando", TypeFact, "O dólar comercial fechou em R$ 5,20, segundo o Banco Central.", []int64{1}}, false, "5,20"},
		{"reprova clima truncado", Line{"orlando", TypeFact, "Em Vila Serena, máxima de 27,3 graus.", []int64{4}}, false, "27,3"},
		{"reprova pessoa fora de entities", Line{"orlando", TypeFact, "A prefeita Marta Quintela e o governador Rui Falcão inauguraram a ponte em Vila Serena.", []int64{6}}, false, "Rui Falcão"},
		{"reprova entidade conhecida de outro fato", Line{"orlando", TypeFact, "Caio Brandão foi à inauguração da ponte de Vila Serena.", []int64{6}}, false, "Caio Brandão"},
		{"reprova banter com número", Line{"duda", TypeBanter, "Cinco minutos de ponte e eu já tô cansada.", nil}, false, "Cinco"},
		{"reprova banter com dígito", Line{"duda", TypeBanter, "Isso aí é 100% verdade, Orlando.", nil}, false, "100%"},
		{"reprova banter com data", Line{"duda", TypeBanter, "Desde 2 de outubro que eu espero esse café.", nil}, false, "2 de outubro"},
		{"reprova banter com nome do banco", Line{"duda", TypeBanter, "Essa Marta Quintela manda bem, hein!", nil}, false, "Marta Quintela"},
		{"reprova banter com nome real fora do banco", Line{"duda", TypeBanter, "Parece até que o Pedro Bial apresenta esse jornal.", nil}, false, "Pedro Bial"},
		{"aceita banter limpo", Line{"duda", TypeBanter, "Orlando, até a máquina ficou impressionada com essa ponte. Tá ligado?", nil}, true, ""},
		{"reprova fato vencido", Line{"orlando", TypeFact, "A ponte velha do rio Claro foi interditada para obras.", []int64{20}}, false, "vencido"},
		{"reprova fact_id inexistente", Line{"orlando", TypeFact, "A ponte foi inaugurada.", []int64{999}}, false, "inexistente"},
		{"reprova fact sem fact_ids", Line{"orlando", TypeFact, "A ponte foi inaugurada.", nil}, false, "sem fact_ids"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := Deterministic(c.line, e)
			if r.Passed != c.ok {
				t.Fatalf("passed=%v, want %v; reasons=%v", r.Passed, c.ok, r.Reasons)
			}
			if c.reason != "" && !strings.Contains(strings.Join(r.Reasons, "|"), c.reason) {
				t.Fatalf("motivo deveria citar %q: %v", c.reason, r.Reasons)
			}
		})
	}
}

func TestForbiddenPhrases(t *testing.T) {
	e := env(t)
	e.ForbiddenPhrases = []string{"invista", "hora de comprar"}
	r := Deterministic(Line{"duda", TypeBanter, "Então é hora de comprar, galera!", nil}, e)
	if r.Passed {
		t.Fatal("recomendação de investimento deveria reprovar")
	}
}

func TestDetectNames(t *testing.T) {
	ex := []string{"Orlando", "Duda"}
	cases := map[string][]string{
		"Orlando, o Banco Central do Brasil subiu os juros.": {"Banco Central do Brasil"},
		"Banco Central anunciou hoje.":                       {"Central"},
		"Hoje tem sol. Pedro Bial chegou.":                   {"Bial"},
		"Olha só, Duda e Orlando no estúdio!":                nil,
		"A máxima é de 27 °C em R$ alta.":                    nil,
	}
	for in, want := range cases {
		got := DetectNames(in, ex)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%q: got %v, want %v", in, got, want)
		}
	}
}

// ---- fluxo de reescrita ----

type fakeRewriter struct {
	out   []Line
	calls int
}

func (f *fakeRewriter) Rewrite(_ context.Context, l Line, _ []string) (Line, error) {
	f.calls++
	return f.out[f.calls-1], nil
}

func judgeOK(t *testing.T) *Judge {
	m := llm.NewMock(nil)
	m.Handler = func(r llm.Request) (string, error) { return testfix.LLM(t, "judge_ok.json"), nil }
	return &Judge{LLM: m, Model: "mock"}
}

func TestFlowApprovesOnSecondAttempt(t *testing.T) {
	rw := &fakeRewriter{out: []Line{{"orlando", TypeFact, "O dólar comercial ficou em R$ 5,21 na sexta, 2 de outubro, segundo o Banco Central.", []int64{1}}}}
	f := &Flow{Env: env(t), Judge: judgeOK(t), Rewriter: rw}
	out, err := f.CheckLine(context.Background(), Line{"orlando", TypeFact, "O dólar fechou em R$ 5,20, segundo o Banco Central.", []int64{1}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "rewritten" || len(out.Attempts) != 2 || rw.calls != 1 {
		t.Fatalf("status=%s attempts=%d rewrites=%d reason=%s", out.Status, len(out.Attempts), rw.calls, out.Reason)
	}
}

func TestFlowDropsWithoutThirdAttempt(t *testing.T) {
	rw := &fakeRewriter{out: []Line{
		{"orlando", TypeFact, "O dólar comercial ficou em R$ 5,30, segundo o Banco Central.", []int64{1}},
		{"orlando", TypeFact, "nunca deveria ser usada", []int64{1}},
	}}
	f := &Flow{Env: env(t), Judge: judgeOK(t), Rewriter: rw}
	out, err := f.CheckLine(context.Background(), Line{"orlando", TypeFact, "O dólar fechou em R$ 5,20.", []int64{1}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "dropped" {
		t.Fatalf("status=%s, want dropped", out.Status)
	}
	if rw.calls != 1 || len(out.Attempts) != 2 {
		t.Fatalf("a fala só pode ser reescrita uma vez: rewrites=%d attempts=%d", rw.calls, len(out.Attempts))
	}
	if !strings.Contains(out.Reason, "5,30") || !strings.Contains(out.Reason, "5,20") {
		t.Fatalf("o motivo deve registrar as duas tentativas: %s", out.Reason)
	}
}

func TestFlowJudgeRejects(t *testing.T) {
	m := llm.NewMock(map[string][]string{"judge": {testfix.LLM(t, "judge_unsupported.json"), testfix.LLM(t, "judge_ok.json")}})
	rw := &fakeRewriter{out: []Line{{"orlando", TypeFact, "A Agência Nacional de Águas Fictícias declarou escassez no rio Claro até 31 de outubro.", []int64{12}}}}
	f := &Flow{Env: env(t), Judge: &Judge{LLM: m, Model: "mock"}, Rewriter: rw}
	out, err := f.CheckLine(context.Background(), Line{"orlando", TypeFact, "A Agência Nacional de Águas Fictícias declarou escassez no rio Claro por causa da seca.", []int64{12}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "rewritten" {
		t.Fatalf("status=%s reason=%s", out.Status, out.Reason)
	}
	if out.Attempts[0].Results[1].Stage != StageJudge || out.Attempts[0].Results[1].Passed {
		t.Fatalf("o juiz deveria ter reprovado a 1ª tentativa: %+v", out.Attempts[0].Results)
	}
}

func TestFlowJudgeRealPersonMocked(t *testing.T) {
	m := llm.NewMock(nil)
	m.Handler = func(r llm.Request) (string, error) { return testfix.LLM(t, "judge_mocked.json"), nil }
	rw := &fakeRewriter{out: []Line{{"duda", TypeBanter, "Orlando, que dia, hein?", nil}}}
	f := &Flow{Env: env(t), Judge: &Judge{LLM: m, Model: "mock"}, Rewriter: rw}
	out, _ := f.CheckLine(context.Background(), Line{"duda", TypeBanter, "Que figura, hein?", nil})
	if out.Status != "dropped" || !strings.Contains(out.Reason, "pessoa real") {
		t.Fatalf("status=%s reason=%s", out.Status, out.Reason)
	}
}

func TestFlowJudgeOnlyAfterDeterministic(t *testing.T) {
	m := llm.NewMock(nil)
	m.Handler = func(r llm.Request) (string, error) { return testfix.LLM(t, "judge_ok.json"), nil }
	rw := &fakeRewriter{out: []Line{{"duda", TypeBanter, "Ainda tem 3 coisas.", nil}}}
	f := &Flow{Env: env(t), Judge: &Judge{LLM: m, Model: "mock"}, Rewriter: rw}
	_, _ = f.CheckLine(context.Background(), Line{"duda", TypeBanter, "Tenho 2 coisas a dizer.", nil})
	if m.CallsFor("judge") != 0 {
		t.Fatalf("juiz não pode ser chamado se o estágio 1 reprovou (%d chamadas)", m.CallsFor("judge"))
	}
}

func outcomes(facts, droppedFacts, banter int) []Outcome {
	var outs []Outcome
	for i := 0; i < facts; i++ {
		st := "ok"
		if i < droppedFacts {
			st = "dropped"
		}
		outs = append(outs, Outcome{Original: Line{Type: TypeFact}, Final: Line{Type: TypeFact, Text: "x"}, Status: st})
	}
	for i := 0; i < banter; i++ {
		outs = append(outs, Outcome{Original: Line{Type: TypeBanter}, Final: Line{Type: TypeBanter, Text: "y"}, Status: "ok"})
	}
	return outs
}

func TestDecide(t *testing.T) {
	r := Rules{MaxFactDropRatio: 0.30, MinLines: 6}
	if st, why := Decide(outcomes(10, 4, 4), r); st != "rejected" {
		t.Fatalf("40%% de falas fact cortadas deveria rejeitar: %s %s", st, why)
	}
	if st, why := Decide(outcomes(10, 3, 4), r); st != "approved" {
		t.Fatalf("30%% exatos ainda aprova: %s %s", st, why)
	}
	if st, _ := Decide(outcomes(4, 0, 1), r); st != "rejected" {
		t.Fatal("menos de 6 falas deveria rejeitar")
	}
	r.ClosingLine = "Isso não é recomendação de investimento."
	if st, _ := Decide(outcomes(6, 0, 2), r); st != "rejected" {
		t.Fatal("economia sem a frase final deveria rejeitar")
	}
}
