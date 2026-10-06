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
	SegmentFacts     []facts.Fact         // fatos da pauta (contexto do banter)
	KnownEntities    []facts.Entity       // entidades (tipadas) de todos os fatos do banco
	Lex              *Lexicon
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

// BanterCover: organizações e lugares dos fatos do segmento podem ser citados
// em banter (pessoas, nunca).
func (env *Env) BanterCover() []string {
	var out []string
	for _, f := range env.SegmentFacts {
		for _, e := range f.Entities {
			if e.Type != facts.Person {
				out = append(out, e.Name)
			}
		}
	}
	return out
}

func nameReasons(prefix string, nf NameFindings) []string {
	var r []string
	for _, n := range nf.Persons {
		r = append(r, fmt.Sprintf("%s cita pessoa real: %q", prefix, n))
	}
	for _, n := range nf.FirstNames {
		r = append(r, fmt.Sprintf("%s com prenome de pessoa: %q", prefix, n))
	}
	for _, n := range nf.Runs {
		r = append(r, fmt.Sprintf("%s com nome próprio fora da allowlist: %q", prefix, n))
	}
	for _, n := range nf.Others {
		r = append(r, fmt.Sprintf("%s com organização/lugar fora da allowlist: %q", prefix, n))
	}
	return r
}

// Deterministic aplica as regras de código a uma fala.
func Deterministic(l Line, env *Env) StageResult {
	var reasons []string
	fail := func(format string, a ...any) { reasons = append(reasons, fmt.Sprintf(format, a...)) }
	lex := env.Lex
	if lex == nil {
		lex = NewLexicon(nil, nil)
	}

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
	var refNames []string
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
			refNames = append(refNames, facts.Names(f.Entities)...)
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

	// Nomes.
	var nf NameFindings
	switch {
	case l.Type == TypeBanter:
		nf = lex.Analyze(text, env.KnownEntities, env.BanterCover())
		reasons = append(reasons, nameReasons("banter", nf)...)
	case len(refs) > 0:
		nf = lex.Analyze(text, env.KnownEntities, refNames)
		for _, r := range nameReasons("fala fact", nf) {
			reasons = append(reasons, r+" (não está em entities dos fatos referenciados)")
		}
	}

	res := StageResult{Stage: StageDeterministic, Passed: len(reasons) == 0, Reasons: reasons}
	res.Detail = map[string]any{"numbers": rawNumbers(nums), "names": nf.All()}
	return res
}

func rawNumbers(ns []brnum.Number) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = n.Raw
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
			if f.Value != nil && n.Matches(*f.Value) && brnum.Compatible(n.Kind, brnum.UnitKind(f.Unit)) {
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
				if p.HasValue && n.Matches(p.Value) && brnum.Compatible(n.Kind, p.Kind) {
					return true
				}
			}
		}
	}
	return false
}

// ---- Estágio 2: juiz ----

// JudgeResult unifica os dois modos do juiz.
type JudgeResult struct {
	// Modo fact.
	Entailed    bool     `json:"entailed"`
	Unsupported []string `json:"unsupported"`
	// Modo banter.
	NewFactualClaim bool   `json:"new_factual_claim"`
	Claim           string `json:"claim"`
	// Ambos.
	RealPersonMocked bool `json:"real_person_mocked"`
}

var judgeFactSchema = llm.MustSchema("judge_fact.json", `{
  "type": "object",
  "required": ["entailed", "unsupported", "real_person_mocked"],
  "properties": {
    "entailed": {"type": "boolean"},
    "unsupported": {"type": "array", "items": {"type": "string"}},
    "real_person_mocked": {"type": "boolean"}
  }
}`)

var judgeBanterSchema = llm.MustSchema("judge_banter.json", `{
  "type": "object",
  "required": ["new_factual_claim", "claim", "real_person_mocked"],
  "properties": {
    "new_factual_claim": {"type": "boolean"},
    "claim": {"type": "string"},
    "real_person_mocked": {"type": "boolean"}
  }
}`)

const judgeFactSystem = `Você é o checador de fatos de um telejornal brasileiro apresentado por dois avatares fictícios, Orlando Pimenta e Duda Faísca. Sua função é impedir que qualquer afirmação sem fonte vá ao ar. Seja rigoroso: na dúvida, reprove.

Você recebe UMA fala do tipo "fact" e o texto dos fatos que a sustentam.
- entailed=true somente se TODA afirmação factual da fala estiver sustentada pelos fatos (paráfrase é permitida; arredondamento correto é permitido; definições de glossário valem como fato).
- Causas, consequências, previsões, comparações, contexto, adjetivos que impliquem fatos novos, ou qualquer detalhe ausente dos fatos tornam entailed=false. Liste esses trechos em "unsupported".
- Saudações e conectivos ("Boa noite.", "Outra notícia:") não são afirmações factuais.
- Paráfrase sem informação nova é permitida: "segue em", "continua em" ou "está em" para um valor vigente; explicar uma definição do glossário com palavras do dia a dia, sem acrescentar fato verificável.
- Mas qualquer detalhe que mude ou acrescente fato (quem, quanto, quando, onde, sinal, unidade, período, cargo, causa, "recorde", "primeira vez") reprova.
- real_person_mocked=true se a fala ridiculariza, zomba ou faz piada com uma pessoa real ou com um grupo real de pessoas.

Responda apenas com JSON: {"entailed": true|false, "unsupported": ["trecho", ...], "real_person_mocked": true|false}`

const judgeBanterSystem = `Você revisa falas de "banter" (comentário, reação, transição, piada) de um telejornal apresentado por dois avatares fictícios, Orlando Pimenta e Duda Faísca.

PERMITIDO: opinião, piada, exagero óbvio, reação emocional, comentário sobre os próprios avatares e sobre o estúdio (a rixa pela cadeira, "a máquina", a idade do Orlando, as gírias da Duda), memórias e bordões dos avatares, chamadas para a próxima notícia, e repetir em outras palavras o que os fatos do segmento já dizem.

PROIBIDO:
1. Afirmação factual NOVA sobre o mundo real: algo verificável que não está nos fatos do segmento (quem, o quê, quando, onde, quanto, causa, consequência, parentesco, recorde...).
2. Zombar de pessoa real ou de grupo real de pessoas.

Responda apenas com JSON: {"new_factual_claim": true|false, "claim": "o trecho da afirmação nova, ou vazio", "real_person_mocked": true|false}`

type Judge struct {
	LLM         llm.Client
	Model       string
	BanterModel string // modelo do modo banter; vazio = Model
}

// Evaluate escolhe o modo pelo tipo da fala. refs: fatos citados (fact) ou
// fatos do segmento (banter, como contexto do que já foi dito).
func (j *Judge) Evaluate(ctx context.Context, l Line, refs []facts.Fact) (JudgeResult, error) {
	var b strings.Builder
	system, schema := judgeFactSystem, judgeFactSchema
	if l.Type == TypeBanter {
		system, schema = judgeBanterSystem, judgeBanterSchema
		fmt.Fprintf(&b, "Fala (banter): %q\n\nFatos do segmento (já ditos no ar):\n", l.Text)
	} else {
		fmt.Fprintf(&b, "Fala (fact): %q\n\nFatos referenciados:\n", l.Text)
	}
	if len(refs) == 0 {
		b.WriteString("(nenhum)\n")
	}
	for _, f := range refs {
		fmt.Fprintf(&b, "- [%d] %s (fonte: %s)\n", f.ID, f.Claim, f.SourceName)
	}
	model := j.Model
	if l.Type == TypeBanter && j.BanterModel != "" {
		model = j.BanterModel
	}
	resp, err := j.LLM.Complete(ctx, llm.Request{
		Purpose: "judge", Model: model, System: system, Prompt: b.String(),
		MaxTokens: 4000, Temperature: llm.Float(0), Effort: "low",
	})
	if err != nil {
		return JudgeResult{}, err
	}
	var r JudgeResult
	if err := schema.Decode(resp.Text, &r); err != nil {
		return JudgeResult{}, err
	}
	return r, nil
}

// ---- Fluxo por fala ----

// MaxRewrites: no máximo duas reescritas por fala.
const MaxRewrites = 2

// Rewriter reescreve uma fala reprovada. original é a fala do roteiro; last,
// a última tentativa; reasons, por que last reprovou.
type Rewriter interface {
	Rewrite(ctx context.Context, original, last Line, reasons []string) (Line, error)
}

type Attempt struct {
	N       int
	Line    Line
	Results []StageResult
}

type Outcome struct {
	Original Line
	Final    Line
	Status   string // ok | rewritten | dropped | removed (continuidade)
	Reason   string
	Attempts []Attempt
}

type Flow struct {
	Env      *Env
	Judge    *Judge
	Rewriter Rewriter
}

func (f *Flow) judgeRefs(l Line) []facts.Fact {
	if l.Type == TypeBanter {
		return f.Env.SegmentFacts
	}
	var refs []facts.Fact
	for _, id := range l.FactIDs {
		if fa, ok := f.Env.Facts[id]; ok {
			refs = append(refs, fa)
		}
	}
	return refs
}

func (f *Flow) judge(ctx context.Context, l Line) (StageResult, error) {
	jr, err := f.Judge.Evaluate(ctx, l, f.judgeRefs(l))
	if err != nil {
		if llm.Fatal(err) {
			return StageResult{}, err
		}
		reason := "juiz indisponível ou resposta inválida: " + err.Error()
		return StageResult{Stage: StageJudge, Passed: false, Reasons: []string{reason}}, nil
	}
	var reasons []string
	detail := map[string]any{"real_person_mocked": jr.RealPersonMocked}
	if l.Type == TypeBanter {
		detail["new_factual_claim"], detail["claim"] = jr.NewFactualClaim, jr.Claim
		if jr.NewFactualClaim {
			reasons = append(reasons, "juiz: afirmação factual nova no banter: "+jr.Claim)
		}
	} else {
		detail["entailed"], detail["unsupported"] = jr.Entailed, jr.Unsupported
		if !jr.Entailed {
			r := "juiz: afirmação sem sustentação nos fatos"
			if len(jr.Unsupported) > 0 {
				r += ": " + strings.Join(jr.Unsupported, " | ")
			}
			reasons = append(reasons, r)
		}
	}
	if jr.RealPersonMocked {
		reasons = append(reasons, "juiz: piada/zombaria com pessoa real")
	}
	return StageResult{Stage: StageJudge, Passed: len(reasons) == 0, Reasons: reasons, Detail: detail}, nil
}

// CheckLine: estágio 1 + juiz. Reprovou: até 2 reescritas, cada uma checada
// primeiro pelo estágio determinístico (sem custo de juiz); a que passar vai
// ao juiz uma vez. Reprovou de novo, a fala é cortada.
func (f *Flow) CheckLine(ctx context.Context, l Line) (Outcome, error) {
	out := Outcome{Original: l}
	det := Deterministic(l, f.Env)
	att := Attempt{N: 1, Line: l, Results: []StageResult{det}}
	reasons := det.Reasons
	if det.Passed {
		jr, err := f.judge(ctx, l)
		if err != nil {
			out.Attempts = append(out.Attempts, att)
			return out, err
		}
		att.Results = append(att.Results, jr)
		reasons = jr.Reasons
		if jr.Passed {
			out.Attempts = append(out.Attempts, att)
			out.Final, out.Status = l, "ok"
			return out, nil
		}
	}
	out.Attempts = append(out.Attempts, att)
	history := []string{"1ª tentativa: " + strings.Join(reasons, "; ")}

	last := l
	for n := 1; n <= MaxRewrites; n++ {
		rw, err := f.Rewriter.Rewrite(ctx, l, last, reasons)
		if err != nil {
			if llm.Fatal(err) {
				return out, err
			}
			history = append(history, "reescrita falhou: "+err.Error())
			break
		}
		rw.Speaker, rw.Type = l.Speaker, l.Type // a reescrita mantém quem fala e o tipo
		det := Deterministic(rw, f.Env)
		att := Attempt{N: n + 1, Line: rw, Results: []StageResult{det}}
		if !det.Passed {
			out.Attempts = append(out.Attempts, att)
			history = append(history, fmt.Sprintf("%dª tentativa: %s", n+1, strings.Join(det.Reasons, "; ")))
			last, reasons = rw, det.Reasons
			continue
		}
		jr, err := f.judge(ctx, rw)
		if err != nil {
			out.Attempts = append(out.Attempts, att)
			return out, err
		}
		att.Results = append(att.Results, jr)
		out.Attempts = append(out.Attempts, att)
		if jr.Passed {
			out.Final, out.Status = rw, "rewritten"
			return out, nil
		}
		history = append(history, fmt.Sprintf("%dª tentativa: %s", n+1, strings.Join(jr.Reasons, "; ")))
		last = rw
		break // o juiz só reavalia uma reescrita
	}
	out.Final, out.Status = last, "dropped"
	out.Reason = strings.Join(history, " | ")
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

// Decide devolve approved|rejected e o motivo. Falas removidas pelo passe de
// continuidade não contam como corte, mas saem da contagem de falas.
func Decide(outs []Outcome, r Rules) (string, string) {
	factLines, factDropped, remaining := 0, 0, 0
	lastKept := ""
	for _, o := range outs {
		if o.Original.Type == TypeFact {
			factLines++
			if o.Status == "dropped" {
				factDropped++
			}
		}
		if o.Status != "dropped" && o.Status != "removed" {
			remaining++
			lastKept = o.Final.Text
		}
	}
	if factLines > 0 && float64(factDropped)/float64(factLines) > r.MaxFactDropRatio {
		return "rejected", fmt.Sprintf("%d de %d falas fact cortadas (%.0f%% > %.0f%%)", factDropped, factLines, 100*float64(factDropped)/float64(factLines), 100*r.MaxFactDropRatio)
	}
	if remaining < r.MinLines {
		return "rejected", fmt.Sprintf("restaram %d falas (mínimo %d)", remaining, r.MinLines)
	}
	if r.ClosingLine != "" && !textutil.ContainsPhrase(lastKept, r.ClosingLine) {
		return "rejected", fmt.Sprintf("o bloco não termina com %q", r.ClosingLine)
	}
	return "approved", ""
}
