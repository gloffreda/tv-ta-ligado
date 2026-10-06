package speech

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/gloffreda/tv-ta-ligado/internal/brnum"
	"github.com/gloffreda/tv-ta-ligado/internal/textutil"
)

// atom é um número comparável; optional: pode sobrar no lado falado
// ("um" artigo, "segundo" de "segundo o Banco Central").
type atom struct {
	v        float64
	optional bool
}

var wordVal = map[string]int64{
	"zero": 0, "um": 1, "uma": 1, "dois": 2, "duas": 2, "tres": 3, "quatro": 4, "cinco": 5, "seis": 6, "sete": 7, "oito": 8, "nove": 9,
	"dez": 10, "onze": 11, "doze": 12, "treze": 13, "catorze": 14, "quatorze": 14, "quinze": 15, "dezesseis": 16, "dezessete": 17,
	"dezoito": 18, "dezenove": 19, "vinte": 20, "trinta": 30, "quarenta": 40, "cinquenta": 50, "sessenta": 60, "setenta": 70,
	"oitenta": 80, "noventa": 90, "cem": 100, "cento": 100, "duzentos": 200, "duzentas": 200, "trezentos": 300, "trezentas": 300,
	"quatrocentos": 400, "quatrocentas": 400, "quinhentos": 500, "quinhentas": 500, "seiscentos": 600, "seiscentas": 600,
	"setecentos": 700, "setecentas": 700, "oitocentos": 800, "oitocentas": 800, "novecentos": 900, "novecentas": 900,
}
var scaleVal = map[string]float64{"mil": 1e3, "milhao": 1e6, "milhoes": 1e6, "bilhao": 1e9, "bilhoes": 1e9}
var ordVal = map[string]int64{"primeiro": 1, "primeira": 1, "segundo": 2, "segunda": 2, "terceiro": 3, "terceira": 3, "quarto": 4, "quarta": 4,
	"quinto": 5, "quinta": 5, "sexto": 6, "sexta": 6, "setimo": 7, "setima": 7, "oitavo": 8, "oitava": 8, "nono": 9, "nona": 9,
	"decimo": 10, "decima": 10, "vigesimo": 20, "vigesima": 20, "trigesimo": 30, "trigesima": 30, "quadragesimo": 40, "quadragesima": 40,
	"quinquagesimo": 50, "quinquagesima": 50, "sexagesimo": 60, "sexagesima": 60, "septuagesimo": 70, "septuagesima": 70,
	"octogesimo": 80, "octogesima": 80, "nonagesimo": 90, "nonagesima": 90}
var monthVal = map[string]int64{"janeiro": 1, "fevereiro": 2, "marco": 3, "abril": 4, "maio": 5, "junho": 6, "julho": 7, "agosto": 8,
	"setembro": 9, "outubro": 10, "novembro": 11, "dezembro": 12}

// SpokenNumbers lê os números de um texto falado (por extenso).
func spokenAtoms(spoken string) []atom {
	toks := strings.Fields(textutil.Normalize(spoken))
	var out []atom
	i := 0
	isNum := func(t string) bool { _, ok := wordVal[t]; _, s := scaleVal[t]; return ok || s }
	for i < len(toks) {
		t := toks[i]
		// Mês só vale como número dentro de data: com dia antes ("dois de
		// outubro", "primeiro de março") ou ano depois ("agosto de dois mil...").
		// "Rio de Janeiro" não é janeiro.
		if m, ok := monthVal[t]; ok {
			dayBefore := i >= 2 && toks[i-1] == "de" && (isNum(toks[i-2]) || toks[i-2] == "primeiro")
			yearAfter := i+2 < len(toks) && toks[i+1] == "de" && isNum(toks[i+2])
			if dayBefore || yearAfter {
				out = append(out, atom{v: float64(m)})
				i++
				continue
			}
		}
		if o, ok := ordVal[t]; ok {
			// Ordinal composto: "vigesima quarta".
			v := o
			j := i + 1
			if o >= 10 && j < len(toks) {
				if u, ok := ordVal[toks[j]]; ok && u < 10 {
					v += u
					j++
				}
			}
			// "segunda" de "segunda-feira" vira "segunda feira" no Normalize; ordinais
			// ambíguos (primeiro, segundo, quinta...) podem sobrar.
			out = append(out, atom{v: float64(v), optional: v <= 6 || (j < len(toks) && toks[j] == "feira")})
			i = j
			continue
		}
		if !isNum(t) || t == "cento" && i > 0 && toks[i-1] == "por" { // "por cento" não é 100
			i++
			continue
		}
		// Frase numérica: grupos com "e", escalas, "virgula" e decimais.
		var total, group float64
		any := false
		j := i
		for j < len(toks) {
			w := toks[j]
			if v, ok := wordVal[w]; ok {
				group += float64(v)
				any = true
				j++
				continue
			}
			if s, ok := scaleVal[w]; ok {
				if group == 0 {
					group = 1
				}
				total += group * s
				group = 0
				any = true
				j++
				continue
			}
			if w == "e" && j+1 < len(toks) && isNum(toks[j+1]) {
				j++
				continue
			}
			break
		}
		value := total + group
		// Decimais: "virgula trinta e dois" ou "virgula nove oito cinco nove".
		if j < len(toks) && toks[j] == "virgula" {
			k := j + 1
			var digits []int64
			allSingle := true
			for k < len(toks) {
				if v, ok := wordVal[toks[k]]; ok {
					digits = append(digits, v)
					allSingle = allSingle && v < 10
					k++
					continue
				}
				if toks[k] == "e" && k+1 < len(toks) {
					if _, ok := wordVal[toks[k+1]]; ok {
						allSingle = false
						k++
						continue
					}
				}
				break
			}
			if len(digits) > 0 {
				var decStr string
				if allSingle && len(digits) > 1 || len(digits) == 1 && digits[0] < 10 {
					for _, d := range digits {
						decStr += fmt.Sprint(d)
					}
				} else {
					var sum int64
					for _, d := range digits {
						sum += d
					}
					decStr = fmt.Sprint(sum)
				}
				var dv float64
				fmt.Sscanf("0."+decStr, "%g", &dv)
				value += dv
				// Escala depois do decimal: "quarenta e oito virgula seis milhoes".
				if k < len(toks) {
					if s, ok := scaleVal[toks[k]]; ok {
						value *= s
						k++
					}
				}
				j = k
			}
		}
		// Moeda com centavos: "cinco reais e quarenta e tres centavos".
		if j+1 < len(toks) && (toks[j] == "reais" || toks[j] == "real" || toks[j] == "dolares" || toks[j] == "dolar" || toks[j] == "euros" || toks[j] == "euro") && toks[j+1] == "e" {
			k := j + 2
			var cents int64
			for k < len(toks) {
				if v, ok := wordVal[toks[k]]; ok {
					cents += v
					k++
					continue
				}
				if toks[k] == "e" {
					k++
					continue
				}
				break
			}
			if k < len(toks) && (toks[k] == "centavos" || toks[k] == "centavo") {
				value += float64(cents) / 100
				j = k + 1
			}
		}
		// "cinquenta centavos" (sem reais) é fração da moeda.
		if j < len(toks) && (toks[j] == "centavos" || toks[j] == "centavo") {
			value /= 100
			j++
		}
		if i > 0 && toks[i-1] == "menos" {
			value = -value
		}
		if any {
			// "um" sozinho é artigo na maior parte das vezes.
			out = append(out, atom{v: value, optional: value == 1 && j == i+1})
		}
		if j == i {
			j++
		}
		i = j
	}
	// Algarismos que sobraram no texto falado (ex.: "COP30").
	for _, n := range brnum.Extract(spoken) {
		if n.Kind != brnum.Word && n.HasValue {
			out = append(out, textAtoms([]brnum.Number{n})...)
		}
	}
	return out
}

func textAtoms(ns []brnum.Number) []atom {
	var out []atom
	for _, n := range ns {
		switch {
		case n.Kind == brnum.Date:
			for _, p := range []int{n.D.Day, n.D.Month, n.D.Year} {
				if p != 0 {
					out = append(out, atom{v: float64(p)})
				}
			}
		case n.Kind == brnum.Time:
			h := math.Floor(n.Value)
			out = append(out, atom{v: h})
			if m := math.Round((n.Value - h) * 100); m > 0 {
				out = append(out, atom{v: m})
			}
		case n.HasValue:
			out = append(out, atom{v: n.Signed()})
		}
	}
	return out
}

// VerifyNumbers confere que o texto falado diz exatamente os números do texto
// checado (sem perder nem inventar nenhum). Valor 1 é tolerado como artigo.
func VerifyNumbers(text, spoken string) error {
	want := textAtoms(brnum.Extract(text))
	got := spokenAtoms(spoken)
	sort.Slice(want, func(i, j int) bool { return want[i].v < want[j].v })
	used := make([]bool, len(got))
	var missing []string
	for _, w := range want {
		found := false
		for i, g := range got {
			if !used[i] && brnum.Equal(g.v, w.v) {
				used[i], found = true, true
				break
			}
		}
		if !found && !brnum.Equal(w.v, 1) {
			missing = append(missing, trim(w.v))
		}
	}
	var extra []string
	for i, g := range got {
		if !used[i] && !g.optional {
			extra = append(extra, trim(g.v))
		}
	}
	if len(missing) > 0 || len(extra) > 0 {
		return fmt.Errorf("números falados não batem: faltam %v, sobram %v", missing, extra)
	}
	return nil
}

func trim(v float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.6f", v), "0"), ".")
}
