// Package brnum extrai e normaliza números escritos em português do Brasil:
// 1.234,56 · 5,2% · R$ 5,43 · US$ 2 bilhões · 02/10/2026 · 5 de outubro ·
// 14h30 · "três", "mil", "milhões".
package brnum

import (
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/gloffreda/tv-ta-ligado/internal/textutil"
)

type Kind string

const (
	Plain   Kind = "plain"
	Percent Kind = "percent"
	BRL     Kind = "brl"
	USD     Kind = "usd"
	EUR     Kind = "eur"
	Date    Kind = "date"
	Time    Kind = "time"
	Word    Kind = "word" // número por extenso
)

// Number é um número encontrado no texto.
type Number struct {
	Raw      string
	Kind     Kind
	Value    float64 // valor completo (mantissa × escala), sem sinal aplicado se Negative
	Mantissa float64 // como escrito, sem escala
	Decimals int     // casas decimais escritas
	Scale    float64 // 1, 1e3, 1e6, 1e9, 1e12
	Negative bool
	HasValue bool // false para palavras vagas ("milhares")
	D        DateParts
}

// DateParts: zero significa "não informado".
type DateParts struct{ Day, Month, Year int }

// Signed devolve o valor com sinal.
func (n Number) Signed() float64 {
	if n.Negative {
		return -n.Value
	}
	return n.Value
}

var months = map[string]int{
	"janeiro": 1, "fevereiro": 2, "marco": 3, "abril": 4, "maio": 5, "junho": 6,
	"julho": 7, "agosto": 8, "setembro": 9, "outubro": 10, "novembro": 11, "dezembro": 12,
}

var scales = map[string]float64{
	"mil": 1e3, "milhao": 1e6, "milhoes": 1e6, "mi": 1e6,
	"bilhao": 1e9, "bilhoes": 1e9, "bi": 1e9, "trilhao": 1e12, "trilhoes": 1e12, "tri": 1e12,
}

// Números por extenso. "um/uma" ficam de fora (são artigos).
var words = map[string]float64{
	"dois": 2, "duas": 2, "tres": 3, "quatro": 4, "cinco": 5, "seis": 6, "sete": 7,
	"oito": 8, "nove": 9, "dez": 10, "onze": 11, "doze": 12, "treze": 13, "catorze": 14,
	"quatorze": 14, "quinze": 15, "dezesseis": 16, "dezessete": 17, "dezoito": 18,
	"dezenove": 19, "vinte": 20, "trinta": 30, "quarenta": 40, "cinquenta": 50,
	"sessenta": 60, "setenta": 70, "oitenta": 80, "noventa": 90, "cem": 100,
	"duzentos": 200, "trezentos": 300, "quinhentos": 500, "mil": 1e3,
	"milhao": 1e6, "bilhao": 1e9, "trilhao": 1e12,
	"dobro": -1, "triplo": -1, "metade": -1, "dezenas": -1, "centenas": -1,
	"milhares": -1, "milhoes": -1, "bilhoes": -1, "trilhoes": -1,
}

const monthAlt = `janeiro|fevereiro|mar[çc]o|abril|maio|junho|julho|agosto|setembro|outubro|novembro|dezembro`

var (
	reNumDate  = regexp.MustCompile(`\b(\d{1,2})/(\d{1,2})(?:/(\d{4}|\d{2}))?\b`)
	reISODate  = regexp.MustCompile(`\b(\d{4})-(\d{2})-(\d{2})\b`)
	reTextDate = regexp.MustCompile(`(?i)\b(\d{1,2})(?:º|°)?\s+de\s+(` + monthAlt + `)(?:\s+de\s+(\d{4}))?`)
	reMonthYr  = regexp.MustCompile(`(?i)\b(` + monthAlt + `)\s+(?:de\s+)?(\d{4})\b`)
	reTime     = regexp.MustCompile(`\b(\d{1,2})(?:h(\d{2})?|:(\d{2}))\b`)
	reNumber   = regexp.MustCompile(`(?i)(R\$|US\$|U\$|€)?\s?([-−+])?(\d{1,3}(?:\.\d{3})+(?:,\d+)?|\d+,\d+|\d+\.\d+|\d+)(?:\s?(mil|milh[ãa]o|milh[õo]es|bilh[ãa]o|bilh[õo]es|trilh[ãa]o|trilh[õo]es|mi|bi|tri)\b)?(\s?%|\s+por\s+cento|\s+pontos?\s+percentua(?:l|is)|\s?p\.p\.|\s+reais|\s+d[óo]lares|\s+euros)?`)
	reWord     = regexp.MustCompile(`[\p{L}]+`)
)

type span struct{ a, b int }

func overlaps(used []span, a, b int) bool {
	for _, s := range used {
		if a < s.b && b > s.a {
			return true
		}
	}
	return false
}

// Extract devolve os números do texto na ordem em que aparecem.
func Extract(text string) []Number {
	type found struct {
		pos int
		n   Number
	}
	var out []found
	var used []span
	add := func(a, b int, n Number) {
		used = append(used, span{a, b})
		out = append(out, found{a, n})
	}

	for _, m := range reISODate.FindAllStringSubmatchIndex(text, -1) {
		y, _ := strconv.Atoi(text[m[2]:m[3]])
		mo, _ := strconv.Atoi(text[m[4]:m[5]])
		d, _ := strconv.Atoi(text[m[6]:m[7]])
		add(m[0], m[1], Number{Raw: text[m[0]:m[1]], Kind: Date, HasValue: true, D: DateParts{d, mo, y}})
	}
	for _, m := range reNumDate.FindAllStringSubmatchIndex(text, -1) {
		if overlaps(used, m[0], m[1]) {
			continue
		}
		d, _ := strconv.Atoi(text[m[2]:m[3]])
		mo, _ := strconv.Atoi(text[m[4]:m[5]])
		y := 0
		if m[6] >= 0 {
			y, _ = strconv.Atoi(text[m[6]:m[7]])
			if y < 100 {
				y += 2000
			}
		}
		if d < 1 || d > 31 || mo < 1 || mo > 12 {
			continue // não é data (ex.: 24/7 vira número comum abaixo)
		}
		add(m[0], m[1], Number{Raw: text[m[0]:m[1]], Kind: Date, HasValue: true, D: DateParts{d, mo, y}})
	}
	for _, m := range reTextDate.FindAllStringSubmatchIndex(text, -1) {
		if overlaps(used, m[0], m[1]) {
			continue
		}
		d, _ := strconv.Atoi(text[m[2]:m[3]])
		mo := months[textutil.Fold(text[m[4]:m[5]])]
		y := 0
		if m[6] >= 0 {
			y, _ = strconv.Atoi(text[m[6]:m[7]])
		}
		add(m[0], m[1], Number{Raw: text[m[0]:m[1]], Kind: Date, HasValue: true, D: DateParts{d, mo, y}})
	}
	for _, m := range reMonthYr.FindAllStringSubmatchIndex(text, -1) {
		if overlaps(used, m[0], m[1]) {
			continue
		}
		mo := months[textutil.Fold(text[m[2]:m[3]])]
		y, _ := strconv.Atoi(text[m[4]:m[5]])
		add(m[0], m[1], Number{Raw: text[m[0]:m[1]], Kind: Date, HasValue: true, D: DateParts{0, mo, y}})
	}
	for _, m := range reTime.FindAllStringSubmatchIndex(text, -1) {
		if overlaps(used, m[0], m[1]) {
			continue
		}
		h, _ := strconv.Atoi(text[m[2]:m[3]])
		mi := 0
		if m[4] >= 0 {
			mi, _ = strconv.Atoi(text[m[4]:m[5]])
		} else if m[6] >= 0 {
			mi, _ = strconv.Atoi(text[m[6]:m[7]])
		}
		if h > 24 || mi > 59 {
			continue
		}
		raw := text[m[0]:m[1]]
		add(m[0], m[1], Number{Raw: raw, Kind: Time, HasValue: true, Value: float64(h) + float64(mi)/100, Mantissa: float64(h) + float64(mi)/100, Decimals: 2, Scale: 1})
	}
	for _, m := range reNumber.FindAllStringSubmatchIndex(text, -1) {
		numA, numB := m[6], m[7]
		if overlaps(used, numA, numB) {
			continue
		}
		start := m[0]
		// O espaço opcional do início pode ter sido capturado sem moeda.
		for start < numA && text[start] == ' ' {
			start++
		}
		n := Number{Raw: strings.TrimSpace(text[start:m[1]]), Kind: Plain, Scale: 1, HasValue: true}
		if m[2] >= 0 {
			switch strings.ToUpper(text[m[2]:m[3]]) {
			case "R$":
				n.Kind = BRL
			case "US$", "U$":
				n.Kind = USD
			case "€":
				n.Kind = EUR
			}
		}
		if m[4] >= 0 {
			sign := text[m[4]:m[5]]
			// Sinal só conta se não estiver colado numa palavra (covid-19).
			if sign != "+" && (m[4] == 0 || !isAlnumBefore(text, m[4])) {
				n.Negative = true
			}
		}
		mant, dec, ok := parseBR(text[numA:numB])
		if !ok {
			continue
		}
		n.Mantissa, n.Decimals = mant, dec
		if m[8] >= 0 {
			n.Scale = scales[textutil.Fold(text[m[8]:m[9]])]
		}
		if m[10] >= 0 {
			suf := textutil.Fold(strings.TrimSpace(text[m[10]:m[11]]))
			switch {
			case strings.HasPrefix(suf, "%"), strings.HasPrefix(suf, "por"), strings.HasPrefix(suf, "ponto"), strings.HasPrefix(suf, "p.p"):
				n.Kind = Percent
			case suf == "reais":
				n.Kind = BRL
			case strings.HasPrefix(suf, "dolares"):
				n.Kind = USD
			case suf == "euros":
				n.Kind = EUR
			}
		}
		n.Value = n.Mantissa * n.Scale
		add(start, m[1], n)
	}
	for _, m := range reWord.FindAllStringIndex(text, -1) {
		if overlaps(used, m[0], m[1]) {
			continue
		}
		w := textutil.Fold(text[m[0]:m[1]])
		v, ok := words[w]
		if !ok {
			continue
		}
		n := Number{Raw: text[m[0]:m[1]], Kind: Word, Scale: 1}
		if v > 0 {
			n.HasValue, n.Value, n.Mantissa = true, v, v
		}
		add(m[0], m[1], n)
	}

	// Ordena por posição (inserção simples; listas são curtas).
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].pos < out[j-1].pos; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	res := make([]Number, len(out))
	for i, f := range out {
		res[i] = f.n
	}
	return res
}

func isAlnumBefore(s string, i int) bool {
	if i == 0 {
		return false
	}
	c := s[i-1]
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// parseBR interpreta "1.234,56", "5,2", "5.2", "2026".
func parseBR(s string) (float64, int, bool) {
	dec := 0
	switch {
	case strings.Contains(s, ","):
		parts := strings.SplitN(s, ",", 2)
		dec = len(parts[1])
		s = strings.ReplaceAll(parts[0], ".", "") + "." + parts[1]
	case strings.Count(s, ".") == 1 && len(s)-strings.Index(s, ".")-1 != 3:
		dec = len(s) - strings.Index(s, ".") - 1 // decimal com ponto (5.2)
	default:
		s = strings.ReplaceAll(s, ".", "") // separador de milhar
	}
	v, err := strconv.ParseFloat(s, 64)
	return v, dec, err == nil
}

// RoundHalfUp arredonda v em d casas.
func RoundHalfUp(v float64, d int) float64 {
	p := math.Pow(10, float64(d))
	return math.Floor(v*p+0.5+1e-9) / p
}

// Equal compara floats com tolerância relativa (48,6 × 1e6 não é exato em binário).
func Equal(a, b float64) bool {
	return math.Abs(a-b) <= 1e-9*math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
}

// Matches diz se o número n (como escrito numa fala) é uma representação
// correta de v: igual, ou v arredondado nas casas que a fala usou, na escala
// que a fala usou. O sinal é ignorado (queda de 0,32% ~ -0,32%).
func (n Number) Matches(v float64) bool {
	if !n.HasValue || n.Kind == Date {
		return false
	}
	c := math.Abs(v) / n.Scale
	return math.Abs(RoundHalfUp(c, n.Decimals)-n.Mantissa) < 1e-7
}
