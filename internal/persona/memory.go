// Package persona carrega as personas (YAML) e cuida da memória dos avatares.
package persona

import (
	"context"
	"fmt"
	"strings"

	"github.com/gloffreda/tv-ta-ligado/internal/check"
	"github.com/gloffreda/tv-ta-ligado/internal/config"
	"github.com/gloffreda/tv-ta-ligado/internal/facts"
	"github.com/gloffreda/tv-ta-ligado/internal/llm"
	"github.com/gloffreda/tv-ta-ligado/internal/store"
)

// Load relê os YAML a cada uso: mudar a persona não exige recompilar.
func Load(dir string) (map[string]config.Persona, error) { return config.LoadPersonas(dir) }

var memorySchema = llm.MustSchema("memory.json", `{
  "type": "object",
  "required": ["memories"],
  "properties": {
    "memories": {
      "type": "array", "maxItems": 3,
      "items": {
        "type": "object",
        "required": ["persona", "kind", "content"],
        "properties": {
          "persona": {"type": "string"},
          "kind": {"enum": ["feud", "joke", "opinion", "running_gag"]},
          "content": {"type": "string", "minLength": 5, "maxLength": 300}
        }
      }
    }
  }
}`)

const memorySystem = `Você mantém a memória dos avatares de um telejornal: Orlando Pimenta, Duda Faísca e Glória Garoa (a moça do tempo).
Do roteiro aprovado, extraia até 3 memórias sobre a relação entre os avatares: rixa (feud), piada interna (joke), opinião de um sobre o outro (opinion) ou bordão recorrente (running_gag).
NUNCA registre memórias sobre pessoas reais, números, datas ou política. Lugares e instituições conhecidas (São Paulo, Banco Central) podem aparecer como contexto. O foco é a relação entre os avatares.
Responda apenas com JSON: {"memories":[{"persona":"orlando|duda|gloria","kind":"feud|joke|opinion|running_gag","content":"..."}]}`

type Extractor struct {
	LLM   llm.Client
	Model string
	Lex   *check.Lexicon
}

// Extract devolve as memórias válidas e os motivos das descartadas.
func (e *Extractor) Extract(ctx context.Context, lines []check.Line, personas map[string]config.Persona, knownEntities []facts.Entity) ([]store.Memory, []string, error) {
	var b strings.Builder
	b.WriteString("Roteiro aprovado:\n")
	for _, l := range lines {
		fmt.Fprintf(&b, "%s (%s): %s\n", l.Speaker, l.Type, l.Text)
	}
	resp, err := e.LLM.Complete(ctx, llm.Request{Purpose: "memory", Model: e.Model, System: memorySystem, Prompt: b.String(), MaxTokens: 800, Temperature: llm.Float(0)})
	if err != nil {
		return nil, nil, err
	}
	var out struct {
		Memories []store.Memory `json:"memories"`
	}
	if err := memorySchema.Decode(resp.Text, &out); err != nil {
		return nil, nil, err
	}
	var kept []store.Memory
	var dropped []string
	for _, m := range out.Memories {
		if reason := e.reject(m, personas, knownEntities); reason != "" {
			dropped = append(dropped, fmt.Sprintf("%q: %s", m.Content, reason))
			continue
		}
		m.Weight = 1.0
		kept = append(kept, m)
	}
	return kept, dropped, nil
}

// reject: pessoas nunca; lugares e organizações só os da allowlist.
func (e *Extractor) reject(m store.Memory, personas map[string]config.Persona, known []facts.Entity) string {
	if _, ok := personas[m.Persona]; !ok {
		return "persona desconhecida"
	}
	nf := e.Lex.Analyze(m.Content, known, nil)
	if !nf.Empty() {
		return "cita nome fora da allowlist: " + strings.Join(nf.All(), ", ")
	}
	return ""
}
