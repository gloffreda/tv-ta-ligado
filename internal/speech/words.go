// Package speech transforma o texto checado em texto para falar (spoken_text)
// e verifica que os números falados são os mesmos do texto checado.
package speech

import (
	"strconv"
	"strings"
)

var units = []string{"zero", "um", "dois", "três", "quatro", "cinco", "seis", "sete", "oito", "nove",
	"dez", "onze", "doze", "treze", "catorze", "quinze", "dezesseis", "dezessete", "dezoito", "dezenove"}
var tens = []string{"", "", "vinte", "trinta", "quarenta", "cinquenta", "sessenta", "setenta", "oitenta", "noventa"}
var hundreds = []string{"", "cento", "duzentos", "trezentos", "quatrocentos", "quinhentos", "seiscentos", "setecentos", "oitocentos", "novecentos"}

func fem(w string, f bool) string {
	if !f {
		return w
	}
	switch w {
	case "um":
		return "uma"
	case "dois":
		return "duas"
	}
	if strings.HasSuffix(w, "entos") {
		return strings.TrimSuffix(w, "os") + "as"
	}
	return w
}

// below1000 escreve 0 < n < 1000.
func below1000(n int64, f bool) string {
	if n == 100 {
		return "cem"
	}
	var parts []string
	if h := n / 100; h > 0 {
		parts = append(parts, fem(hundreds[h], f))
	}
	r := n % 100
	switch {
	case r == 0:
	case r < 20:
		parts = append(parts, fem(units[r], f))
	default:
		t := tens[r/10]
		if u := r % 10; u > 0 {
			t += " e " + fem(units[u], f)
		}
		parts = append(parts, t)
	}
	return strings.Join(parts, " e ")
}

// Cardinal escreve um inteiro por extenso (f: feminino, "duas mil pessoas").
func Cardinal(n int64, f bool) string {
	if n < 0 {
		return "menos " + Cardinal(-n, f)
	}
	if n < 20 {
		return fem(units[n], f)
	}
	type grp struct {
		v          int64
		sing, plur string
	}
	scales := []grp{{1_000_000_000, "bilhão", "bilhões"}, {1_000_000, "milhão", "milhões"}, {1000, "mil", "mil"}}
	var parts []string
	rest := n
	for _, s := range scales {
		q := rest / s.v
		if q == 0 {
			continue
		}
		rest %= s.v
		switch {
		case s.v == 1000 && q == 1:
			parts = append(parts, "mil")
		case s.v == 1000:
			parts = append(parts, below1000(q, f)+" mil")
		case q == 1:
			parts = append(parts, "um "+s.sing)
		default:
			parts = append(parts, below1000(q, false)+" "+s.plur)
		}
	}
	if rest > 0 {
		last := below1000(rest, f)
		// "mil e duzentos", "dois mil e vinte e seis"; "mil duzentos e cinquenta".
		if len(parts) > 0 && (rest < 100 || rest%100 == 0) {
			parts = append(parts, "e "+last)
		} else {
			parts = append(parts, last)
		}
	}
	return strings.Join(parts, " ")
}

var ordUnits = []string{"", "primeiro", "segundo", "terceiro", "quarto", "quinto", "sexto", "sétimo", "oitavo", "nono"}
var ordTens = []string{"", "décimo", "vigésimo", "trigésimo", "quadragésimo", "quinquagésimo", "sexagésimo", "septuagésimo", "octogésimo", "nonagésimo"}

// Ordinal escreve 1–99 como ordinal (f: feminino).
func Ordinal(n int, f bool) string {
	if n <= 0 || n >= 100 {
		return Cardinal(int64(n), f)
	}
	g := func(w string) string {
		if f && strings.HasSuffix(w, "o") {
			return strings.TrimSuffix(w, "o") + "a"
		}
		return w
	}
	var parts []string
	if t := n / 10; t > 0 {
		parts = append(parts, g(ordTens[t]))
	}
	if u := n % 10; u > 0 {
		parts = append(parts, g(ordUnits[u]))
	}
	return strings.Join(parts, " ")
}

// Decimal lê a parte decimal: "32" → "trinta e dois"; "05" e "9859" → dígito a dígito.
func Decimal(digits string) string {
	if len(digits) <= 2 && digits[0] != '0' {
		n, _ := strconv.Atoi(digits)
		return Cardinal(int64(n), false)
	}
	out := make([]string, len(digits))
	for i, c := range digits {
		out[i] = units[c-'0']
	}
	return strings.Join(out, " ")
}

var months = []string{"", "janeiro", "fevereiro", "março", "abril", "maio", "junho", "julho", "agosto", "setembro", "outubro", "novembro", "dezembro"}
