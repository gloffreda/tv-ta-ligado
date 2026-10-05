package pipeline

// Extração preguiçosa: a ingestão guarda só título, resumo e (CC BY) corpo;
// os fatos de um artigo são extraídos só quando a pauta o escolhe.

import (
	"context"
	"log/slog"
	"time"

	"github.com/gloffreda/tv-ta-ligado/internal/facts"
	"github.com/gloffreda/tv-ta-ligado/internal/store"
)

// extractArticle chama o LLM, valida e grava. Falha vira evento extract_failed.
func (p *Pipeline) extractArticle(ctx context.Context, ex *facts.Extractor, a store.Article) (extracted, kept int, err error) {
	src := facts.Source{ArticleID: a.ID, Title: a.Title, Summary: a.Summary, URL: a.URL, Credit: a.SourceName, PublishedAt: a.FetchedAt}
	if a.Body != nil {
		src.Body = *a.Body
	}
	if a.PublishedAt != nil {
		src.PublishedAt = *a.PublishedAt
	}
	keptFacts, discarded, err := ex.Extract(ctx, src)
	if err != nil {
		slog.Warn("extração falhou", "artigo", a.ID, "erro", err)
		_ = p.Store.Event(context.WithoutCancel(ctx), "extract_failed", map[string]any{"article_id": a.ID, "url": a.URL, "error": err.Error()})
		return 0, 0, err
	}
	for _, f := range keptFacts {
		if _, err := p.Store.UpsertFact(ctx, f); err != nil {
			return 0, 0, err
		}
	}
	if err := p.Store.MarkExtracted(ctx, a.ID); err != nil {
		return 0, 0, err
	}
	extracted = len(keptFacts) + len(discarded)
	_ = p.Store.Event(ctx, "facts_extracted", map[string]any{"article_id": a.ID, "extracted": extracted, "kept": len(keptFacts), "discarded": discarded})
	return extracted, len(keptFacts), nil
}

// articleFacts devolve os fatos válidos do artigo, extraindo se ainda não foi feito.
func (p *Pipeline) articleFacts(ctx context.Context, ex *facts.Extractor, a store.Article, now time.Time) ([]facts.Fact, error) {
	done, err := p.Store.HasExtracted(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	if !done {
		if _, _, err := p.extractArticle(ctx, ex, a); err != nil {
			return nil, err
		}
	}
	return p.Store.FactsForArticle(ctx, a.ID, now)
}
