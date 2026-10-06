package check

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gloffreda/tv-ta-ligado/internal/facts"
	"github.com/gloffreda/tv-ta-ligado/internal/textutil"
)

var connectors = map[string]bool{"de": true, "da": true, "do": true, "das": true, "dos": true, "del": true, "van": true, "von": true}

type token struct {
	word          string
	sentenceStart bool
}

func tokenize(text string) []token {
	var out []token
	start := true
	var cur strings.Builder
	rs := []rune(text)
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, token{word: cur.String(), sentenceStart: start})
			start = false
			cur.Reset()
		}
	}
	for i, r := range rs {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '\'' || r == '’':
			cur.WriteRune(r)
		case r == '.' && cur.Len() > 0 && i+1 < len(rs) && unicode.IsUpper(rs[i+1]):
			cur.WriteRune(r) // "S.Paulo", "U.S.A." fazem parte do nome
		default:
			flush()
			switch r {
			case '.', '!', '?', '…', ':', ';', '"', '“', '”', '—', '–', '(', '\n':
				start = true
			}
		}
	}
	flush()
	return out
}

func capitalized(w string) bool {
	r, _ := utf8.DecodeRuneInString(w)
	return unicode.IsUpper(r)
}

func hasUpper(s string) bool {
	for _, r := range s {
		if unicode.IsUpper(r) {
			return true
		}
	}
	return false
}

// Lexicon reúne o que a checagem de nomes considera permitido (allowlist) e
// a lista de prenomes brasileiros comuns.
type Lexicon struct {
	allow    map[string]bool
	maxWords int
	first    map[string]bool
	codes    []string // termos da allowlist com dígitos (g1, B3, 4G, G20)
}

var hasDigit = func(s string) bool {
	for _, r := range s {
		if unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// MaskCodes troca os códigos alfanuméricos da allowlist por espaço, para não
// serem lidos como número (4G, 5G, B3, IPCA-15).
func (lx *Lexicon) MaskCodes(text string) string {
	if lx == nil {
		return text
	}
	for _, c := range lx.codes {
		re := regexp.MustCompile(`(^|[^\p{L}\p{N}])` + regexp.QuoteMeta(c) + `($|[^\p{L}\p{N}])`)
		text = re.ReplaceAllString(text, "${1} ${2}")
	}
	return text
}

// Codes devolve os termos da allowlist com dígitos.
func (lx *Lexicon) Codes() []string { return lx.codes }

// NewLexicon: allow são termos permitidos (instituições, lugares, meses,
// avatares…); firstNames, prenomes de pessoas.
func NewLexicon(allow, firstNames []string) *Lexicon {
	lx := &Lexicon{allow: map[string]bool{}, first: map[string]bool{}, maxWords: 1}
	for _, a := range append([]string{"R", "US", "U", "C"}, allow...) { // R$, US$, °C
		lx.add(a)
	}
	for _, n := range firstNames {
		if n = strings.TrimSpace(n); n != "" && !strings.HasPrefix(n, "#") {
			lx.first[textutil.Fold(n)] = true
		}
	}
	return lx
}

func (lx *Lexicon) add(a string) {
	if hasDigit(a) {
		lx.codes = append(lx.codes, strings.TrimSpace(a))
	}
	k := textutil.Normalize(a)
	if k == "" {
		return
	}
	lx.allow[k] = true
	if n := len(strings.Fields(k)); n > lx.maxWords {
		lx.maxWords = n
	}
}

// Allowed: o termo inteiro está na allowlist.
func (lx *Lexicon) Allowed(s string) bool { return lx.allow[textutil.Normalize(s)] }

// IsFirstName: a palavra é um prenome comum (e não está na allowlist).
func (lx *Lexicon) IsFirstName(w string) bool {
	return lx.first[textutil.Fold(w)] && !lx.Allowed(w)
}

// NameFindings separa o que foi achado por regra.
type NameFindings struct {
	Persons    []string // (a) entidade do tipo pessoa conhecida no banco
	Runs       []string // (b) 2+ palavras capitalizadas fora da allowlist (fora do início da frase)
	FirstNames []string // (c) prenome comum
	Others     []string // (d) organização/lugar conhecido fora da allowlist e da cobertura
}

func (f NameFindings) All() []string {
	return dedupe(append(append(append(append([]string{}, f.Persons...), f.Runs...), f.FirstNames...), f.Others...))
}

func (f NameFindings) Empty() bool { return len(f.All()) == 0 }

// Analyze aplica as regras de nome a um texto. cover são nomes adicionalmente
// permitidos (entidades dos fatos citados, ou orgs/lugares do segmento).
func (lx *Lexicon) Analyze(text string, known []facts.Entity, cover []string) NameFindings {
	var nf NameFindings
	coverSet := map[string]bool{}
	maxW := lx.maxWords
	for _, c := range cover {
		k := textutil.Normalize(c)
		if k == "" {
			continue
		}
		coverSet[k] = true
		if n := len(strings.Fields(k)); n > maxW {
			maxW = n
		}
	}
	inSet := func(k string) bool { return lx.allow[k] || coverSet[k] }

	// Sequências capitalizadas (com "de/da/do" no meio), uma frase por vez.
	toks := tokenize(text)
	var seqs [][]token
	var seq []token
	for i, t := range toks {
		switch {
		case capitalized(t.word) && !(t.sentenceStart && len(seq) > 0):
			seq = append(seq, t)
		case len(seq) > 0 && connectors[strings.ToLower(t.word)] && i+1 < len(toks) && capitalized(toks[i+1].word) && !toks[i+1].sentenceStart:
			seq = append(seq, t)
		default:
			if len(seq) > 0 {
				seqs = append(seqs, seq)
			}
			seq = nil
			if capitalized(t.word) {
				seq = []token{t}
			}
		}
	}
	if len(seq) > 0 {
		seqs = append(seqs, seq)
	}

	for _, sq := range seqs {
		covered := make([]bool, len(sq))
		for i := 0; i < len(sq); {
			matched := false
			for l := min(maxW, len(sq)-i); l >= 1; l-- {
				words := make([]string, l)
				for j := 0; j < l; j++ {
					words[j] = sq[i+j].word
				}
				if inSet(textutil.Normalize(strings.Join(words, " "))) {
					for j := 0; j < l; j++ {
						covered[i+j] = true
					}
					i += l
					matched = true
					break
				}
			}
			if !matched {
				i++
			}
		}
		// (c) prenomes, em qualquer posição.
		for k, t := range sq {
			if !covered[k] && capitalized(t.word) && lx.IsFirstName(t.word) && !coverSet[textutil.Normalize(t.word)] {
				nf.FirstNames = append(nf.FirstNames, t.word)
			}
		}
		// (b) 2+ capitalizadas não cobertas, ignorando a palavra que abre a frase.
		startAt := 0
		if sq[0].sentenceStart {
			startAt = 1
		}
		var run []string
		caps := 0
		flush := func() {
			for len(run) > 0 && connectors[strings.ToLower(run[len(run)-1])] {
				run = run[:len(run)-1]
			}
			if caps >= 2 {
				nf.Runs = append(nf.Runs, strings.Join(run, " "))
			}
			run, caps = nil, 0
		}
		for k := startAt; k < len(sq); k++ {
			t := sq[k]
			if covered[k] {
				flush()
				continue
			}
			if capitalized(t.word) {
				run = append(run, t.word)
				caps++
			} else if caps > 0 { // conector no meio
				run = append(run, t.word)
			}
		}
		flush()
	}

	// (a) e (d): entidades conhecidas citadas. Entidade sem maiúscula não é nome próprio.
	for _, e := range known {
		name := strings.TrimSpace(e.Name)
		if !hasUpper(name) || lx.Allowed(name) || covered(name, cover) || !MentionsEntity(text, name) {
			continue
		}
		if e.Type == facts.Person {
			nf.Persons = append(nf.Persons, name)
		} else {
			nf.Others = append(nf.Others, name)
		}
	}
	nf.Persons, nf.Runs, nf.FirstNames, nf.Others = dedupe(nf.Persons), dedupe(nf.Runs), dedupe(nf.FirstNames), dedupe(nf.Others)
	return nf
}

// MentionsEntity: a entidade aparece no texto. Nome de uma palavra só conta
// com a mesma grafia (inclusive maiúsculas): "novo" não é o partido "Novo",
// "vitória" não é a cidade "Vitória". Nomes compostos ignoram caixa e acento.
func MentionsEntity(text, entity string) bool {
	e := strings.TrimSpace(entity)
	if len([]rune(e)) < 2 {
		return false
	}
	if len(strings.Fields(e)) == 1 {
		for _, t := range tokenize(text) {
			if t.word == e {
				return true
			}
		}
		return false
	}
	return textutil.ContainsPhrase(text, e)
}

// covered: o nome está contido num dos nomes permitidos ou contém um deles.
func covered(name string, entities []string) bool {
	for _, e := range entities {
		if textutil.ContainsPhrase(e, name) || textutil.ContainsPhrase(name, e) {
			return true
		}
	}
	return false
}

func dedupe(ss []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range ss {
		k := textutil.Normalize(s)
		if k != "" && !seen[k] {
			seen[k] = true
			out = append(out, s)
		}
	}
	return out
}
