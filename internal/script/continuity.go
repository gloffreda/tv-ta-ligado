package script

import (
	"context"
	"fmt"
	"strings"

	"github.com/gloffreda/tv-ta-ligado/internal/check"
	"github.com/gloffreda/tv-ta-ligado/internal/llm"
	"github.com/gloffreda/tv-ta-ligado/internal/textutil"
)

// Continuity é o passe pós-checagem: só pode REMOVER ou ENCURTAR falas de
// transição (banter) que ficaram órfãs depois dos cortes. Nunca acrescenta.
type Continuity struct {
	LLM   llm.Client
	Model string
}

// Edit é uma decisão sobre uma fala (índice no roteiro, a partir de 1).
type Edit struct {
	Seq    int    `json:"seq"`
	Action string `json:"action"` // remove | shorten
	Text   string `json:"text"`   // texto encurtado (shorten)
}

var continuitySchema = llm.MustSchema("continuity.json", `{
  "type": "object",
  "required": ["edits"],
  "properties": {
    "edits": {"type": "array", "items": {
      "type": "object", "required": ["seq", "action"],
      "properties": {
        "seq": {"type": "integer"},
        "action": {"enum": ["remove", "shorten"]},
        "text": {"type": "string"}
      }}}
  }
}`)

const continuitySystem = `Você é o editor de continuidade de um telejornal. Algumas falas foram cortadas pela checagem de fatos, e certas falas de transição podem ter ficado órfãs (por exemplo: "Orlando, conta o resto" sem continuação, uma resposta a uma piada que saiu, uma chamada para algo que não vem).

Você SÓ pode:
- "remove": remover uma fala banter órfã;
- "shorten": encurtar uma fala banter APAGANDO palavras (o texto novo deve ser um trecho do original, na mesma ordem, sem nenhuma palavra nova).
Nunca acrescente conteúdo, nunca mexa em falas fact, nunca mexa na última fala. Se nada estiver órfão, devolva edits vazio.

Responda apenas com JSON: {"edits":[{"seq":3,"action":"remove"},{"seq":7,"action":"shorten","text":"..."}]}`

// Kept é uma fala que sobreviveu à checagem.
type Kept struct {
	Seq  int
	Line check.Line
}

func (c *Continuity) Propose(ctx context.Context, kept []Kept, dropped []Kept) ([]Edit, error) {
	var b strings.Builder
	b.WriteString("Roteiro depois dos cortes (seq, quem fala, tipo, texto):\n")
	for _, k := range kept {
		fmt.Fprintf(&b, "%d %s (%s): %s\n", k.Seq, k.Line.Speaker, k.Line.Type, k.Line.Text)
	}
	b.WriteString("\nFalas que foram cortadas (não estão mais no ar):\n")
	for _, d := range dropped {
		fmt.Fprintf(&b, "%d %s (%s): %s\n", d.Seq, d.Line.Speaker, d.Line.Type, d.Line.Text)
	}
	resp, err := c.LLM.Complete(ctx, llm.Request{Purpose: "continuity", Model: c.Model, System: continuitySystem, Prompt: b.String(), MaxTokens: 1000, Temperature: llm.Float(0)})
	if err != nil {
		return nil, err
	}
	var out struct {
		Edits []Edit `json:"edits"`
	}
	if err := continuitySchema.Decode(resp.Text, &out); err != nil {
		return nil, err
	}
	return out.Edits, nil
}

// IsShortening: short é original com palavras apagadas (mesma ordem, nenhuma
// palavra nova) e é estritamente menor. Pontuação e caixa são ignoradas.
func IsShortening(original, short string) bool {
	o := strings.Fields(textutil.Normalize(original))
	s := strings.Fields(textutil.Normalize(short))
	if len(s) == 0 || len(s) >= len(o) {
		return false
	}
	j := 0
	for _, w := range o {
		if j < len(s) && w == s[j] {
			j++
		}
	}
	return j == len(s)
}
