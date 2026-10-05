package check

import (
	"strings"
	"unicode"
	"unicode/utf8"

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
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, token{word: cur.String(), sentenceStart: start})
			start = false
			cur.Reset()
		}
	}
	for _, r := range text {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '\'' || r == '’':
			cur.WriteRune(r)
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

// DetectNames acha nomes próprios por heurística: sequências de palavras
// capitalizadas (com "de/da/do" no meio) fora do início da frase. Uma
// sequência que começa a frase perde a primeira palavra. Exceções são ignoradas.
func DetectNames(text string, exceptions []string) []string {
	exc := map[string]bool{"r": true, "us": true, "u": true, "c": true} // R$, US$, °C
	for _, e := range exceptions {
		exc[textutil.Normalize(e)] = true
	}
	toks := tokenize(text)
	var names []string
	emit := func(seq []token) {
		if len(seq) > 0 && seq[0].sentenceStart {
			seq = seq[1:]
		}
		// Conectores nas pontas não fazem parte do nome.
		for len(seq) > 0 && connectors[strings.ToLower(seq[len(seq)-1].word)] {
			seq = seq[:len(seq)-1]
		}
		for len(seq) > 0 && connectors[strings.ToLower(seq[0].word)] {
			seq = seq[1:]
		}
		if len(seq) == 0 {
			return
		}
		words := make([]string, len(seq))
		for i, t := range seq {
			words[i] = t.word
		}
		name := strings.Join(words, " ")
		if exc[textutil.Normalize(name)] {
			return
		}
		// Remove palavras de exceção soltas (ex.: "Duda" numa sequência "Duda Faísca").
		var kept []string
		for _, w := range words {
			if !exc[textutil.Normalize(w)] || connectors[strings.ToLower(w)] {
				kept = append(kept, w)
			}
		}
		if len(kept) == 0 || (len(kept) == 1 && connectors[strings.ToLower(kept[0])]) {
			return
		}
		names = append(names, strings.Join(kept, " "))
	}
	var seq []token
	for i, t := range toks {
		if capitalized(t.word) && !(t.sentenceStart && len(seq) > 0) {
			seq = append(seq, t)
			continue
		}
		if len(seq) > 0 && connectors[strings.ToLower(t.word)] && i+1 < len(toks) && capitalized(toks[i+1].word) && !toks[i+1].sentenceStart {
			seq = append(seq, t)
			continue
		}
		emit(seq)
		seq = nil
		if capitalized(t.word) {
			seq = append(seq, t)
		}
	}
	emit(seq)
	return names
}

// mentionsEntity: a entidade aparece no texto. Siglas curtas (SP, SE, AM)
// só contam com a mesma caixa, para não confundir com "se", "am"...
func MentionsEntity(text, entity string) bool {
	e := strings.TrimSpace(entity)
	if len([]rune(e)) < 2 {
		return false
	}
	if len([]rune(e)) <= 3 && strings.ToUpper(e) == e {
		for _, t := range tokenize(text) {
			if t.word == e {
				return true
			}
		}
		return false
	}
	return textutil.ContainsPhrase(text, e)
}

// covered: o nome está contido numa entidade ou contém uma entidade.
func covered(name string, entities []string) bool {
	for _, e := range entities {
		if textutil.ContainsPhrase(e, name) || textutil.ContainsPhrase(name, e) {
			return true
		}
	}
	return false
}
