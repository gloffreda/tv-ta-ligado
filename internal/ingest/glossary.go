package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/gloffreda/tv-ta-ligado/internal/config"
	"github.com/gloffreda/tv-ta-ligado/internal/facts"
	"github.com/gloffreda/tv-ta-ligado/internal/textutil"
)

// GlossaryReport resume a carga do glossário.
type GlossaryReport struct {
	Loaded   []string
	Rejected map[string]string
}

// LoadGlossary valida a fonte de cada termo e grava os válidos como fatos
// kind=glossary, sem validade. Termo com fonte inválida fica fora (e, se já
// existia, deixa de valer).
func (in *Ingester) LoadGlossary(ctx context.Context, terms []config.GlossaryTerm, series func(string) string) GlossaryReport {
	r := GlossaryReport{Rejected: map[string]string{}}
	if in.Gate != nil {
		if err := in.Gate.Allow(ctx, "glossary"); err != nil {
			r.Rejected["*"] = err.Error()
			return r
		}
	}
	cache := map[string]string{}
	fetchErr := map[string]error{}
	for _, t := range terms {
		text, ok := cache[t.CheckURL]
		var err error
		if !ok {
			if err = fetchErr[t.CheckURL]; err == nil {
				text, err = in.sourceTextRetry(ctx, t.CheckURL)
				if err == nil {
					cache[t.CheckURL] = text
				} else {
					fetchErr[t.CheckURL] = err
				}
			}
		}
		// Falha de rede é transitória: o termo já validado continua valendo.
		if err != nil {
			r.Rejected[t.Term] = "fonte indisponível agora (mantida a validação anterior): " + err.Error()
			_ = in.Store.Event(ctx, "glossary_source_unreachable", map[string]string{"term": t.Term, "url": t.CheckURL, "error": err.Error()})
			slog.Warn("glossário: fonte fora do ar; termo mantido se já validado", "termo", t.Term, "erro", err)
			continue
		}
		// Conteúdo que não bate é motivo real: o termo sai do ar.
		if !strings.Contains(squash(text), squash(t.Check)) {
			err = fmt.Errorf("a fonte não contém a frase de verificação")
		} else if !textutil.ContainsPhrase(t.Definition, t.Term) && !containsAny(t.Definition, t.Aliases) {
			err = fmt.Errorf("a definição não cita o termo")
		}
		if err != nil {
			r.Rejected[t.Term] = err.Error()
			_ = in.Store.ExpireSeries(ctx, series(t.Term))
			_ = in.Store.Event(ctx, "glossary_invalid", map[string]string{"term": t.Term, "url": t.URL, "error": err.Error()})
			slog.Warn("glossário: termo fora", "termo", t.Term, "erro", err)
			continue
		}
		ents := make([]facts.Entity, len(t.Entities))
		for i, e := range t.Entities {
			ents[i] = facts.Entity{Name: e.Name, Type: e.Type}
		}
		f := facts.Fact{
			Kind: facts.Glossary, Claim: t.Definition, Entities: ents, AsOf: in.now(),
			SourceName: t.SourceName, SourceURL: t.URL, Series: series(t.Term), ExpiresAt: facts.NoExpiry,
		}
		if _, err := in.Store.UpsertFact(ctx, f); err != nil {
			r.Rejected[t.Term] = err.Error()
			continue
		}
		r.Loaded = append(r.Loaded, t.Term)
	}
	return r
}

// sourceTextRetry: 3 tentativas com espera crescente (sites oficiais oscilam).
func (in *Ingester) sourceTextRetry(ctx context.Context, url string) (string, error) {
	var err error
	for i, wait := range []time.Duration{0, 3 * time.Second, 8 * time.Second} {
		if i > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(wait):
			}
		}
		var text string
		if text, err = in.sourceText(ctx, url); err == nil {
			return text, nil
		}
	}
	return "", err
}

// sourceText baixa a fonte e devolve o texto. A API de páginas do BCB devolve
// JSON com "conteudo"; caminho inexistente vem com metatags nulas.
func (in *Ingester) sourceText(ctx context.Context, url string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	body, err := in.get(cctx, url)
	if err != nil {
		return "", err
	}
	trim := strings.TrimSpace(string(body))
	if strings.HasPrefix(trim, "{") {
		var page struct {
			Metatags *struct {
				Titulo string `json:"Titulo"`
			} `json:"metatags"`
			Conteudo string `json:"conteudo"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return "", fmt.Errorf("json: %w", err)
		}
		if page.Metatags == nil || strings.TrimSpace(page.Conteudo) == "" {
			return "", fmt.Errorf("página inexistente no BCB")
		}
		return textutil.StripHTML(page.Conteudo), nil
	}
	return textutil.StripHTML(string(body)), nil
}

// squash: sem acento, minúsculas, espaços simples e apóstrofo único.
func squash(s string) string {
	s = strings.NewReplacer("’", "'", "​", "", " ", " ").Replace(s)
	return strings.Join(strings.Fields(textutil.Fold(s)), " ")
}

func containsAny(text string, phrases []string) bool {
	for _, p := range phrases {
		if textutil.ContainsPhrase(text, p) {
			return true
		}
	}
	return false
}
