package ingest

import "testing"

func TestNormalizeURL(t *testing.T) {
	cases := map[string]string{
		"https://redir.folha.com.br/redir/online/emcimadahora/rss091/*https://www1.folha.uol.com.br/poder/2026/10/materia.shtml": "https://www1.folha.uol.com.br/poder/2026/10/materia.shtml",
		"https://www.bbc.com/portuguese/articles/c32l4nxgpvq2o?at_medium=RSS&at_campaign=rss":                                    "https://www.bbc.com/portuguese/articles/c32l4nxgpvq2o",
		"https://exemplo.invalid/a?id=7&utm_source=rss&utm_medium=feed#comentarios":                                              "https://exemplo.invalid/a?id=7",
		"https://news.exemplo.invalid/r?url=https%3A%2F%2Fportal.invalid%2Fnoticia%3Futm_source%3Dx":                             "https://portal.invalid/noticia",
		"https://g1.globo.com/economia/noticia/2026/10/05/x.ghtml":                                                               "https://g1.globo.com/economia/noticia/2026/10/05/x.ghtml",
	}
	for in, want := range cases {
		if got := NormalizeURL(in); got != want {
			t.Errorf("%s\n got %s\nwant %s", in, got, want)
		}
	}
}
