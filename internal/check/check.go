// Package check decide se uma fala pode ir ao ar. Estágio 1 é código
// determinístico; estágio 2 é um juiz (LLM). Na dúvida, a fala é cortada.
package check

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gloffreda/tv-ta-ligado/internal/brnum"
	"github.com/gloffreda/tv-ta-ligado/internal/facts"
	"github.com/gloffreda/tv-ta-ligado/internal/llm"
	"github.com/gloffreda/tv-ta-ligado/internal/textutil"
)

const (
	TypeFact   = "fact"
	TypeBanter = "banter"

	StageDeterministic = "deterministic"
	StageJudge         = "judge"
)

// Line é uma fala do roteiro.
type Line struct {
	Speaker string  `json:"speaker"`
	Type    string  `json:"type"`
	Text    string  `json:"text"`
	FactIDs []int64 `json:"fact_ids"`
}

// Env é o que a checagem determinística precisa saber.
type Env struct {
	Facts            map[int64]facts.Fact // ao menos os referenciados
	KnownEntities    []string             // entidades de todos os fatos do banco
	NameExceptions   []string
	ForbiddenPhrases []string
	Now              time.Time
	Loc              *time.Location
}

// StageResult é uma linha de check_log.
type StageResult struct {
	Stage   string         `json:"stage"`
	Passed  bool           `json:"passed"`
	Reasons []string       `json:"reasons,omitempty"`
	Detail  map[string]any `json:"detail,omitempty"`
}

// Deterministic aplica as regras de código a uma fala.
func Deterministic(l Line, env *Env) StageResult {
	var reasons []string
	fail := func(format string, a ...any) { reasons = append(reasons, fmt.Sprintf(format, a...)) }

	text := strings.TrimSpace(l.Text)
	if text == "" {
		fail("fala vazia")
	}
	for _, p := range env.ForbiddenPhrases {
		if textutil.ContainsPhrase(text, p) {
			fail("expressão proibida no bloco: %q", p)
		}
	}

	var refs []facts.Fact
	var refEntities []string
	switch l.Type {
	case TypeFact:
		if len(l.FactIDs) == 0 {
			fail("fala fact sem fact_ids")
		}
		for _, id := range l.FactIDs {
			f, ok := env.Facts[id]
			if !ok {
				fail("fact_id %d inexistente", id)
				continue
			}
			if f.Expired(env.Now) {
				fail("fact_id %d vencido (validade até %s)", id, f.ExpiresAt.In(env.Loc).Format("02/01 15:04"))
				continue
			}
			refs = append(refs, f)
			refEntities = append(refEntities, f.Entities...)
		}
	case TypeBanter:
	default:
		fail("tipo de fala desconhecido: %q", l.Type)
	}

	// Números.
	nums := brnum.Extract(text)
	if l.Type == TypeBanter {
		for _, n := range nums {
			fail("banter com número/data/valor: %q", n.Raw)
		}
	} else if len(refs) > 0 {
		for _, n := range nums {
			if !NumberSupported(n, refs, env.Loc) {
				fail("número %q não bate com os fatos referenciados", n.Raw)
			}
		}
	}

	// Nomes: heurística + entidades conhecidas do banco.
	names := DetectNames(text, env.NameExceptions)
	exc := map[string]bool{}
	for _, e := range env.NameExceptions {
		exc[textutil.Normalize(e)] = true
	}
	for _, e := range env.KnownEntities {
		if !exc[textutil.Normalize(e)] && MentionsEntity(text, e) {
			names = append(names, e)
		}
	}
	names = dedupe(names)
	if l.Type == TypeBanter {
		for _, n := range names {
			fail("banter com nome próprio: %q", n)
		}
	} else if len(refs) > 0 {
		for _, n := range names {
			if !covered(n, refEntities) {
				fail("nome %q fora de entities dos fatos referenciados", n)
			}
		}
	}

	res := StageResult{Stage: StageDeterministic, Passed: len(reasons) == 0, Reasons: reasons}
	res.Detail = map[string]any{"numbers": rawNumbers(nums), "names": names}
	return res
}

func rawNumbers(ns []brnum.Number) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = n.Raw
	}
	return out
}

func dedupe(ss []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range ss {
		k := textutil.Normalize(s)
		if !seen[k] {
			seen[k] = true
			out = append(out, s)
		}
	}
	return out
}

// NumberSupported: o número da fala bate com o value ou com o texto de algum
// fato referenciado (com arredondamento correto em até as casas escritas).
func NumberSupported(n brnum.Number, refs []facts.Fact, loc *time.Location) bool {
	for _, f := range refs {
		pool := brnum.Extract(f.Claim)
		asOf := f.AsOf.In(loc)
		asOfParts := brnum.DateParts{Day: asOf.Day(), Month: int(asOf.Month()), Year: asOf.Year()}
		switch {
		case n.Kind == brnum.Date:
			if facts.DatePartsCompatible(n.D, asOfParts) {
				return true
			}
			for _, p := range pool {
				if p.Kind == brnum.Date && facts.DatePartsCompatible(n.D, p.D) {
					return true
				}
			}
		case n.Kind == brnum.Time:
			for _, p := range pool {
				if p.Kind == brnum.Time && p.Value == n.Value {
					return true
				}
			}
		case !n.HasValue:
			if textutil.ContainsPhrase(f.Claim, n.Raw) {
				return true
			}
		default:
			if f.Value != nil && n.Matches(*f.Value) {
				return true
			}
			isInt := n.Scale == 1 && n.Decimals == 0
			v := int(n.Value)
			if isInt && (v == asOfParts.Day || v == asOfParts.Month || v == asOfParts.Year) {
				return true
			}
			for _, p := range pool {
				if p.Kind == brnum.Date {
					if isInt && (v == p.D.Day || v == p.D.Month || v == p.D.Year) {
						return true
					}
					continue
				}
				if p.HasValue && n.Matches(p.Value) {
					return true
				}
			}
		}
	}
	return false
}

// ---- Estágio 2: juiz ----

type JudgeResult struct {
	Entailed         bool     `json:"entailed"`
	Unsupported      []string `json:"unsupported"`
	RealPersonMocked bool     `json:"real_person_mocked"`
}

var judgeSchema = llm.MustSchema("judge.json", `{
  "type": "object",
  "required": ["entailed", "unsupported", "real_person_mocked"],
  "properties": {
    "entailed": {"type": "boolean"},
    "unsupported": {"type": "array", "items": {"type": "string"}},
    "real_person_mocked": {"type": "boolean"}
  }
}`)

const judgeSystem = `Você é o checador de fatos de um telejornal brasileiro apresentado por dois avatares fictícios, Orlando Pimenta e Duda Faísca. Sua função é impedir que qualquer afirmação sem fonte vá ao ar. Seja rigoroso: na dúvida, reprove.

Você recebe UMA fala e o texto dos fatos que a sustentam.

Para fala do tipo "fact":
- entailed=true somente se TODA afirmação factual da fala estiver sustentada pelos fatos (paráfrase é permitida; arredondamento correto é permitido).
- Causas, consequências, previsões, comparações, contexto, adjetivos que impliquem fatos novos, ou qualquer detalhe ausente dos fatos tornam entailed=false. Liste esses trechos em "unsupported".

Para fala do tipo "banter" (comentário, reação, piada):
- entailed=true se a fala não fizer nenhuma afirmação factual concreta sobre o mundo real. Opiniões, reações, brincadeiras entre os avatares e comentários genéricos são permitidos.
- Qualquer afirmação concreta sobre o mundo real torna entailed=false; liste o trecho em "unsupported".

Para qualquer tipo:
- real_person_mocked=true se a fala ridiculariza, zomba ou faz piada com uma pessoa real (qualquer pessoa que não seja os avatares Orlando Pimenta e Duda Faísca) ou com um grupo real de pessoas.

Responda apenas com JSON, sem texto fora dele:
{"entailed": true|false, "unsupported": ["trecho", ...], "real_person_mocked": true|false}`

type Judge struct {
	LLM   llm.Client
	Model string
}

func (j *Judge) Evaluate(ctx context.Context, l Line, refs []facts.Fact) (JudgeResult, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "Tipo da fala: %s\nFala: %q\n\nFatos referenciados:\n", l.Type, l.Text)
	if len(refs) == 0 {
		b.WriteString("(nenhum)\n")
	}
	for _, f := range refs {
		fmt.Fprintf(&b, "- [%d] %s (fonte: %s)\n", f.ID, f.Claim, f.SourceName)
	}
	resp, err := j.LLM.Complete(ctx, llm.Request{
		Purpose: "judge", Model: j.Model, System: judgeSystem, Prompt: b.String(),
		MaxTokens: 4000, Temperature: llm.Float(0), Effort: "low",
	})
	if err != nil {
		return JudgeResult{}, err
	}
	var r JudgeResult
	if err := judgeSchema.Decode(resp.Text, &r); err != nil {
		return JudgeResult{}, err
	}
	return r, nil
}

// ---- Fluxo por fala ----

// Rewriter reescreve uma fala reprovada, recebendo o motivo.
type Rewriter interface {
	Rewrite(ctx context.Context, l Line, reasons []string) (Line, error)
}

type Attempt struct {
	N       int
	Line    Line
	Results []StageResult
}

type Outcome struct {
	Original Line
	Final    Line
	Status   string // ok | rewritten | dropped
	Reason   string
	Attempts []Attempt
}

type Flow struct {
	Env      *Env
	Judge    *Judge
	Rewriter Rewriter
}

func (f *Flow) evaluate(ctx context.Context, l Line) ([]StageResult, []string, error) {
	det := Deterministic(l, f.Env)
	results := []StageResult{det}
	if !det.Passed {
		return results, det.Reasons, nil
	}
	var refs []facts.Fact
	for _, id := range l.FactIDs {
		if fa, ok := f.Env.Facts[id]; ok {
			refs = append(refs, fa)
		}
	}
	jr, err := f.Judge.Evaluate(ctx, l, refs)
	if err != nil {
		if llm.Fatal(err) {
			return results, nil, err
		}
		reason := "juiz indisponível ou resposta inválida: " + err.Error()
		results = append(results, StageResult{Stage: StageJudge, Passed: false, Reasons: []string{reason}})
		return results, []string{reason}, nil
	}
	var reasons []string
	if !jr.Entailed {
		r := "juiz: afirmação sem sustentação nos fatos"
		if len(jr.Unsupported) > 0 {
			r += ": " + strings.Join(jr.Unsupported, " | ")
		}
		reasons = append(reasons, r)
	}
	if jr.RealPersonMocked {
		reasons = append(reasons, "juiz: piada/zombaria com pessoa real")
	}
	results = append(results, StageResult{Stage: StageJudge, Passed: len(reasons) == 0, Reasons: reasons,
		Detail: map[string]any{"entailed": jr.Entailed, "unsupported": jr.Unsupported, "real_person_mocked": jr.RealPersonMocked}})
	return results, reasons, nil
}

// CheckLine: reprovou, reescreve uma vez com o motivo; reprovou de novo, corta.
func (f *Flow) CheckLine(ctx context.Context, l Line) (Outcome, error) {
	out := Outcome{Original: l}
	res, reasons, err := f.evaluate(ctx, l)
	out.Attempts = append(out.Attempts, Attempt{N: 1, Line: l, Results: res})
	if err != nil {
		return out, err
	}
	if len(reasons) == 0 {
		out.Final, out.Status = l, "ok"
		return out, nil
	}
	rw, err := f.Rewriter.Rewrite(ctx, l, reasons)
	if err != nil {
		if llm.Fatal(err) {
			return out, err
		}
		out.Final, out.Status = l, "dropped"
		out.Reason = strings.Join(reasons, "; ") + " | reescrita falhou: " + err.Error()
		return out, nil
	}
	// A reescrita mantém quem fala e o tipo.
	rw.Speaker, rw.Type = l.Speaker, l.Type
	res2, reasons2, err := f.evaluate(ctx, rw)
	out.Attempts = append(out.Attempts, Attempt{N: 2, Line: rw, Results: res2})
	if err != nil {
		return out, err
	}
	if len(reasons2) == 0 {
		out.Final, out.Status = rw, "rewritten"
		return out, nil
	}
	out.Final, out.Status = rw, "dropped"
	out.Reason = "1ª tentativa: " + strings.Join(reasons, "; ") + " | 2ª tentativa: " + strings.Join(reasons2, "; ")
	return out, nil
}

// CheckAll checa as falas em paralelo (limitado), preservando a ordem.
func (f *Flow) CheckAll(ctx context.Context, lines []Line, concurrency int) ([]Outcome, error) {
	if concurrency < 1 {
		concurrency = 1
	}
	outs := make([]Outcome, len(lines))
	errs := make([]error, len(lines))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i, l := range lines {
		wg.Add(1)
		go func(i int, l Line) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			outs[i], errs[i] = f.CheckLine(ctx, l)
		}(i, l)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return outs, err
		}
	}
	return outs, nil
}

// Rules decide o destino do segmento depois da checagem.
type Rules struct {
	MaxFactDropRatio float64
	MinLines         int
	ClosingLine      string // se definido, tem de sobreviver como última fala
}

// Decide devolve approved|rejected e o motivo.
func Decide(outs []Outcome, r Rules) (string, string) {
	facts, factDropped, remaining := 0, 0, 0
	lastKept := ""
	for _, o := range outs {
		if o.Original.Type == TypeFact {
			facts++
			if o.Status == "dropped" {
				factDropped++
			}
		}
		if o.Status != "dropped" {
			remaining++
			lastKept = o.Final.Text
		}
	}
	if facts > 0 && float64(factDropped)/float64(facts) > r.MaxFactDropRatio {
		return "rejected", fmt.Sprintf("%d de %d falas fact cortadas (%.0f%% > %.0f%%)", factDropped, facts, 100*float64(factDropped)/float64(facts), 100*r.MaxFactDropRatio)
	}
	if remaining < r.MinLines {
		return "rejected", fmt.Sprintf("restaram %d falas (mínimo %d)", remaining, r.MinLines)
	}
	if r.ClosingLine != "" && !textutil.ContainsPhrase(lastKept, r.ClosingLine) {
		return "rejected", fmt.Sprintf("o bloco não termina com %q", r.ClosingLine)
	}
	return "approved", ""
}
