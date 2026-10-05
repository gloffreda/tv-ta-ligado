// Package store concentra o acesso ao Postgres.
package store

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gloffreda/tv-ta-ligado/internal/facts"
	"github.com/gloffreda/tv-ta-ligado/internal/llm"
	"github.com/gloffreda/tv-ta-ligado/migrations"
)

type Store struct{ DB *pgxpool.Pool }

func Open(ctx context.Context, url string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 6
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for i := 0; i < 30; i++ {
		if lastErr = pool.Ping(ctx); lastErr == nil {
			return &Store{DB: pool}, nil
		}
		time.Sleep(time.Second)
	}
	pool.Close()
	return nil, fmt.Errorf("postgres indisponível: %w", lastErr)
}

func (s *Store) Close() { s.DB.Close() }

// Migrate aplica, em ordem, os arquivos migrations/*.sql ainda não aplicados.
func (s *Store) Migrate(ctx context.Context) ([]string, error) {
	if _, err := s.DB.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return nil, err
	}
	names, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	var applied []string
	for _, n := range names {
		var exists bool
		if err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, n).Scan(&exists); err != nil {
			return applied, err
		}
		if exists {
			continue
		}
		sqlBytes, err := migrations.FS.ReadFile(n)
		if err != nil {
			return applied, err
		}
		err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, string(sqlBytes)); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES ($1)`, n)
			return err
		})
		if err != nil {
			return applied, fmt.Errorf("migração %s: %w", n, err)
		}
		applied = append(applied, n)
	}
	return applied, nil
}

// ---- fontes e artigos ----

func (s *Store) UpsertSource(ctx context.Context, name, kind, url, license string) (int64, error) {
	var id int64
	err := s.DB.QueryRow(ctx, `
		INSERT INTO sources(name, kind, url, license) VALUES ($1,$2,$3,$4)
		ON CONFLICT (name) DO UPDATE SET kind=EXCLUDED.kind, url=EXCLUDED.url, license=EXCLUDED.license
		RETURNING id`, name, kind, url, license).Scan(&id)
	return id, err
}

type Article struct {
	ID          int64
	SourceID    int64
	SourceName  string // crédito
	URL         string
	Title       string
	TitleHash   string
	Summary     string
	Body        *string
	PublishedAt *time.Time
	FetchedAt   time.Time
}

// InsertArticle devolve false quando a URL ou o título normalizado já existem.
func (s *Store) InsertArticle(ctx context.Context, a Article) (bool, error) {
	tag, err := s.DB.Exec(ctx, `
		INSERT INTO articles(source_id, url, title, title_hash, summary, body, published_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`,
		a.SourceID, a.URL, a.Title, a.TitleHash, a.Summary, a.Body, a.PublishedAt)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

const articleCols = `a.id, a.source_id, src.name, a.url, a.title, a.title_hash, a.summary, a.body, a.published_at, a.fetched_at`

func scanArticle(r pgx.Row) (Article, error) {
	var a Article
	err := r.Scan(&a.ID, &a.SourceID, &a.SourceName, &a.URL, &a.Title, &a.TitleHash, &a.Summary, &a.Body, &a.PublishedAt, &a.FetchedAt)
	return a, err
}

// CandidateArticles: artigos publicados depois de since e ainda não usados
// em nenhuma pauta desde usedSince.
func (s *Store) CandidateArticles(ctx context.Context, since, usedSince time.Time, limit int) ([]Article, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT `+articleCols+`
		FROM articles a JOIN sources src ON src.id = a.source_id
		WHERE COALESCE(a.published_at, a.fetched_at) >= $1
		  AND NOT EXISTS (
		    SELECT 1 FROM rundown_items ri JOIN rundowns r ON r.id = ri.rundown_id
		    WHERE ri.article_id = a.id AND r.slot_start >= $2)
		ORDER BY COALESCE(a.published_at, a.fetched_at) DESC
		LIMIT $3`, since, usedSince, limit)
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

func (s *Store) ArticlesByIDs(ctx context.Context, ids []int64) ([]Article, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+articleCols+` FROM articles a JOIN sources src ON src.id=a.source_id WHERE a.id = ANY($1)`, ids)
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

// ---- fatos ----

// UpsertFact grava o fato; se o mesmo dado já existe, renova a validade.
func (s *Store) UpsertFact(ctx context.Context, f facts.Fact) (int64, error) {
	ents, _ := json.Marshal(nonNil(f.Entities))
	var id int64
	err := s.DB.QueryRow(ctx, `
		INSERT INTO facts(article_id, kind, claim, entities, value, unit, as_of, source_name, source_url, series, expires_at, fingerprint)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,''),$11,$12)
		ON CONFLICT (fingerprint) DO UPDATE SET expires_at = GREATEST(facts.expires_at, EXCLUDED.expires_at)
		RETURNING id`,
		f.ArticleID, string(f.Kind), f.Claim, ents, f.Value, f.Unit, f.AsOf, f.SourceName, f.SourceURL, f.Series, f.ExpiresAt, f.Fingerprint()).Scan(&id)
	return id, err
}

func nonNilIDs(s []int64) []int64 {
	if s == nil {
		return []int64{}
	}
	return s
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

const (
	factCols  = `id, article_id, kind, claim, entities, value::float8, unit, as_of, source_name, source_url, COALESCE(series, ''), created_at, expires_at`
	factColsF = `f.id, f.article_id, f.kind, f.claim, f.entities, f.value::float8, f.unit, f.as_of, f.source_name, f.source_url, COALESCE(f.series, ''), f.created_at, f.expires_at`
)

func scanFacts(rows pgx.Rows) ([]facts.Fact, error) {
	defer rows.Close()
	var out []facts.Fact
	for rows.Next() {
		var f facts.Fact
		var kind string
		var ents []byte
		if err := rows.Scan(&f.ID, &f.ArticleID, &kind, &f.Claim, &ents, &f.Value, &f.Unit, &f.AsOf, &f.SourceName, &f.SourceURL, &f.Series, &f.CreatedAt, &f.ExpiresAt); err != nil {
			return nil, err
		}
		f.Kind = facts.Kind(kind)
		_ = json.Unmarshal(ents, &f.Entities)
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) FactsByIDs(ctx context.Context, ids []int64) (map[int64]facts.Fact, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+factCols+` FROM facts WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	list, err := scanFacts(rows)
	if err != nil {
		return nil, err
	}
	m := make(map[int64]facts.Fact, len(list))
	for _, f := range list {
		m[f.ID] = f
	}
	return m, nil
}

func (s *Store) FactsForArticle(ctx context.Context, articleID int64, now time.Time) ([]facts.Fact, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+factCols+` FROM facts WHERE article_id=$1 AND expires_at > $2 ORDER BY id`, articleID, now)
	if err != nil {
		return nil, err
	}
	return scanFacts(rows)
}

// HasExtracted: o artigo já passou pela extração (mesmo que sem fatos válidos).
func (s *Store) HasExtracted(ctx context.Context, articleID int64) (bool, error) {
	var ok bool
	err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM system_events WHERE kind='facts_extracted' AND (detail->>'article_id')::bigint=$1)`, articleID).Scan(&ok)
	return ok, err
}

// LatestFactsByKind: o fato válido mais recente de cada série.
func (s *Store) LatestFactsByKind(ctx context.Context, kind facts.Kind, now time.Time) ([]facts.Fact, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT `+factCols+` FROM (
		  SELECT DISTINCT ON (COALESCE(series, source_url)) * FROM facts
		  WHERE kind=$1 AND expires_at > $2
		  ORDER BY COALESCE(series, source_url), as_of DESC, id DESC) t
		ORDER BY id`, string(kind), now)
	if err != nil {
		return nil, err
	}
	return scanFacts(rows)
}

// AllEntities: todas as entidades conhecidas no banco (para barrar nomes reais).
func (s *Store) AllEntities(ctx context.Context) ([]string, error) {
	rows, err := s.DB.Query(ctx, `SELECT DISTINCT jsonb_array_elements_text(entities) FROM facts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---- pauta ----

func (s *Store) CreateRundown(ctx context.Context, block string, slot time.Time) (int64, error) {
	var id int64
	err := s.DB.QueryRow(ctx, `INSERT INTO rundowns(block, slot_start) VALUES ($1,$2) RETURNING id`, block, slot).Scan(&id)
	return id, err
}

type RundownItem struct {
	ArticleID *int64
	FactID    *int64
	Rank      int
}

func (s *Store) AddRundownItems(ctx context.Context, rundownID int64, items []RundownItem) error {
	for _, it := range items {
		if _, err := s.DB.Exec(ctx, `INSERT INTO rundown_items(rundown_id, article_id, fact_id, rank) VALUES ($1,$2,$3,$4)`,
			rundownID, it.ArticleID, it.FactID, it.Rank); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) SetRundownStatus(ctx context.Context, id int64, status string) error {
	_, err := s.DB.Exec(ctx, `UPDATE rundowns SET status=$2 WHERE id=$1`, id, status)
	return err
}

// ---- segmentos e falas ----

func (s *Store) CreateSegment(ctx context.Context, rundownID int64, block string) (int64, error) {
	var id int64
	err := s.DB.QueryRow(ctx, `INSERT INTO segments(rundown_id, block) VALUES ($1,$2) RETURNING id`, rundownID, block).Scan(&id)
	return id, err
}

func (s *Store) SetSegmentAttempts(ctx context.Context, id int64, attempts int) error {
	_, err := s.DB.Exec(ctx, `UPDATE segments SET attempts=$2 WHERE id=$1`, id, attempts)
	return err
}

func (s *Store) FinishSegment(ctx context.Context, id int64, status string, reason string) error {
	var r *string
	if reason != "" {
		r = &reason
	}
	_, err := s.DB.Exec(ctx, `
		UPDATE segments SET status=$2, reject_reason=$3,
		  cost_usd = (SELECT COALESCE(SUM(cost_usd),0) FROM llm_calls WHERE segment_id=$1)
		WHERE id=$1`, id, status, r)
	return err
}

type SegmentInfo struct {
	ID        int64
	RundownID int64
	Block     string
	Status    string
	Attempts  int
	CreatedAt time.Time
}

func (s *Store) Segment(ctx context.Context, id int64) (SegmentInfo, error) {
	var si SegmentInfo
	err := s.DB.QueryRow(ctx, `SELECT id, rundown_id, block, status, attempts, created_at FROM segments WHERE id=$1`, id).
		Scan(&si.ID, &si.RundownID, &si.Block, &si.Status, &si.Attempts, &si.CreatedAt)
	return si, err
}

// RundownFacts: fatos válidos da pauta (dos artigos e os avulsos), em ordem de rank.
func (s *Store) RundownFacts(ctx context.Context, rundownID int64, now time.Time) ([]facts.Fact, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT `+factColsF+`
		FROM rundown_items ri
		JOIN facts f ON (f.id = ri.fact_id OR (ri.article_id IS NOT NULL AND f.article_id = ri.article_id))
		WHERE ri.rundown_id=$1 AND f.expires_at > $2
		ORDER BY ri.rank, f.id`, rundownID, now)
	if err != nil {
		return nil, err
	}
	return scanFacts(rows)
}

// RefreshSegmentCost recalcula o custo (ex.: depois da extração de memória).
func (s *Store) RefreshSegmentCost(ctx context.Context, id int64) error {
	_, err := s.DB.Exec(ctx, `UPDATE segments SET cost_usd=(SELECT COALESCE(SUM(cost_usd),0) FROM llm_calls WHERE segment_id=$1) WHERE id=$1`, id)
	return err
}

type Line struct {
	ID           int64
	SegmentID    int64
	Seq          int
	Speaker      string
	Type         string
	Text         string
	OriginalText *string
	Status       string
	RejectReason *string
	FactIDs      []int64 // como veio do roteiro
}

func (s *Store) InsertLine(ctx context.Context, l Line) (int64, error) {
	var id int64
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO lines(segment_id, seq, speaker, type, text, fact_ids) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
			l.SegmentID, l.Seq, l.Speaker, l.Type, l.Text, nonNilIDs(l.FactIDs)).Scan(&id); err != nil {
			return err
		}
		return insertClaims(ctx, tx, id, l.FactIDs)
	})
	return id, err
}

// insertClaims só grava fact_ids que existem (os inválidos já reprovam na checagem).
func insertClaims(ctx context.Context, tx pgx.Tx, lineID int64, ids []int64) error {
	for _, fid := range ids {
		if _, err := tx.Exec(ctx, `INSERT INTO line_claims(line_id, fact_id) SELECT $1, id FROM facts WHERE id=$2 ON CONFLICT DO NOTHING`, lineID, fid); err != nil {
			return err
		}
	}
	return nil
}

// UpdateLine grava o resultado da checagem (e a reescrita, se houve).
func (s *Store) UpdateLine(ctx context.Context, lineID int64, status, text string, original *string, reason string, factIDs []int64) error {
	var r *string
	if reason != "" {
		r = &reason
	}
	return pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE lines SET status=$2, text=$3, original_text=$4, reject_reason=$5, fact_ids=$6 WHERE id=$1`,
			lineID, status, text, original, r, nonNilIDs(factIDs)); err != nil {
			return err
		}
		if original == nil {
			return nil
		}
		if _, err := tx.Exec(ctx, `DELETE FROM line_claims WHERE line_id=$1`, lineID); err != nil {
			return err
		}
		return insertClaims(ctx, tx, lineID, factIDs)
	})
}

func (s *Store) LogCheck(ctx context.Context, lineID int64, attempt int, stage string, passed bool, detail any) error {
	b, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(ctx, `INSERT INTO check_log(line_id, attempt, stage, passed, detail) VALUES ($1,$2,$3,$4,$5)`, lineID, attempt, stage, passed, b)
	return err
}

// LastSegmentAt: momento do último segmento do bloco com algum dos status.
func (s *Store) LastSegmentAt(ctx context.Context, block string, statuses ...string) (time.Time, bool, error) {
	var t *time.Time
	err := s.DB.QueryRow(ctx, `SELECT max(created_at) FROM segments WHERE block=$1 AND status = ANY($2)`, block, statuses).Scan(&t)
	if err != nil || t == nil {
		return time.Time{}, false, err
	}
	return *t, true, nil
}

// ---- memória ----

type Memory struct {
	ID        int64
	Persona   string
	Kind      string
	Content   string
	Weight    float64
	CreatedAt time.Time
}

func (s *Store) InsertMemory(ctx context.Context, m Memory, segmentID int64) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO persona_memory(persona, kind, content, source_segment_id, weight) VALUES ($1,$2,$3,$4,$5)`,
		m.Persona, m.Kind, m.Content, segmentID, m.Weight)
	return err
}

// TopMemories: peso com decaimento exponencial (meia-vida em dias).
func (s *Store) TopMemories(ctx context.Context, n int, halfLifeDays float64, now time.Time) ([]Memory, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT id, persona, kind, content,
		       weight * power(0.5, extract(epoch FROM ($3::timestamptz - created_at)) / 86400.0 / $2) AS w, created_at
		FROM persona_memory ORDER BY w DESC, created_at DESC LIMIT $1`, n, halfLifeDays, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Memory
	for rows.Next() {
		var m Memory
		if err := rows.Scan(&m.ID, &m.Persona, &m.Kind, &m.Content, &m.Weight, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ---- LLM e eventos ----

func (s *Store) RecordLLMCall(ctx context.Context, c llm.LLMCall) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO llm_calls(purpose, model, input_tokens, output_tokens, cost_usd, segment_id) VALUES ($1,$2,$3,$4,$5,$6)`,
		c.Purpose, c.Model, c.InputTokens, c.OutputTokens, c.CostUSD, c.SegmentID)
	return err
}

func (s *Store) SpentSince(ctx context.Context, since time.Time) (float64, error) {
	var v float64
	err := s.DB.QueryRow(ctx, `SELECT COALESCE(SUM(cost_usd),0)::float8 FROM llm_calls WHERE created_at >= $1`, since).Scan(&v)
	return v, err
}

func (s *Store) Event(ctx context.Context, kind string, detail any) error {
	b, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(ctx, `INSERT INTO system_events(kind, detail) VALUES ($1,$2)`, kind, b)
	return err
}

// ---- leitura para o `show` ----

type SegmentView struct {
	ID        int64
	Block     string
	Status    string
	Attempts  int
	CostUSD   float64
	CreatedAt time.Time
	Reason    *string
	Lines     []LineView
}

type LineView struct {
	Line
	Sources []facts.Fact
}

func (s *Store) Segments(ctx context.Context, status string, limit int) ([]SegmentView, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, block, status, attempts, cost_usd::float8, created_at, reject_reason FROM segments WHERE status=$1 ORDER BY created_at DESC LIMIT $2`, status, limit)
	if err != nil {
		return nil, err
	}
	var segs []SegmentView
	for rows.Next() {
		var v SegmentView
		if err := rows.Scan(&v.ID, &v.Block, &v.Status, &v.Attempts, &v.CostUSD, &v.CreatedAt, &v.Reason); err != nil {
			rows.Close()
			return nil, err
		}
		segs = append(segs, v)
	}
	rows.Close()
	for i := range segs {
		ls, err := s.LinesOf(ctx, segs[i].ID)
		if err != nil {
			return nil, err
		}
		segs[i].Lines = ls
	}
	return segs, nil
}

func (s *Store) LinesOf(ctx context.Context, segmentID int64) ([]LineView, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, segment_id, seq, speaker, type, text, fact_ids, original_text, status, reject_reason FROM lines WHERE segment_id=$1 ORDER BY seq`, segmentID)
	if err != nil {
		return nil, err
	}
	var out []LineView
	for rows.Next() {
		var l LineView
		if err := rows.Scan(&l.ID, &l.SegmentID, &l.Seq, &l.Speaker, &l.Type, &l.Text, &l.FactIDs, &l.OriginalText, &l.Status, &l.RejectReason); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, l)
	}
	rows.Close()
	for i := range out {
		frows, err := s.DB.Query(ctx, `SELECT `+factColsF+` FROM facts f JOIN line_claims lc ON lc.fact_id=f.id WHERE lc.line_id=$1 ORDER BY f.id`, out[i].ID)
		if err != nil {
			return nil, err
		}
		fs, err := scanFacts(frows)
		if err != nil {
			return nil, err
		}
		out[i].Sources = fs
	}
	return out, nil
}

// BlockStats: falas fact/banter e cortes por bloco (para o relatório).
type BlockStats struct {
	Block      string
	Segments   int
	Approved   int
	Lines      int
	Dropped    int
	Rewritten  int
	AvgCostUSD float64
}

func (s *Store) Stats(ctx context.Context) ([]BlockStats, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT s.block, count(DISTINCT s.id), count(DISTINCT s.id) FILTER (WHERE s.status='approved'),
		       count(l.id), count(l.id) FILTER (WHERE l.status='dropped'), count(l.id) FILTER (WHERE l.status='rewritten'),
		       COALESCE((SELECT avg(cost_usd)::float8 FROM segments s2 WHERE s2.block=s.block AND s2.status<>'draft'),0)
		FROM segments s LEFT JOIN lines l ON l.segment_id=s.id
		WHERE s.status<>'draft'
		GROUP BY s.block ORDER BY s.block`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BlockStats
	for rows.Next() {
		var b BlockStats
		if err := rows.Scan(&b.Block, &b.Segments, &b.Approved, &b.Lines, &b.Dropped, &b.Rewritten, &b.AvgCostUSD); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
