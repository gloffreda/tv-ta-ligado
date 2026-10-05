// Package textutil normaliza texto em português para comparação e dedupe.
package textutil

import (
	"crypto/sha256"
	"encoding/hex"
	"html"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// Fold remove acentos e põe em minúsculas. O Transformer guarda estado,
// então é criado a cada chamada (a checagem roda em paralelo).
func Fold(s string) string {
	stripMarks := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	out, _, err := transform.String(stripMarks, s)
	if err != nil {
		out = s
	}
	return strings.ToLower(out)
}

// Normalize: Fold + só letras/dígitos + espaços simples.
func Normalize(s string) string {
	s = Fold(s)
	var b strings.Builder
	space := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			space = false
		} else if !space {
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(b.String())
}

// TitleHash é a chave de dedupe por título.
func TitleHash(title string) string {
	h := sha256.Sum256([]byte(Normalize(title)))
	return hex.EncodeToString(h[:])
}

// ContainsPhrase verifica se phrase aparece em text como sequência de palavras
// inteiras, ignorando acentos, caixa e pontuação.
func ContainsPhrase(text, phrase string) bool {
	p := Normalize(phrase)
	if p == "" {
		return false
	}
	return strings.Contains(" "+Normalize(text)+" ", " "+p+" ")
}

var (
	tagRe   = regexp.MustCompile(`(?s)<[^>]*>`)
	blockRe = regexp.MustCompile(`(?i)</?(p|br|div|li|h[1-6])[^>]*>`)
	wsRe    = regexp.MustCompile(`[ \t\r\f\v]+`)
	nlRe    = regexp.MustCompile(`\n\s*\n+`)
)

// StripHTML converte HTML em texto simples.
func StripHTML(s string) string {
	s = blockRe.ReplaceAllString(s, "\n")
	s = tagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = wsRe.ReplaceAllString(s, " ")
	s = nlRe.ReplaceAllString(s, "\n\n")
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	return strings.TrimSpace(nlRe.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
}

// WordCount conta palavras para estimar duração de fala.
func WordCount(s string) int { return len(strings.Fields(s)) }
