package script

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/gloffreda/tv-ta-ligado/internal/check"
	"github.com/gloffreda/tv-ta-ligado/internal/config"
	"github.com/gloffreda/tv-ta-ligado/internal/llm"
	"github.com/gloffreda/tv-ta-ligado/internal/testfix"
)

func writer(t *testing.T, m llm.Client) (*Writer, config.Schedule) {
	sched, err := config.LoadSchedule(testfix.Path("config"))
	if err != nil {
		t.Fatal(err)
	}
	ps, err := config.LoadPersonas(testfix.Path("config"))
	if err != nil {
		t.Fatal(err)
	}
	return &Writer{LLM: m, Model: "mock", Schedule: sched, Personas: ps}, sched
}

func validScript(t *testing.T) string {
	return regexp.MustCompile(`\{\{\d+\}\}`).ReplaceAllString(testfix.LLM(t, "script_noticias.json"), "1")
}

func TestWriterRetriesOnceThenSucceeds(t *testing.T) {
	m := llm.NewMock(map[string][]string{"script": {"isto não é JSON", validScript(t)}})
	w, sched := writer(t, m)
	b, _ := sched.Block("noticias")
	s, attempts, err := w.Write(context.Background(), Input{Block: b, Facts: testfix.Facts(t)})
	if err != nil || attempts != 2 || len(s.Lines) != 16 {
		t.Fatalf("err=%v attempts=%d lines=%d", err, attempts, len(s.Lines))
	}
	if !strings.Contains(m.Calls[1].Prompt, "recusada pelo validador") {
		t.Fatal("a 2ª tentativa deve receber o erro de validação")
	}
}

func TestWriterRejectsAfterTwoInvalid(t *testing.T) {
	bad := `{"block":"noticias","lines":[{"speaker":"orlando","type":"fact","text":"oi","fact_ids":[]}]}`
	m := llm.NewMock(map[string][]string{"script": {bad, bad}})
	w, sched := writer(t, m)
	b, _ := sched.Block("noticias")
	if _, _, err := w.Write(context.Background(), Input{Block: b}); err == nil {
		t.Fatal("dois roteiros inválidos devem rejeitar o segmento")
	}
	if m.CallsFor("script") != 2 {
		t.Fatalf("chamadas=%d", m.CallsFor("script"))
	}
}

func TestPromptOnlyHasRundownFacts(t *testing.T) {
	m := llm.NewMock(map[string][]string{"script": {validScript(t)}})
	w, sched := writer(t, m)
	b, _ := sched.Block("noticias")
	fs := testfix.Facts(t)[:3]
	if _, _, err := w.Write(context.Background(), Input{Block: b, Facts: fs}); err != nil {
		t.Fatal(err)
	}
	p := m.Calls[0].Prompt
	if !strings.Contains(p, fs[0].Claim) || strings.Contains(p, "Marta Quintela") {
		t.Fatal("o prompt deve ter só os fatos da pauta")
	}
	if !strings.Contains(p, "Isso está checado? Então eu leio.") {
		t.Fatal("o prompt deve carregar as personas do YAML")
	}
}

func TestValidateRules(t *testing.T) {
	w, sched := writer(t, nil)
	b, _ := sched.Block("economia")
	s := Script{Block: "economia"}
	for i := 0; i < 8; i++ {
		s.Lines = append(s.Lines, lineOf("orlando", "fact", strings.Repeat("palavra ", 25), []int64{1}))
	}
	if err := w.Validate(&s, b); err != nil {
		t.Fatal(err)
	}
	if last := s.Lines[len(s.Lines)-1]; last.Text != b.ClosingLine || last.Type != "banter" {
		t.Fatalf("economia deve terminar com o aviso: %+v", last)
	}
	s.Lines[0].FactIDs = nil
	if err := w.Validate(&s, b); err == nil {
		t.Fatal("fala fact sem fact_ids deve ser inválida")
	}
	short := Script{Block: "economia"}
	for i := 0; i < 8; i++ {
		short.Lines = append(short.Lines, lineOf("duda", "banter", "curta demais", nil))
	}
	if err := w.Validate(&short, b); err == nil || !strings.Contains(err.Error(), "duração") {
		t.Fatalf("segmento curto deve falhar pela duração: %v", err)
	}
}

func lineOf(sp, typ, text string, ids []int64) (l checkLine) {
	return checkLine{Speaker: sp, Type: typ, Text: text, FactIDs: ids}
}

type checkLine = check.Line
