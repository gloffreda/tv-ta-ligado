package speech

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Dict é config/pronunciation.yaml: como falar siglas, abreviações e símbolos.
type Dict struct {
	Terms map[string]string `yaml:"terms"` // termo exato (caixa e acento) → como falar
}

func LoadDict(path string) (Dict, error) {
	var d Dict
	b, err := os.ReadFile(path)
	if err != nil {
		return d, err
	}
	return d, yaml.Unmarshal(b, &d)
}

// Substantivos femininos comuns depois de números ("duas pessoas", "duzentas vagas").
var feminine = map[string]bool{"pessoas": true, "vagas": true, "moradias": true, "horas": true, "toneladas": true, "crianças": true,
	"mulheres": true, "vezes": true, "casas": true, "escolas": true, "cidades": true, "empresas": true, "semanas": true, "famílias": true,
	"unidades": true, "mortes": true, "vítimas": true, "doses": true, "cadeiras": true, "rodadas": true, "partidas": true, "medalhas": true,
	"linhas": true, "obras": true, "ruas": true, "pontes": true, "lojas": true, "vagas,": true, "árvores": true, "pessoa": true, "hora": true}

const numRe = `(\d{1,3}(?:\.\d{3})+(?:,\d+)?|\d+(?:,\d+)?)`

var (
	reDate      = regexp.MustCompile(`\b(\d{1,2})/(\d{1,2})(?:/(\d{4}))?\b`)
	reISO       = regexp.MustCompile(`\b(\d{4})-(\d{2})-(\d{2})\b`)
	reTime      = regexp.MustCompile(`\b(\d{1,2})(?:h(\d{2})?|:(\d{2}))\b`)
	reMoney     = regexp.MustCompile(`(R\$|US\$|U\$|€)\s?([-−]?)` + numRe + `(?:\s?(mil|milh[ãa]o|milh[õo]es|bilh[ãa]o|bilh[õo]es|mi|bi)\b)?`)
	rePercent   = regexp.MustCompile(`([-−+]?)` + numRe + `(?:\s?(mil|milh[ãa]o|milh[õo]es|bilh[ãa]o|bilh[õo]es))?\s?%`)
	reTemp      = regexp.MustCompile(`([-−]?)` + numRe + `\s?°C`)
	reOrdinal   = regexp.MustCompile(`\b(\d{1,2})(º|ª|°)`)
	reWeekday   = regexp.MustCompile(`(?i)\b((?:segunda|terça|quarta|quinta|sexta)(?:-feira)?|sábado|domingo)\s*\((\d{1,2})\)`)
	reNumber    = regexp.MustCompile(`([-−]\s?)?\b` + numRe + `(?:\s?(mil|milh[ãa]o|milh[õo]es|bilh[ãa]o|bilh[õo]es)\b)?(\s+\p{L}+)?`)
	reSpaces    = regexp.MustCompile(`\s+`)
	reHyphenNum = regexp.MustCompile(`(\p{L})-(\d)`) // covid-19 → covid 19
)

// splitNum separa "1.234,56" em inteiro (sem pontos) e dígitos decimais.
func splitNum(s string) (int64, string) {
	intPart, dec, _ := strings.Cut(s, ",")
	intPart = strings.ReplaceAll(intPart, ".", "")
	n, _ := strconv.ParseInt(intPart, 10, 64)
	return n, dec
}

// sayNumber lê "48,6" → "quarenta e oito vírgula seis".
func sayNumber(s string, f bool) string {
	n, dec := splitNum(s)
	out := Cardinal(n, f && dec == "")
	if dec != "" {
		out += " vírgula " + Decimal(dec)
	}
	return out
}

func scaleWord(s string) string {
	switch strings.ToLower(s) {
	case "mi", "milhão", "milhao":
		return "milhão"
	case "milhões", "milhoes":
		return "milhões"
	case "bi", "bilhão", "bilhao":
		return "bilhão"
	case "bilhões", "bilhoes":
		return "bilhões"
	case "mil":
		return "mil"
	}
	return s
}

func currencyNames(sym string) (string, string) {
	switch sym {
	case "US$", "U$":
		return "dólar", "dólares"
	case "€":
		return "euro", "euros"
	}
	return "real", "reais"
}

// sayMoney: R$ 5,43 → "cinco reais e quarenta e três centavos";
// R$ 48,6 milhões → "quarenta e oito vírgula seis milhões de reais";
// R$ 4,9859 → "quatro vírgula nove oito cinco nove reais".
func sayMoney(sym, sign, num, scale string) string {
	sing, plur := currencyNames(sym)
	n, dec := splitNum(num)
	if strings.Trim(dec, "0") == "" {
		dec = "" // R$ 1,00 → "um real"
	}
	neg := ""
	if sign != "" {
		neg = "menos "
	}
	if scale != "" {
		sc := scaleWord(scale)
		de := " de "
		if sc == "mil" {
			de = " "
		}
		return neg + sayNumber(num, false) + " " + sc + de + plur
	}
	unit := plur
	if n == 1 && dec == "" {
		unit = sing
	}
	switch {
	case dec == "":
		return neg + Cardinal(n, false) + " " + unit
	case len(dec) == 2:
		cents, _ := strconv.Atoi(dec)
		c := "centavos"
		if cents == 1 {
			c = "centavo"
		}
		if n == 0 {
			return neg + Cardinal(int64(cents), false) + " " + c
		}
		unit = plur
		if n == 1 {
			unit = sing
		}
		return neg + Cardinal(n, false) + " " + unit + " e " + Cardinal(int64(cents), false) + " " + c
	default:
		return neg + sayNumber(num, false) + " " + plur
	}
}

// Normalize devolve o texto para falar. O texto checado nunca é alterado: o
// chamador guarda os dois (text e spoken_text).
func Normalize(text string, d Dict) string {
	s, _ := NormalizeChecked(text, d)
	return s
}

// NormalizeChecked normaliza e confere os números falados contra o texto
// checado ANTES do dicionário de pronúncia (que só troca siglas: "B3" → "bê
// três" não é um número novo).
func NormalizeChecked(text string, d Dict) (string, error) {
	masked, codes := maskCodes(text, d)
	pre := normalizeNumbers(masked)
	spoken := finish(applyDict(unmask(pre, codes, true), d))
	return spoken, VerifyNumbers(unmask(masked, codes, false), finish(unmask(pre, codes, false)))
}

// maskCodes protege termos do dicionário que têm dígitos (5G, B3, g1) da
// conversão de números: viram marcadores e voltam depois com a pronúncia.
func maskCodes(text string, d Dict) (string, []string) {
	var codes []string
	for k := range d.Terms {
		if strings.IndexFunc(k, func(r rune) bool { return r >= '0' && r <= '9' }) >= 0 {
			codes = append(codes, k)
		}
	}
	sort.Slice(codes, func(i, j int) bool { return len(codes[i]) > len(codes[j]) })
	for i, c := range codes {
		re := regexp.MustCompile(`(^|[^\p{L}\p{N}])` + regexp.QuoteMeta(c) + `($|[^\p{L}\p{N}])`)
		for re.MatchString(text) {
			text = re.ReplaceAllString(text, "${1}\x00"+string(rune('A'+i))+"\x00${2}")
		}
	}
	return text, codes
}

// unmask devolve os códigos: com pronúncia (keep) ou removidos (para a verificação).
func unmask(s string, codes []string, keep bool) string {
	for i, c := range codes {
		rep := ""
		if keep {
			rep = c
		}
		s = strings.ReplaceAll(s, "\x00"+string(rune('A'+i))+"\x00", rep)
	}
	return s
}

func finish(s string) string { return strings.TrimSpace(reSpaces.ReplaceAllString(s, " ")) }

func normalizeNumbers(text string) string {
	s := reHyphenNum.ReplaceAllString(text, "$1 $2")
	s = reISO.ReplaceAllStringFunc(s, func(m string) string {
		p := reISO.FindStringSubmatch(m)
		y, _ := strconv.Atoi(p[1])
		mo, _ := strconv.Atoi(p[2])
		dd, _ := strconv.Atoi(p[3])
		return sayDate(dd, mo, y)
	})
	s = reDate.ReplaceAllStringFunc(s, func(m string) string {
		p := reDate.FindStringSubmatch(m)
		dd, _ := strconv.Atoi(p[1])
		mo, _ := strconv.Atoi(p[2])
		y := 0
		if p[3] != "" {
			y, _ = strconv.Atoi(p[3])
		}
		if dd < 1 || dd > 31 || mo < 1 || mo > 12 {
			return m
		}
		return sayDate(dd, mo, y)
	})
	s = reWeekday.ReplaceAllString(s, "$1, dia $2")
	s = reMoney.ReplaceAllStringFunc(s, func(m string) string {
		p := reMoney.FindStringSubmatch(m)
		return sayMoney(p[1], p[2], p[3], p[4])
	})
	s = rePercent.ReplaceAllStringFunc(s, func(m string) string {
		p := rePercent.FindStringSubmatch(m)
		out := sayNumber(p[2], false)
		if p[3] != "" {
			out += " " + scaleWord(p[3])
		}
		return signWord(p[1]) + out + " por cento"
	})
	s = reTemp.ReplaceAllStringFunc(s, func(m string) string {
		p := reTemp.FindStringSubmatch(m)
		n, dec := splitNum(p[2])
		unit := "graus"
		if n == 1 && dec == "" {
			unit = "grau"
		}
		return signWord(p[1]) + sayNumber(p[2], false) + " " + unit
	})
	s = reTime.ReplaceAllStringFunc(s, func(m string) string {
		p := reTime.FindStringSubmatch(m)
		h, _ := strconv.Atoi(p[1])
		mi := p[2] + p[3]
		if h > 24 {
			return m
		}
		hw := "horas"
		if h == 1 {
			hw = "hora"
		}
		out := Cardinal(int64(h), true) + " " + hw
		if mi != "" && mi != "00" {
			v, _ := strconv.Atoi(mi)
			if v > 59 {
				return m
			}
			out += " e " + Cardinal(int64(v), false) + " minutos"
		}
		return out
	})
	s = reOrdinal.ReplaceAllStringFunc(s, func(m string) string {
		p := reOrdinal.FindStringSubmatch(m)
		n, _ := strconv.Atoi(p[1])
		return Ordinal(n, p[2] == "ª")
	})
	s = reNumber.ReplaceAllStringFunc(s, func(m string) string {
		p := reNumber.FindStringSubmatch(m)
		sign, num, scale, next := p[1], p[2], p[3], p[4]
		f := feminine[strings.ToLower(strings.TrimSpace(next))]
		out := signWord(sign)
		if scale != "" {
			out += sayNumber(num, false) + " " + scaleWord(scale)
		} else {
			out += sayNumber(num, f)
		}
		return out + next
	})
	return strings.ReplaceAll(s, "às uma hora", "à uma hora")
}

func signWord(sign string) string {
	switch strings.TrimSpace(sign) {
	case "-", "−":
		return "menos "
	case "+":
		return "mais "
	}
	return ""
}

func sayDate(d, m, y int) string {
	day := Cardinal(int64(d), false)
	if d == 1 {
		day = "primeiro"
	}
	out := day + " de " + months[m]
	if y > 0 {
		out += " de " + Cardinal(int64(y), false)
	}
	return out
}

// applyDict troca termos inteiros (respeitando caixa), do mais longo ao mais curto.
func applyDict(s string, d Dict) string {
	keys := make([]string, 0, len(d.Terms))
	for k := range d.Terms {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	for _, k := range keys {
		re := regexp.MustCompile(`(^|[^\p{L}\p{N}])` + regexp.QuoteMeta(k) + `($|[^\p{L}\p{N}])`)
		v := d.Terms[k]
		for re.MatchString(s) {
			s = re.ReplaceAllString(s, "${1}"+v+"${2}")
		}
	}
	return s
}

var _ = fmt.Sprintf
