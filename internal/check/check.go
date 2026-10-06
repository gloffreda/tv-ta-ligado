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
	Sensitive        bool // modo sério: segmento com morte, violência, desastre ou doença
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
			refNames = append(refNames, f.SourceName) // "segundo a Folha de S.Paulo"
		}
	case TypeBanter:
	default:
		fail("tipo de fala desconhecido: %q", l.Type)
	}

	// Números. Códigos alfanuméricos da allowlist (4G, 5G, B3) não são número.
	nums := brnum.Extract(lex.MaskCodes(text))
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

	// Banter não fala de pessoa real nem pelo cargo ("o ministro", "o técnico
	// do time"): piada com cargo é piada com pessoa real.
	if l.Type == TypeBanter {
		if w := roleMention(text); w != "" {
			fail("banter cita pessoa real pelo cargo ou função: %q (piada só com a situação ou com os avatares)", w)
		}
	}

	// Uma fala, um tipo: a primeira frase de uma fala fact tem de ser o fato.
	if l.Type == TypeFact && len(refs) > 0 {
		if first, ok := firstContentSentence(text); ok && !sentenceHasFact(first, refs, lex) {
			fail("fala fact mistura comentário e fato (a primeira frase não traz o fato): separe em uma fala banter e outra fact")
		}
	}
	// Modo sério: segmento com fato sensível não tem piada (o juiz confere o tom).
	res := StageResult{Stage: StageDeterministic, Passed: len(reasons) == 0, Reasons: reasons}
	res.Detail = map[string]any{"numbers": rawNumbers(nums), "names": nf.All()}
	return res
}

// Cargos e funções que identificam uma pessoa real (forma normalizada, sem acento).
var roleWords = map[string]bool{
	"presidente": true, "presidenta": true, "vice-presidente": true, "ministro": true, "ministra": true, "prefeito": true, "prefeita": true,
	"governador": true, "governadora": true, "deputado": true, "deputada": true, "senador": true, "senadora": true, "vereador": true,
	"vereadora": true, "secretario": true, "juiz": true, "juiza": true, "desembargador": true, "desembargadora": true,
	"procurador": true, "procuradora": true, "delegado": true, "delegada": true, "tecnico": true, "treinador": true,
	"treinadora": true, "jogador": true, "jogadora": true, "goleiro": true, "atacante": true, "zagueiro": true, "ceo": true,
	"diretor": true, "diretora": true, "empresario": true, "empresaria": true, "porta-voz": true, "papa": true, "rei": true, "rainha": true,
	"candidato": true, "candidata": true, "lider": true, "comandante": true, "general": true, "cantor": true, "cantora": true,
	"ator": true, "atriz": true, "influenciador": true, "influenciadora": true, "chanceler": true, "premie": true, "embaixador": true,
	"embaixadora": true, "reitor": true, "reitora": true, "bilionario": true, "bilionaria": true, "magnata": true,
}

// roleMention devolve o primeiro cargo citado na fala ("" se nenhum).
// ("secretária" e "técnica" ficam de fora: sem acento viram órgão e adjetivo.)
// Palavras ambíguas (general, líder, papa, rei) só contam depois de artigo.
func roleMention(text string) string {
	words := strings.Fields(textutil.Normalize(text))
	for i, w := range words {
		w = strings.Trim(w, ".,;:!?()\"'“”")
		if !roleWords[w] {
			continue
		}
		if w == "general" || w == "lider" || w == "papa" || w == "rei" {
			if i == 0 {
				continue
			}
			switch strings.Trim(words[i-1], ".,;:!?") {
			case "o", "a", "do", "da", "pro", "pra", "ao", "esse", "essa", "aquele", "aquela", "nosso", "nossa", "seu", "sua":
			default:
				continue
			}
		}
		return w
	}
	return ""
}

// Aberturas curtas que podem vir antes do fato numa fala fact.
var openers = []string{"boa noite", "bom dia", "boa tarde", "agora", "outra noticia", "mudando de assunto", "e mais", "seguindo",
	"vamos a economia", "vamos ao tempo", "traduzindo", "no esporte", "na economia", "na politica", "atencao", "enquanto isso"}

// firstContentSentence: a primeira frase que não é só abertura ("Boa noite.").
func firstContentSentence(text string) (string, bool) {
	for _, s := range splitSentences(text) {
		n := textutil.Normalize(s)
		if n == "" {
			continue
		}
		short := len(strings.Fields(n)) <= 4
		isOpener := false
		for _, o := range openers {
			if n == o || strings.HasPrefix(n, o+" ") && short {
				isOpener = true
				break
			}
		}
		if !isOpener {
			return s, true
		}
	}
	return "", false
}

func splitSentences(t string) []string {
	var out []string
	start := 0
	rs := []rune(t)
	for i, r := range rs {
		if (r == '.' || r == '!' || r == '?') && (i+1 == len(rs) || rs[i+1] == ' ') {
			out = append(out, strings.TrimSpace(string(rs[start:i+1])))
			start = i + 1
		}
	}
	if rest := strings.TrimSpace(string(rs[start:])); rest != "" {
		out = append(out, rest)
	}
	return out
}

var stop = map[string]bool{"para": true, "pela": true, "pelo": true, "como": true, "mais": true, "isso": true, "esta": true, "este": true,
	"essa": true, "esse": true, "sobre": true, "entre": true, "segundo": true, "ainda": true, "agora": true, "voce": true, "duda": true, "orlando": true,
	"gloria": true, "cadeira": true, "quando": true, "onde": true, "porque": true, "muito": true, "pois": true, "tambem": true, "hoje": true}

// sentenceHasFact: a frase traz número, entidade ou ≥ 2 palavras de conteúdo dos fatos citados.
func sentenceHasFact(sent string, refs []facts.Fact, lex *Lexicon) bool {
	if len(brnum.Extract(lex.MaskCodes(sent))) > 0 {
		return true
	}
	words := map[string]bool{}
	for _, f := range refs {
		for _, e := range f.Entities {
			if MentionsEntity(sent, e.Name) {
				return true
			}
		}
		for _, w := range strings.Fields(textutil.Normalize(f.Claim)) {
			if len(w) >= 4 && !stop[w] {
				words[w] = true
			}
		}
	}
	hits := 0
	for _, w := range strings.Fields(textutil.Normalize(sent)) {
		if words[w] {
			hits++
			delete(words, w)
		}
	}
	return hits >= 2
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
	IsJoke          bool   `json:"is_joke"`
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
    "is_joke": {"type": "boolean"},
    "real_person_mocked": {"type": "boolean"}
  }
}`)

const judgeFactSystem = `Você é o checador de fatos de um telejornal brasileiro apresentado por avatares fictícios (Orlando Pimenta, Duda Faísca e Glória Garoa, a moça do tempo). Sua função é impedir que qualquer afirmação sem fonte vá ao ar. Seja rigoroso: na dúvida, reprove.

Você recebe UMA fala do tipo "fact" e o texto dos fatos que a sustentam.
- entailed=true somente se TODA afirmação factual da fala estiver sustentada pelos fatos (paráfrase é permitida; arredondamento correto é permitido; definições de glossário valem como fato).
- Causas, consequências, previsões, comparações, contexto, adjetivos que impliquem fatos novos, ou qualquer detalhe ausente dos fatos tornam entailed=false. Liste esses trechos em "unsupported".
- Saudações e conectivos ("Boa noite.", "Outra notícia:") não são afirmações factuais.
- Paráfrase sem informação nova é permitida: "segue em", "continua em" ou "está em" para um valor vigente; explicar uma definição do glossário com palavras do dia a dia, sem acrescentar fato verificável.
- Mas qualquer detalhe que mude ou acrescente fato (quem, quanto, quando, onde, sinal, unidade, período, cargo, causa, "recorde", "primeira vez") reprova.
- real_person_mocked=true se a fala ridiculariza, zomba ou faz piada com uma pessoa real ou com um grupo real de pessoas.

Exemplos (fatos → fala → veredito):
1. Fato: "O dólar comercial (venda) ficou em R$ 5,2079 em 02/10/2026, segundo o Banco Central." Fala: "O dólar fechou a R$ 5,21, segundo o Banco Central." → entailed=true (arredondamento correto).
2. Mesmo fato. Fala: "O dólar caiu para R$ 5,21 por causa da Selic." → entailed=false, unsupported=["caiu", "por causa da Selic"].
3. Fato: "A meta da taxa Selic está em 13,75% ao ano." Fala: "A Selic segue em 13,75% ao ano." → entailed=true (paráfrase sem fato novo).
4. Mesmo fato. Fala: "A Selic está em 13,75% ao mês." → entailed=false, unsupported=["ao mês"].
5. Fato: "O ministro Caio Brandão afirmou que o programa entregou 12 mil moradias." Fala: "O governador Caio Brandão afirmou..." → entailed=false, unsupported=["governador"].
6. Fato: "Felipe Massa e Pipo Massa venceram juntos." Fala: "Pai e filho, Felipe e Pipo Massa venceram juntos." → entailed=false, unsupported=["Pai e filho"].
7. Fato: "O festival recebeu 85 mil visitantes." Fala: "O festival bateu recorde com 85 mil visitantes." → entailed=false, unsupported=["bateu recorde"].
8. Fato de glossário: "A taxa Selic é a taxa básica de juros da economia, que influencia outras taxas." Fala: "Traduzindo: a Selic é a taxa básica de juros e mexe com os outros juros." → entailed=true.
9. Fato: "A prefeita inaugurou a ponte." Fala: "A prefeita, que nem sabe cortar fita, inaugurou a ponte." → entailed=false, real_person_mocked=true.

Responda apenas com JSON: {"entailed": true|false, "unsupported": ["trecho", ...], "real_person_mocked": true|false}`

const judgeBanterSystem = `Você revisa falas de "banter" (comentário, reação, transição, piada) de um telejornal apresentado por avatares fictícios (Orlando Pimenta, Duda Faísca e Glória Garoa, a moça do tempo).

PERMITIDO: opinião, piada, exagero óbvio, reação emocional, comentário sobre os próprios avatares e sobre o estúdio (a rixa pela cadeira, "a máquina", a idade do Orlando, as gírias da Duda), memórias e bordões dos avatares, chamadas para a próxima notícia, e repetir em outras palavras o que os fatos do segmento já dizem.

PROIBIDO:
1. Afirmação factual NOVA sobre o mundo real: algo verificável que não está nos fatos do segmento (quem, o quê, quando, onde, quanto, causa, consequência, parentesco, recorde...).
2. Zombar de pessoa real ou de grupo real de pessoas. Pessoa identificada só pelo cargo ou função ("o ministro", "a prefeita", "o técnico do time", "o presidente do Banco Central") é pessoa real: real_person_mocked=true se a fala ironiza, debocha ou faz piada com ela.

is_joke=true se a fala tem tom de piada, ironia, deboche ou brincadeira (mesmo leve); false se é neutra ou séria.

Responda apenas com JSON: {"new_factual_claim": true|false, "claim": "o trecho da afirmação nova, ou vazio", "is_joke": true|false, "real_person_mocked": true|false}`

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
		MaxTokens: 4000, Temperature: llm.Float(0), Effort: "low", CacheSystem: l.Type != TypeBanter,
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
		detail["new_factual_claim"], detail["claim"], detail["is_joke"] = jr.NewFactualClaim, jr.Claim, jr.IsJoke
		if jr.NewFactualClaim {
			reasons = append(reasons, "juiz: afirmação factual nova no banter: "+jr.Claim)
		}
		if f.Env.Sensitive && jr.IsJoke {
			reasons = append(reasons, "modo sério: piada em segmento com notícia sensível (morte, violência, desastre ou doença)")
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
