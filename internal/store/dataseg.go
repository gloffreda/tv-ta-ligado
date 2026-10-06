package store

import (
	"context"
	"time"

	"github.com/gloffreda/tv-ta-ligado/internal/facts"
)

// CreateDataSegment cria a pauta e o segmento de um bloco de dados (origin=data, sem LLM).
func (s *Store) CreateDataSegment(ctx context.Context, block string, at time.Time) (int64, error) {
	var id int64
	err := s.DB.QueryRow(ctx, `
		WITH r AS (INSERT INTO rundowns(block, slot_start, status) VALUES ($1,$2,'used') RETURNING id)
		INSERT INTO segments(rundown_id, block, created_at, origin) SELECT id, $1, $2, 'data' FROM r RETURNING id`, block, at).Scan(&id)
	return id, err
}

// RecentHeadlineFacts: um fato (o primeiro extraído) por matéria recente, ainda
// válido, de matéria que não foi a manchete de um segmento de dados há menos de reuse.
func (s *Store) RecentHeadlineFacts(ctx context.Context, now time.Time, window, reuse time.Duration, limit int) ([]facts.Fact, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT `+factCols+` FROM (
		  SELECT DISTINCT ON (f.article_id) f.* FROM facts f
		  WHERE f.kind='headline' AND f.article_id IS NOT NULL AND f.expires_at > $1 AND f.created_at >= $2 AND f.created_at <= $1
		    AND NOT EXISTS (
		      SELECT 1 FROM line_claims lc JOIN lines l ON l.id=lc.line_id JOIN segments sg ON sg.id=l.segment_id
		      JOIN facts f2 ON f2.id=lc.fact_id
		      WHERE sg.origin='data' AND sg.created_at >= $3 AND f2.article_id=f.article_id)
		  ORDER BY f.article_id, f.id) t
		ORDER BY created_at DESC, id LIMIT $4`, now, now.Add(-window), now.Add(-reuse), limit)
	if err != nil {
		return nil, err
	}
	return scanFacts(rows)
}

// DataStock: segmentos de dados aprovados do bloco, criados desde since, que
// ainda não entraram na linha do tempo (estoque pronto para o ar).
func (s *Store) DataStock(ctx context.Context, block string, since time.Time) (int, error) {
	var n int
	err := s.DB.QueryRow(ctx, `
		SELECT count(*) FROM segments s
		WHERE s.origin='data' AND s.block=$1 AND s.status='approved' AND s.created_at >= $2
		  AND NOT EXISTS (SELECT 1 FROM timeline t WHERE t.segment_id=s.id AND t.status <> 'skipped')`, block, since).Scan(&n)
	return n, err
}

// HeadlineArticles: matérias recentes cujo título pode virar manchete lida no
// ar (nenhum fato delas foi manchete de segmento de dados há menos de reuse).
func (s *Store) HeadlineArticles(ctx context.Context, now time.Time, window, reuse time.Duration, limit int) ([]Article, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT `+articleCols+`
		FROM articles a JOIN sources src ON src.id = a.source_id
		WHERE COALESCE(a.published_at, a.fetched_at) >= $1 AND COALESCE(a.published_at, a.fetched_at) <= $2
		  AND NOT EXISTS (
		    SELECT 1 FROM line_claims lc JOIN lines l ON l.id=lc.line_id JOIN segments sg ON sg.id=l.segment_id
		    JOIN facts f ON f.id=lc.fact_id
		    WHERE sg.origin='data' AND sg.created_at >= $3 AND f.article_id=a.id)
		ORDER BY COALESCE(a.published_at, a.fetched_at) DESC
		LIMIT $4`, now.Add(-window), now, now.Add(-reuse), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Article
	for rows.Next() {
		a, err := scanArticle(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
