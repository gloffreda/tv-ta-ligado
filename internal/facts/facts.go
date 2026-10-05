// Package facts define o fato atômico e as regras para criá-lo: validade,
// fatos de mercado/clima montados por código e validação da extração por LLM.
package facts

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

type Kind string

const (
	Headline Kind = "headline"
	Market   Kind = "market"
	Weather  Kind = "weather"
	Glossary Kind = "glossary" // definição com fonte oficial, sem validade
)

// NoExpiry é a "validade nula" do glossário.
var NoExpiry = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)

// Tipos de entidade. Vazio = desconhecido (fatos anteriores à tipagem).
const (
	Person = "person"
	Org    = "org"
	Place  = "place"
	Other  = "other"
)

// Entity é uma pessoa, organização, lugar ou outro nome citado num fato.
type Entity struct {
	Name string `json:"name" yaml:"name"`
	Type string `json:"type" yaml:"type"`
}

// UnmarshalJSON aceita também a forma antiga, uma string só com o nome.
func (e *Entity) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		e.Type = ""
		return json.Unmarshal(b, &e.Name)
	}
	type plain Entity
	return json.Unmarshal(b, (*plain)(e))
}

// Names devolve só os nomes.
func Names(es []Entity) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Name
	}
	return out
}

// HasPerson: algum fato cita uma pessoa.
func HasPerson(fs []Fact) bool {
	for _, f := range fs {
		for _, e := range f.Entities {
			if e.Type == Person {
				return true
			}
		}
	}
	return false
}

// TTL de cada tipo de fato (glossário não vence: ver NoExpiry).
func TTL(k Kind) time.Duration {
	switch k {
	case Market:
		return 24 * time.Hour
	case Weather:
		return 12 * time.Hour
	default:
		return 48 * time.Hour
	}
}

type Fact struct {
	ID         int64     `json:"id"`
	ArticleID  *int64    `json:"article_id,omitempty"`
	Kind       Kind      `json:"kind"`
	Claim      string    `json:"claim"`
	Entities   []Entity  `json:"entities"`
	Value      *float64  `json:"value,omitempty"`
	Unit       string    `json:"unit,omitempty"`
	AsOf       time.Time `json:"as_of"`
	SourceName string    `json:"source_name"`
	SourceURL  string    `json:"source_url"`
	Series     string    `json:"series,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// Expired: o fato já não pode sustentar uma fala.
func (f Fact) Expired(now time.Time) bool { return !now.Before(f.ExpiresAt) }

// Fingerprint identifica o mesmo dado entre ingestões.
func (f Fact) Fingerprint() string {
	h := sha256.Sum256([]byte(string(f.Kind) + "\x00" + f.SourceURL + "\x00" + f.Claim))
	return hex.EncodeToString(h[:])
}

// FormatBR formata v com dec casas no padrão brasileiro (1.234,56).
func FormatBR(v float64, dec int) string {
	neg := v < 0
	s := strconv.FormatFloat(math.Abs(v), 'f', dec, 64)
	intPart, frac, _ := strings.Cut(s, ".")
	var b strings.Builder
	for i, r := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(r)
	}
	out := b.String()
	if frac != "" {
		out += "," + frac
	}
	if neg {
		out = "-" + out
	}
	return out
}

// Decimals conta as casas decimais de uma string numérica da API ("5.2079").
func Decimals(s string) int {
	if _, frac, ok := strings.Cut(s, "."); ok {
		return len(frac)
	}
	return 0
}

var monthNames = []string{"", "janeiro", "fevereiro", "março", "abril", "maio", "junho",
	"julho", "agosto", "setembro", "outubro", "novembro", "dezembro"}

// MonthYear: "agosto de 2026".
func MonthYear(t time.Time) string {
	return fmt.Sprintf("%s de %d", monthNames[t.Month()], t.Year())
}
