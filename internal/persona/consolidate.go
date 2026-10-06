package persona

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/gloffreda/tv-ta-ligado/internal/check"
	"github.com/gloffreda/tv-ta-ligado/internal/config"
	"github.com/gloffreda/tv-ta-ligado/internal/facts"
	"github.com/gloffreda/tv-ta-ligado/internal/llm"
	"github.com/gloffreda/tv-ta-ligado/internal/store"
)

var mergeSchema = llm.MustSchema("memory_merge.json", `{
  "type": "object",
  "required": ["groups"],
  "properties": {
    "groups": {
      "type": "array", "maxItems": 20,
      "items": {
        "type": "object",
        "required": ["ids", "kind", "content"],
        "properties": {
          "ids": {"type": "array", "minItems": 2, "items": {"type": "integer"}},
          "kind": {"enum": ["feud", "joke", "opinion", "running_gag"]},
          "content": {"type": "string", "minLength": 5, "maxLength": 300}
        }
      }
    }
  }
}`)

const mergeSystem = `Você organiza a memória de um avatar de telejornal. Receberá memórias numeradas (id: tipo: texto).
Agrupe SOMENTE as que dizem a mesma coisa com outras palavras (mesma piada, mesma rixa, mesmo bordão) e escreva uma versão única e curta para cada grupo.
Não junte memórias diferentes. Não invente nada que não esteja nas memórias do grupo. NUNCA inclua pessoas reais, números, datas ou política.
Responda apenas com JSON: {"groups":[{"ids":[1,2],"kind":"joke","content":"..."}]}. Sem grupos: {"groups":[]}.`

// Consolidator: consolidação semanal da memória (MODEL_FAST).
type Consolidator struct {
	Store         *store.Store
	LLM           llm.Client
	Model         string
	Lex           *check.Lexicon
	HalfLifeDays  float64
	RetireBelow   float64 // peso efetivo abaixo disso: aposenta (padrão 0,15)
	MaxUsesWeek   int     // piada recorrente: no máximo 3 vezes por semana
	Personas      map[string]config.Persona
	KnownEntities []facts.Entity
}

type ConsolidationReport struct {
	Merged, Retired, Capped int
	Dropped                 []string
}

func (c *Consolidator) Run(ctx context.Context, now time.Time) (ConsolidationReport, error) {
	var rep ConsolidationReport
	mems, err := c.Store.ActiveMemories(ctx)
	if err != nil {
		return rep, err
	}
	below := c.RetireBelow
	if below == 0 {
		below = 0.15
	}
	// 1. Aposenta as velhas (peso com decaimento abaixo do limite).
	var stale []int64
	var alive []store.Memory
	for _, m := range mems {
		w := m.Weight * math.Pow(0.5, now.Sub(m.CreatedAt).Hours()/24/c.HalfLifeDays)
		if w < below {
			stale = append(stale, m.ID)
		} else {
			alive = append(alive, m)
		}
	}
	if err := c.Store.RetireMemories(ctx, stale, "retired", map[string]any{"reason": "peso abaixo do limite", "below": below}, now); err != nil {
		return rep, err
	}
	rep.Retired = len(stale)
	// 2. Funde as parecidas, persona a persona.
	by := map[string][]store.Memory{}
	for _, m := range alive {
		by[m.Persona] = append(by[m.Persona], m)
	}
	ex := &Extractor{Lex: c.Lex}
	for persona, ms := range by {
		if len(ms) < 2 {
			continue
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Avatar: %s\nMemórias:\n", persona)
		valid := map[int64]store.Memory{}
		for _, m := range ms {
			fmt.Fprintf(&b, "%d: %s: %s\n", m.ID, m.Kind, m.Content)
			valid[m.ID] = m
		}
		resp, err := c.LLM.Complete(ctx, llm.Request{Purpose: "memory_merge", Model: c.Model, System: mergeSystem, Prompt: b.String(), MaxTokens: 1500, Temperature: llm.Float(0)})
		if err != nil {
			return rep, err
		}
		var out struct {
			Groups []struct {
				IDs     []int64 `json:"ids"`
				Kind    string  `json:"kind"`
				Content string  `json:"content"`
			} `json:"groups"`
		}
		if err := mergeSchema.Decode(resp.Text, &out); err != nil {
			return rep, err
		}
		used := map[int64]bool{}
		for _, g := range out.Groups {
			ok := true
			for _, id := range g.IDs {
				if _, in := valid[id]; !in || used[id] {
					ok = false
				}
			}
			m := store.Memory{Persona: persona, Kind: g.Kind, Content: g.Content}
			if !ok {
				rep.Dropped = append(rep.Dropped, fmt.Sprintf("%v: ids inválidos ou repetidos", g.IDs))
				continue
			}
			if why := ex.reject(m, c.Personas, c.KnownEntities); why != "" {
				rep.Dropped = append(rep.Dropped, fmt.Sprintf("%q: %s", g.Content, why))
				continue
			}
			if _, err := c.Store.MergeMemories(ctx, g.IDs, m, now); err != nil {
				return rep, err
			}
			for _, id := range g.IDs {
				used[id] = true
			}
			rep.Merged++
		}
	}
	// 3. Registra as piadas que bateram o limite da semana.
	max := c.MaxUsesWeek
	if max == 0 {
		max = 3
	}
	n, err := c.Store.LogCapped(ctx, max, now)
	rep.Capped = n
	return rep, err
}
