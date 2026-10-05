// Package script é o roteirista: transforma a pauta em falas no contrato JSON
// e reescreve falas reprovadas pela checagem.
package script

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/gloffreda/tv-ta-ligado/internal/check"
	"github.com/gloffreda/tv-ta-ligado/internal/config"
	"github.com/gloffreda/tv-ta-ligado/internal/facts"
	"github.com/gloffreda/tv-ta-ligado/internal/llm"
	"github.com/gloffreda/tv-ta-ligado/internal/store"
	"github.com/gloffreda/tv-ta-ligado/internal/textutil"
)

// Script é o contrato de saída do roteirista.
type Script struct {
	Block string       `json:"block"`
	Lines []check.Line `json:"lines"`
}

// ContractSchema gera o JSON Schema do contrato para as personas e limites dados.
func ContractSchema(speakers []string, minLines, maxLines int) string {
	sp, _ := json.Marshal(speakers)
	return fmt.Sprintf(`{
  "type": "object",
  "required": ["block", "lines"],
  "additionalProperties": false,
  "properties": {
    "block": {"type": "string"},
    "lines": {
      "type": "array", "minItems": %d, "maxItems": %d,
      "items": {
        "type": "object",
        "required": ["speaker", "type", "text", "fact_ids"],
        "additionalProperties": false,
        "properties": {
          "speaker": {"enum": %s},
          "type": {"enum": ["fact", "banter"]},
          "text": {"type": "string", "minLength": 1, "maxLength": 600},
          "fact_ids": {"type": "array", "items": {"type": "integer"}}
        }
      }
    }
  }
}`, minLines, maxLines, sp)
}

type Input struct {
	Block    config.Block
	Facts    []facts.Fact
	Memories []store.Memory
}

type Writer struct {
	LLM      llm.Client
	Model    string
	Schedule config.Schedule
	Personas map[string]config.Persona
}

func (w *Writer) speakers() []string {
	var ids []string
	for id := range w.Personas {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Validate aplica, além do schema, as regras do contrato que dependem de código.
// fs: fatos da pauta (para a regra do humor).
func (w *Writer) Validate(s *Script, b config.Block, fs []facts.Fact) error {
	if s.Block != b.Name {
		return fmt.Errorf("block %q, esperado %q", s.Block, b.Name)
	}
	seg := w.Schedule.Segment
	if b.ClosingLine != "" {
		last := s.Lines[len(s.Lines)-1]
		if !textutil.ContainsPhrase(last.Text, b.ClosingLine) {
			if len(s.Lines) >= seg.MaxLines {
				return fmt.Errorf("a última fala precisa ser %q", b.ClosingLine)
			}
			s.Lines = append(s.Lines, check.Line{Speaker: b.Lead, Type: check.TypeBanter, Text: b.ClosingLine, FactIDs: []int64{}})
		}
	}
	byID := map[int64]facts.Fact{}
	for _, f := range fs {
		byID[f.ID] = f
	}
	words := 0
	for i, l := range s.Lines {
		if b.Name == "humor" && l.Type == check.TypeFact && l.Speaker != "orlando" {
			for _, id := range l.FactIDs {
				if f, ok := byID[id]; ok && facts.HasPerson([]facts.Fact{f}) {
					return fmt.Errorf("fala %d: no humor, fato que cita pessoa é lido pelo Orlando, literalmente", i+1)
				}
			}
		}
		if l.Type == check.TypeFact && len(l.FactIDs) == 0 {
			return fmt.Errorf("fala %d é fact e não tem fact_ids", i+1)
		}
		if l.Type == check.TypeBanter && len(l.FactIDs) > 0 {
			return fmt.Errorf("fala %d é banter e não pode ter fact_ids", i+1)
		}
		words += textutil.WordCount(l.Text)
	}
	secs := words * 60 / seg.WordsPerMinute
	if secs < seg.MinSeconds || secs > seg.MaxSeconds {
		return fmt.Errorf("duração estimada de %ds (%d palavras); precisa ficar entre %d e %d s (%d a %d palavras)",
			secs, words, seg.MinSeconds, seg.MaxSeconds, seg.MinSeconds*seg.WordsPerMinute/60, seg.MaxSeconds*seg.WordsPerMinute/60)
	}
	return nil
}

const writerSystem = `Você é o roteirista do TV Tá Ligado, canal de notícias 24/7 apresentado por dois avatares de IA em pixel art. Você escreve falas curtas para serem lidas em voz alta.

REGRA INEGOCIÁVEL: nenhuma fala com fato vai ao ar sem uma fonte que a sustente. Tudo que você afirmar sobre o mundo precisa estar nos FATOS fornecidos.

Tipos de fala:
- "fact": afirma algo do mundo real. Precisa listar em "fact_ids" os ids dos fatos que a sustentam. Use apenas o que está no texto desses fatos: sem causas, consequências, previsões ou contexto extra. Números escritos com algarismos, exatamente como no fato ou arredondados corretamente (5,2079 pode virar 5,21, nunca 5,20). Só cite pessoas, organizações e lugares que estejam em "entities" dos fatos citados.
- "banter": comentário, reação, transição ou piada entre os avatares. "fact_ids" vazio. Proibido em banter: números, datas, valores, nomes de pessoas reais, e qualquer afirmação nova sobre o mundo. Organizações e lugares só se estiverem nos fatos do segmento ou forem nomes comuns (Banco Central, Brasil, São Paulo). Os avatares podem chamar um ao outro pelo nome. Opinião, piada, exagero óbvio e brincadeira sobre os próprios avatares são permitidos.
- GLOSSÁRIO: fatos de kind "glossary" são definições com fonte oficial. Quando a Duda "traduzir" um termo (Selic, IPCA, frente fria...), a fala é do tipo "fact" e cita o fact_id do glossário. Nunca explique um termo em banter.
- Cada fala de transição deve fazer sentido sozinha: não prometa algo que depende da fala seguinte ("conta o resto", "vem aí...").

Nunca zombe de pessoas reais. Nunca dê opinião política ou eleitoral. Nunca recomende investimentos.
Responda apenas com o JSON do contrato, sem texto fora dele.`

func (w *Writer) prompt(in Input) string {
	var b strings.Builder
	seg := w.Schedule.Segment
	fmt.Fprintf(&b, "BLOCO: %s\nQuem conduz: %s. Quem apoia: %s.\n%s\n", in.Block.Name, in.Block.Lead, in.Block.Support, strings.TrimSpace(in.Block.Instructions))
	if in.Block.ClosingLine != "" {
		fmt.Fprintf(&b, "A última fala do bloco deve ser exatamente: %q (tipo banter, dita por %s).\n", in.Block.ClosingLine, in.Block.Lead)
	}
	fmt.Fprintf(&b, "\nTAMANHO: de %d a %d falas, totalizando entre %d e %d palavras (%d a %d segundos a %d palavras por minuto).\n",
		seg.MinLines, seg.MaxLines, seg.MinSeconds*seg.WordsPerMinute/60, seg.MaxSeconds*seg.WordsPerMinute/60, seg.MinSeconds, seg.MaxSeconds, seg.WordsPerMinute)

	b.WriteString("\nPERSONAS (o campo speaker usa o id):\n")
	for _, id := range w.speakers() {
		p := w.Personas[id]
		pj, _ := json.Marshal(p)
		fmt.Fprintf(&b, "- id=%s: %s\n", id, pj)
	}
	b.WriteString("\nMEMÓRIAS DOS AVATARES (podem inspirar o banter):\n")
	if len(in.Memories) == 0 {
		b.WriteString("(nenhuma ainda)\n")
	}
	for _, m := range in.Memories {
		fmt.Fprintf(&b, "- %s (%s): %s\n", m.Persona, m.Kind, m.Content)
	}
	b.WriteString("\nFATOS DA PAUTA (use os ids em fact_ids):\n")
	for _, f := range in.Facts {
		ents, _ := json.Marshal(f.Entities)
		fmt.Fprintf(&b, "[%d] (%s; fonte: %s) %s | entities: %s\n", f.ID, f.Kind, f.SourceName, f.Claim, ents)
	}
	if in.Block.Name == "humor" {
		b.WriteString("\nREGRA DO HUMOR: se um fato tem entity do tipo \"person\", quem fala é o Orlando, lendo o fato literalmente (type fact), e a piada que vem depois é só sobre a situação, nunca sobre a pessoa.\n")
	}
	fmt.Fprintf(&b, "\nCONTRATO (JSON estrito):\n{\"block\":%q,\"lines\":[{\"speaker\":\"orlando\",\"type\":\"fact\",\"text\":\"...\",\"fact_ids\":[123]},{\"speaker\":\"duda\",\"type\":\"banter\",\"text\":\"...\",\"fact_ids\":[]}]}\n", in.Block.Name)
	return b.String()
}

// Write gera o roteiro. JSON inválido: tenta uma vez de novo; falhou outra
// vez, devolve erro (o segmento é rejeitado). attempts conta as chamadas.
func (w *Writer) Write(ctx context.Context, in Input) (Script, int, error) {
	seg := w.Schedule.Segment
	schema := llm.MustSchema("script.json", ContractSchema(w.speakers(), seg.MinLines, seg.MaxLines))
	prompt := w.prompt(in)
	var lastErr error
	for attempt := 1; attempt <= 2; attempt++ {
		p := prompt
		if lastErr != nil {
			p += fmt.Sprintf("\nA resposta anterior foi recusada pelo validador: %v\nCorrija e responda de novo só com o JSON.\n", lastErr)
		}
		resp, err := w.LLM.Complete(ctx, llm.Request{Purpose: "script", Model: w.Model, System: writerSystem, Prompt: p, MaxTokens: 16000, Effort: "medium"})
		if err != nil {
			if llm.Fatal(err) {
				return Script{}, attempt, err
			}
			lastErr = err
			continue
		}
		var s Script
		if err := schema.Decode(resp.Text, &s); err != nil {
			lastErr = err
			continue
		}
		if err := w.Validate(&s, in.Block, in.Facts); err != nil {
			lastErr = err
			continue
		}
		return s, attempt, nil
	}
	return Script{}, 2, fmt.Errorf("roteiro inválido duas vezes: %w", lastErr)
}

// ---- Reescrita ----

var lineSchema = llm.MustSchema("line.json", `{
  "type": "object",
  "required": ["text", "fact_ids"],
  "properties": {
    "text": {"type": "string", "minLength": 1, "maxLength": 600},
    "fact_ids": {"type": "array", "items": {"type": "integer"}}
  }
}`)

// Rewriter implementa check.Rewriter usando os fatos do segmento.
type Rewriter struct {
	LLM   llm.Client
	Model string
	Block config.Block
	Facts []facts.Fact
	Allow []string // allowlist relevante (termos que aparecem no segmento + avatares)
}

const rulesFact = `REGRAS DA FALA "fact":
- Toda afirmação precisa estar no texto dos fatos citados em fact_ids (paráfrase é permitida). Nada de causa, consequência, previsão, comparação ou contexto que não esteja nos fatos.
- Números com algarismos, exatamente como no fato ou arredondados corretamente (5,2079 → 5,21; nunca 5,20). Sem números que não estejam nos fatos citados.
- Só cite pessoas, organizações e lugares que estejam em "entities" dos fatos citados, ou na lista de termos permitidos.
- Pode trocar os fact_ids, desde que sejam ids da lista de fatos.`

const rulesBanter = `REGRAS DA FALA "banter":
- Nenhum número, data ou valor (nem por extenso).
- Nenhum nome de pessoa real, nem prenome. Organizações e lugares só os da lista de termos permitidos.
- Nenhuma afirmação factual nova sobre o mundo: só opinião, reação, piada, exagero óbvio ou brincadeira sobre os próprios avatares.
- Não zombe de pessoa real nem de grupo real.
- fact_ids vazio.`

func (r *Rewriter) Rewrite(ctx context.Context, original, last check.Line, reasons []string) (check.Line, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "Bloco: %s\nFala original (speaker=%s, type=%s, fact_ids=%v):\n%q\n", r.Block.Name, original.Speaker, original.Type, original.FactIDs, original.Text)
	if last.Text != original.Text {
		fmt.Fprintf(&b, "\nÚltima tentativa de reescrita (também reprovada):\n%q\n", last.Text)
	}
	b.WriteString("\nMotivos da reprovação:\n")
	for _, reason := range reasons {
		fmt.Fprintf(&b, "- %s\n", reason)
	}
	if original.Type == check.TypeBanter {
		b.WriteString("\n" + rulesBanter + "\n")
	} else {
		b.WriteString("\n" + rulesFact + "\n")
	}
	b.WriteString("\nPROIBIDO introduzir nomes de pessoas que não estejam na fala original ou nos fatos citados.\n")
	if len(r.Allow) > 0 {
		fmt.Fprintf(&b, "\nTermos permitidos (organizações, lugares, siglas, avatares): %s\n", strings.Join(r.Allow, ", "))
	}
	if original.Type == check.TypeFact {
		b.WriteString("\nFatos disponíveis:\n")
		for _, f := range r.Facts {
			ents, _ := json.Marshal(f.Entities)
			fmt.Fprintf(&b, "[%d] %s | entities: %s\n", f.ID, f.Claim, ents)
		}
	}
	b.WriteString("\nReescreva a fala corrigindo os motivos, mantendo quem fala, o tipo e o tom. Na dúvida, diga menos: uma fala mais curta e certa é melhor. Responda apenas com JSON: {\"text\":\"...\",\"fact_ids\":[...]}")
	resp, err := r.LLM.Complete(ctx, llm.Request{Purpose: "rewrite", Model: r.Model, System: writerSystem, Prompt: b.String(), MaxTokens: 4000, Effort: "low"})
	if err != nil {
		return check.Line{}, err
	}
	var out struct {
		Text    string  `json:"text"`
		FactIDs []int64 `json:"fact_ids"`
	}
	if err := lineSchema.Decode(resp.Text, &out); err != nil {
		return check.Line{}, err
	}
	if original.Type == check.TypeBanter {
		out.FactIDs = []int64{}
	}
	return check.Line{Speaker: original.Speaker, Type: original.Type, Text: out.Text, FactIDs: out.FactIDs}, nil
}
