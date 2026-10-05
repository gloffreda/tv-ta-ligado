package ingest

import (
	"net/url"
	"regexp"
	"strings"
)

// Redirecionador com a URL final embutida após "*": redir.folha.com.br/.../*https://...
var embeddedURL = regexp.MustCompile(`\*(https?://.+)$`)

// Parâmetros de consulta que carregam a URL final num redirecionador.
var redirectParams = []string{"url", "u", "link", "target", "dest", "destination"}

// Parâmetros só de rastreamento: removidos para que a mesma matéria tenha uma URL só.
func trackingParam(k string) bool {
	k = strings.ToLower(k)
	return strings.HasPrefix(k, "utm_") || strings.HasPrefix(k, "at_") ||
		k == "fbclid" || k == "gclid" || k == "mc_cid" || k == "mc_eid" || k == "cmpid" || k == "origin"
}

// NormalizeURL devolve a URL final da matéria: desfaz redirecionadores,
// tira parâmetros de rastreamento e o fragmento. Afeta a deduplicação e a
// fonte exibida no ar.
func NormalizeURL(raw string) string {
	s := strings.TrimSpace(raw)
	for i := 0; i < 3; i++ { // redirecionadores encadeados
		if m := embeddedURL.FindStringSubmatch(s); m != nil {
			s = m[1]
			continue
		}
		u, err := url.Parse(s)
		if err != nil {
			return s
		}
		found := false
		for _, p := range redirectParams {
			if v := u.Query().Get(p); strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") {
				s, found = v, true
				break
			}
		}
		if !found {
			break
		}
	}
	u, err := url.Parse(s)
	if err != nil {
		return s
	}
	q := u.Query()
	for k := range q {
		if trackingParam(k) {
			q.Del(k)
		}
	}
	u.RawQuery = q.Encode()
	u.Fragment = ""
	return u.String()
}
