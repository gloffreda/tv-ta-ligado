package facts

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/gloffreda/tv-ta-ligado/internal/brnum"
	"github.com/gloffreda/tv-ta-ligado/internal/textutil"
)

// Candidate é um fato proposto pelo LLM a partir do texto de uma fonte.
type Candidate struct {
	Claim    string   `json:"claim"`
	Entities []string `json:"entities"`
	Value    *float64 `json:"value"`
	Unit     string   `json:"unit"`
}

// ExtractionSchema é o JSON Schema da resposta do extrator.
const ExtractionSchema = `{
  "type": "object",
  "required": ["facts"],
  "additionalProperties": false,
  "properties": {
    "facts": {
      "type": "array",
      "maxItems": 6,
      "items": {
        "type": "object",
        "required": ["claim", "entities", "value", "unit"],
        "additionalProperties": false,
        "properties": {
          "claim": {"type": "string", "minLength": 10, "maxLength": 400},
          "entities": {"type": "array", "items": {"type": "string", "minLength": 2}},
          "value": {"type": ["number", "null"]},
          "unit": {"type": "string"}
        }
      }
    }
  }
}`

// Validate garante, por código, que cada número e cada entidade do fato
// aparecem literalmente no texto da fonte. O que não bate é descartado.
func Validate(c Candidate, source string) error {
	claim := strings.TrimSpace(c.Claim)
	if claim == "" {
		return errors.New("claim vazio")
	}
	srcNums := brnum.Extract(source)
	for _, n := range brnum.Extract(claim) {
		if !literalIn(n, srcNums) {
			return fmt.Errorf("número %q não está na fonte", n.Raw)
		}
	}
	if c.Value != nil {
		found := false
		for _, s := range srcNums {
			if s.HasValue && s.Kind != brnum.Date && brnum.Equal(math.Abs(s.Value), math.Abs(*c.Value)) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("value %v não está na fonte", *c.Value)
		}
	}
	for _, e := range c.Entities {
		if !textutil.ContainsPhrase(source, e) {
			return fmt.Errorf("entidade %q não está na fonte", e)
		}
	}
	return nil
}

// literalIn: o mesmo número (mesmo valor e mesma precisão) existe na fonte.
func literalIn(n brnum.Number, src []brnum.Number) bool {
	for _, s := range src {
		switch {
		case n.Kind == brnum.Date || s.Kind == brnum.Date:
			if n.Kind == brnum.Date && s.Kind == brnum.Date && datePartsCompatible(n.D, s.D) {
				return true
			}
			// "2026" no fato pode vir de uma data completa na fonte.
			if n.Kind != brnum.Date && s.Kind == brnum.Date && n.HasValue && n.Scale == 1 && n.Decimals == 0 {
				v := int(n.Value)
				if v == s.D.Day || v == s.D.Month || v == s.D.Year {
					return true
				}
			}
		case !n.HasValue:
			if textutil.Fold(n.Raw) == textutil.Fold(s.Raw) {
				return true
			}
		case s.HasValue:
			if brnum.Equal(n.Value, s.Value) && n.Negative == s.Negative {
				return true
			}
		}
	}
	return false
}

// datePartsCompatible: cada parte informada em a precisa bater com b.
func datePartsCompatible(a, b brnum.DateParts) bool {
	if a.Day != 0 && a.Day != b.Day {
		return false
	}
	if a.Month != 0 && a.Month != b.Month {
		return false
	}
	if a.Year != 0 && b.Year != 0 && a.Year != b.Year {
		return false
	}
	return a.Day != 0 || a.Month != 0 || a.Year != 0
}

// DatePartsCompatible é exportada para a checagem.
func DatePartsCompatible(a, b brnum.DateParts) bool { return datePartsCompatible(a, b) }
