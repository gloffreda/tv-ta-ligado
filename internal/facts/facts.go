// Package facts define o fato atômico e as regras para criá-lo: validade,
// fatos de mercado/clima montados por código e validação da extração por LLM.
package facts

import (
	"crypto/sha256"
	"encoding/hex"
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
)

// TTL de cada tipo de fato.
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
	Entities   []string  `json:"entities"`
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
