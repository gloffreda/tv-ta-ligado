package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/gloffreda/tv-ta-ligado/internal/config"
	"github.com/gloffreda/tv-ta-ligado/internal/facts"
	"github.com/gloffreda/tv-ta-ligado/internal/llm"
	"github.com/gloffreda/tv-ta-ligado/internal/store"
)

// ExtractLimit: no máximo tantos artigos por ciclo, os mais recentes primeiro.
const ExtractLimit = 40

type ExtractReport struct {
	Articles  int    // processados com sucesso
	Extracted int    // fatos propostos pelo LLM
	Kept      int    // aceitos pela validação literal
	Failed    int    // artigos com erro de LLM
	Stopped   string // motivo de parada antecipada, se houve
}

// ExtractPending extrai fatos dos artigos ainda não processados. Não é
// geração de conteúdo: roda mesmo com GENERATE=off, mas para durante o
// bloqueio eleitoral e respeita o teto diário. Erros viram system_events.
func (p *Pipeline) ExtractPending(ctx context.Context, limit int) (ExtractReport, error) {
	var r ExtractReport
	bo, err := config.LoadBlackout(p.ConfigDir)
	if err != nil {
		return r, fmt.Errorf("blackout.yaml: %w", err)
	}
	if w, ok := bo.Active(p.now()); ok {
		r.Stopped = "bloqueio eleitoral: " + w.Name
		_ = p.Store.Event(ctx, "extraction_paused", map[string]string{"reason": r.Stopped})
		return r, nil
	}
	if p.Budget != nil {
		if err := p.Budget.CheckBudget(ctx); err != nil {
			r.Stopped = err.Error()
			return r, nil // o aviso de teto já foi registrado pelo Metered
		}
	}
	arts, err := p.Store.PendingExtraction(ctx, p.now().Add(-facts.TTL(facts.Headline)), limit)
	if err != nil {
		return r, err
	}
	ex := &facts.Extractor{LLM: p.LLM, Model: p.Env.ModelFast, Now: p.Now}
	for _, a := range arts {
		extracted, kept, err := p.extractArticle(ctx, ex, a)
		if err != nil {
			r.Failed++
			if llm.Fatal(err) {
				r.Stopped = err.Error()
				break
			}
			continue
		}
		r.Articles++
		r.Extracted += extracted
		r.Kept += kept
	}
	slog.Info("extração de fatos", "artigos", r.Articles, "propostos", r.Extracted, "aceitos", r.Kept, "falhas", r.Failed, "parada", r.Stopped)
	return r, nil
}

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
