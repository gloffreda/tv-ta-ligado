package check

import (
	"context"
	"strings"
	"testing"

	"github.com/gloffreda/tv-ta-ligado/internal/config"
	"github.com/gloffreda/tv-ta-ligado/internal/facts"
	"github.com/gloffreda/tv-ta-ligado/internal/llm"
	"github.com/gloffreda/tv-ta-ligado/internal/testfix"
)

// lexicon usa a allowlist e os prenomes reais de config/.
func lexicon(t *testing.T) *Lexicon {
	allow, err := config.LoadAllowlist(testfix.Path("config"))
	if err != nil {
		t.Fatal(err)
	}
	names, err := config.LoadFirstNames(testfix.Path("config"))
	if err != nil {
		t.Fatal(err)
	}
	return NewLexicon(allow, names)
}

func env(t *testing.T) *Env {
	fm := testfix.FactMap(t)
	var known []facts.Entity
	var seg []facts.Fact
	for _, f := range fm {
		known = append(known, f.Entities...)
		seg = append(seg, f)
	}
	return &Env{Facts: fm, SegmentFacts: seg, KnownEntities: known, Lex: lexicon(t), Now: testfix.Now, Loc: testfix.Loc()}
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
		{"reprova banter com pessoa do banco", Line{"duda", TypeBanter, "Essa Marta Quintela manda bem, hein!", nil}, false, "Marta Quintela"},
		{"reprova banter com nome real fora do banco", Line{"duda", TypeBanter, "Parece até que o Pedro Bial apresenta esse jornal.", nil}, false, "Pedro"},
		{"reprova banter com sobrenome composto", Line{"duda", TypeBanter, "Hoje o Xavante Tucunaré passou no estúdio.", nil}, false, "Xavante Tucunaré"},
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

// Os falsos positivos do relatório de 05/10 passam; pessoas reais reprovam.
func TestBanterNames(t *testing.T) {
	e := env(t)
	// Entidades como vieram da extração real: "mercado" e "Novo" (partido) são orgs.
	e.KnownEntities = append(e.KnownEntities,
		facts.Entity{Name: "mercado", Type: facts.Org}, facts.Entity{Name: "Novo", Type: facts.Org},
		facts.Entity{Name: "Vitória", Type: facts.Place}, facts.Entity{Name: "Câmara dos Deputados", Type: facts.Org},
		facts.Entity{Name: "Chernobyl", Type: facts.Place}, facts.Entity{Name: "Felipe Massa", Type: facts.Person},
		facts.Entity{Name: "Pipo Massa", Type: facts.Person})
	// Chernobyl está num fato do segmento: lugar do segmento pode aparecer no banter.
	e.SegmentFacts = append(e.SegmentFacts, facts.Fact{ID: 900, Kind: facts.Headline, Claim: "Girassóis foram usados perto do reator de Chernobyl.",
		Entities: []facts.Entity{{Name: "Chernobyl", Type: facts.Place}}})
	pass := []string{
		"Traduzindo: a Selic manda no resto dos juros, tá ligado?",
		"Esse índice mede o quanto o mercado anda nervoso.",
		"Novo dia, nova previsão, Orlando. De novo eu acertei a gíria.",
		"Uma vitória em dupla, hein? Em Vitória o céu tá bonito.",
		"A Câmara dos Deputados vai ficar mais enxuta, Orlando.",
		"Girassol em Chernobyl trabalhando de faxineira. Respeito total!",
		"No meu tempo, a máquina era de escrever.",
		"Orlando Pimenta e Duda Faísca, direto do estúdio do TV Tá Ligado.",
		"Bora pro tempo? Em São Paulo e no Rio de Janeiro tem chuva no radar.",
	}
	for _, text := range pass {
		if r := Deterministic(Line{"duda", TypeBanter, text, nil}, e); !r.Passed {
			t.Errorf("deveria passar: %q → %v", text, r.Reasons)
		}
	}
	fail := map[string]string{
		"Valeu, Léo, pela pauta de hoje!":        "Léo",
		"Clarice mandou um beijo pro estúdio.":   "Clarice",
		"O Felipe Massa ia adorar esse estúdio.": "Felipe",
		"Felipe e Pipo são da mesma família.":    "Felipe",
		"Quem diria, Massa e família no pódio.":  "",
	}
	for text, want := range fail {
		r := Deterministic(Line{"duda", TypeBanter, text, nil}, e)
		if want == "" {
			continue // "Massa" sozinho não é pessoa conhecida nem prenome: o juiz decide
		}
		if r.Passed || !strings.Contains(strings.Join(r.Reasons, "|"), want) {
			t.Errorf("deveria reprovar citando %q: %q → %v", want, text, r.Reasons)
		}
	}
	// Pessoa do banco também reprova pelo nome completo, sem depender do prenome.
	r := Deterministic(Line{"duda", TypeBanter, "Hoje tem Pipo Massa no pódio!", nil}, e)
	if r.Passed || !strings.Contains(strings.Join(r.Reasons, "|"), "pessoa real") {
		t.Errorf("pessoa conhecida deveria reprovar: %v", r.Reasons)
	}
}

func TestFactLineNames(t *testing.T) {
	e := env(t)
	e.KnownEntities = append(e.KnownEntities, facts.Entity{Name: "Vitória", Type: facts.Place})
	// "vitória" minúscula não é a cidade; a capital, na allowlist, também passa.
	if r := Deterministic(Line{"orlando", TypeFact, "Unidos da Serra conquistou uma vitória sobre o Atlético Lagoa por 3 a 1.", []int64{9}}, e); !r.Passed {
		t.Fatalf("falso positivo: %v", r.Reasons)
	}
	if r := Deterministic(Line{"orlando", TypeFact, "A prefeita Marta Quintela e a vereadora Joana inauguraram a ponte.", []int64{6}}, e); r.Passed {
		t.Fatal("prenome fora dos fatos citados deveria reprovar")
	}
}

func TestMentionsEntityCase(t *testing.T) {
	if MentionsEntity("de novo, Orlando", "Novo") {
		t.Error("palavra comum minúscula não é a entidade de uma palavra")
	}
	if !MentionsEntity("O Novo anunciou", "Novo") {
		t.Error("mesma grafia deveria contar")
	}
	if !MentionsEntity("a camara dos deputados", "Câmara dos Deputados") {
		t.Error("nome composto ignora caixa e acento")
	}
}

// ---- fluxo de reescrita ----

type fakeRewriter struct {
	out   []Line
	calls int
}

func (f *fakeRewriter) Rewrite(_ context.Context, _, _ Line, _ []string) (Line, error) {
	f.calls++
	return f.out[min(f.calls, len(f.out))-1], nil
}

// judgeMock responde no modo certo (fact ou banter).
func judgeMock(t *testing.T, fact, banter string) *llm.Mock {
	m := llm.NewMock(nil)
	m.Handler = func(r llm.Request) (string, error) {
		if strings.Contains(r.System, "banter") && strings.Contains(r.Prompt, "Fala (banter)") {
			return testfix.LLM(t, banter), nil
		}
		return testfix.LLM(t, fact), nil
	}
	return m
}

func judgeOK(t *testing.T) *Judge {
	return &Judge{LLM: judgeMock(t, "judge_ok.json", "judge_banter_ok.json"), Model: "mock"}
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

func TestFlowSecondRewriteFixesDeterministicWithoutJudge(t *testing.T) {
	m := judgeMock(t, "judge_ok.json", "judge_banter_ok.json")
	rw := &fakeRewriter{out: []Line{
		{"orlando", TypeFact, "O dólar comercial ficou em R$ 5,30, segundo o Banco Central.", []int64{1}},
		{"orlando", TypeFact, "O dólar comercial ficou em R$ 5,21, segundo o Banco Central.", []int64{1}},
	}}
	f := &Flow{Env: env(t), Judge: &Judge{LLM: m, Model: "mock"}, Rewriter: rw}
	out, _ := f.CheckLine(context.Background(), Line{"orlando", TypeFact, "O dólar fechou em R$ 5,20.", []int64{1}})
	if out.Status != "rewritten" || rw.calls != 2 {
		t.Fatalf("status=%s rewrites=%d reason=%s", out.Status, rw.calls, out.Reason)
	}
	// A 1ª reescrita reprovou no estágio 1: o juiz só viu a 2ª.
	if m.CallsFor("judge") != 1 {
		t.Fatalf("juiz chamado %d vezes; esperado 1", m.CallsFor("judge"))
	}
}

func TestFlowDropsAfterTwoRewrites(t *testing.T) {
	rw := &fakeRewriter{out: []Line{
		{"orlando", TypeFact, "O dólar comercial ficou em R$ 5,30, segundo o Banco Central.", []int64{1}},
		{"orlando", TypeFact, "O dólar comercial ficou em R$ 5,40, segundo o Banco Central.", []int64{1}},
		{"orlando", TypeFact, "nunca deveria ser usada", []int64{1}},
	}}
	f := &Flow{Env: env(t), Judge: judgeOK(t), Rewriter: rw}
	out, err := f.CheckLine(context.Background(), Line{"orlando", TypeFact, "O dólar fechou em R$ 5,20.", []int64{1}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "dropped" || rw.calls != MaxRewrites || len(out.Attempts) != 3 {
		t.Fatalf("status=%s rewrites=%d attempts=%d", out.Status, rw.calls, len(out.Attempts))
	}
	for _, want := range []string{"5,20", "5,30", "5,40"} {
		if !strings.Contains(out.Reason, want) {
			t.Fatalf("o motivo deve registrar as três tentativas (%s): %s", want, out.Reason)
		}
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

// Juiz em dois modos: banter só reprova afirmação factual nova ou zombaria.
func TestJudgeBanterMode(t *testing.T) {
	m := llm.NewMock(nil)
	m.Handler = func(r llm.Request) (string, error) {
		if !strings.Contains(r.System, "new_factual_claim") {
			t.Errorf("banter deveria usar o modo banter do juiz")
		}
		if strings.Contains(r.Prompt, "mesma família") {
			return testfix.LLM(t, "judge_banter_claim.json"), nil
		}
		return testfix.LLM(t, "judge_banter_ok.json"), nil
	}
	rw := &fakeRewriter{out: []Line{{"duda", TypeBanter, "Os pilotos são da mesma família, viu?", nil}}}
	f := &Flow{Env: env(t), Judge: &Judge{LLM: m, Model: "mock"}, Rewriter: rw}

	ok, _ := f.CheckLine(context.Background(), Line{"orlando", TypeBanter, "No meu tempo, a máquina era de escrever.", nil})
	if ok.Status != "ok" {
		t.Fatalf("opinião/bordão do avatar deveria aprovar: %s %s", ok.Status, ok.Reason)
	}
	bad, _ := f.CheckLine(context.Background(), Line{"duda", TypeBanter, "Felipe e Pipo são da mesma família.", nil})
	if bad.Status != "dropped" {
		t.Fatalf("afirmação de parentesco sem fonte deveria cair: %s", bad.Status)
	}
	if !strings.Contains(bad.Reason, "prenome") || !strings.Contains(bad.Reason, "afirmação factual nova") {
		t.Fatalf("deveria reprovar no estágio 1 (nomes) e no juiz (afirmação nova): %s", bad.Reason)
	}
}

func TestFlowJudgeRealPersonMocked(t *testing.T) {
	m := llm.NewMock(nil)
	m.Handler = func(r llm.Request) (string, error) {
		return `{"new_factual_claim": false, "claim": "", "real_person_mocked": true}`, nil
	}
	rw := &fakeRewriter{out: []Line{{"duda", TypeBanter, "Orlando, que dia, hein?", nil}}}
	f := &Flow{Env: env(t), Judge: &Judge{LLM: m, Model: "mock"}, Rewriter: rw}
	out, _ := f.CheckLine(context.Background(), Line{"duda", TypeBanter, "Que figura, hein?", nil})
	if out.Status != "dropped" || !strings.Contains(out.Reason, "pessoa real") {
		t.Fatalf("status=%s reason=%s", out.Status, out.Reason)
	}
}

func TestFlowJudgeOnlyAfterDeterministic(t *testing.T) {
	m := judgeMock(t, "judge_ok.json", "judge_banter_ok.json")
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
	removed := outcomes(6, 0, 2) // 8 falas, 3 removidas → 5 < 6
	for i := range removed[:3] {
		removed[i].Status = "removed"
	}
	if st, _ := Decide(removed, r); st != "rejected" {
		t.Fatal("falas removidas pela continuidade saem da contagem de falas")
	}
	if st, _ := Decide(outcomes(8, 0, 2), r); st != "approved" {
		t.Fatal("remoção não é corte")
	}
	r.ClosingLine = "Isso não é recomendação de investimento."
	if st, _ := Decide(outcomes(6, 0, 2), r); st != "rejected" {
		t.Fatal("economia sem a frase final deveria rejeitar")
	}
}
