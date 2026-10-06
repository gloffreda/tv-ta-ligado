package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

type TimelineLine struct {
	Seq        int             `json:"seq"`
	LineID     *int64          `json:"line_id,omitempty"`
	Speaker    string          `json:"speaker"`
	Type       string          `json:"type"`
	Text       string          `json:"text"`
	SpokenText string          `json:"spoken_text"`
	AudioHash  string          `json:"audio_hash"`
	OffsetMS   int             `json:"offset_ms"`
	DurationMS int             `json:"duration_ms"`
	Visemes    json.RawMessage `json:"visemes,omitempty"`
	Sources    []Source        `json:"sources,omitempty"`
}

type Source struct {
	Name  string `json:"name"`
	URL   string `json:"url"`
	Title string `json:"title,omitempty"` // título da matéria (fatos de notícia)
}

type TimelineItem struct {
	ID        int64          `json:"id"`
	Kind      string         `json:"kind"`
	SegmentID *int64         `json:"segment_id,omitempty"`
	Block     *string        `json:"block,omitempty"`
	StartsAt  time.Time      `json:"starts_at"`
	EndsAt    time.Time      `json:"ends_at"`
	Status    string         `json:"status"`
	Lines     []TimelineLine `json:"lines"`
}

// TimelineEnd: fim do último item agendado.
func (s *Store) TimelineEnd(ctx context.Context) (time.Time, bool, error) {
	var t *time.Time
	err := s.DB.QueryRow(ctx, `SELECT max(ends_at) FROM timeline WHERE status <> 'skipped'`).Scan(&t)
	if err != nil || t == nil {
		return time.Time{}, false, err
	}
	return *t, true, nil
}

// InsertTimelineItem grava o item e suas falas numa transação.
func (s *Store) InsertTimelineItem(ctx context.Context, it TimelineItem) (int64, error) {
	var id int64
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO timeline(kind, segment_id, block, starts_at, ends_at) VALUES ($1,$2,$3,$4,$5) RETURNING id`,
			it.Kind, it.SegmentID, it.Block, it.StartsAt, it.EndsAt).Scan(&id); err != nil {
			return err
		}
		for _, l := range it.Lines {
			if _, err := tx.Exec(ctx, `INSERT INTO timeline_lines(timeline_id, seq, line_id, speaker, type, text, spoken_text, audio_hash, offset_ms, duration_ms)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, id, l.Seq, l.LineID, l.Speaker, l.Type, l.Text, l.SpokenText, l.AudioHash, l.OffsetMS, l.DurationMS); err != nil {
				return err
			}
		}
		return nil
	})
	return id, err
}

// LastStartByBlock: último início agendado (estreia ou reprise) de cada bloco.
func (s *Store) LastStartByBlock(ctx context.Context) (map[string]time.Time, error) {
	rows, err := s.DB.Query(ctx, `SELECT block, max(starts_at) FROM timeline WHERE block IS NOT NULL AND status <> 'skipped' GROUP BY block`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var b string
		var t time.Time
		if err := rows.Scan(&b, &t); err != nil {
			return nil, err
		}
		out[b] = t
	}
	return out, rows.Err()
}

type ReadySegment struct {
	ID        int64
	Block     string
	CreatedAt time.Time
}

// voicedApproved: aprovado e com áudio em todas as falas que vão ao ar.
const voicedApproved = `s.status='approved'
  AND EXISTS (SELECT 1 FROM lines l WHERE l.segment_id=s.id AND l.status IN ('ok','rewritten'))
  AND NOT EXISTS (SELECT 1 FROM lines l LEFT JOIN line_audio la ON la.line_id=l.id
                  WHERE l.segment_id=s.id AND l.status IN ('ok','rewritten') AND la.line_id IS NULL)`

// factsValidAt: todas as falas que vão ao ar citam fatos válidos em $at.
const factsValidAt = `NOT EXISTS (SELECT 1 FROM lines l JOIN line_claims lc ON lc.line_id=l.id JOIN facts f ON f.id=lc.fact_id
	WHERE l.segment_id=s.id AND l.status IN ('ok','rewritten') AND f.expires_at <= $1)`

// FreshSegments: aprovados, com voz, nunca agendados (itens tirados do ar não
// contam), criados nas últimas 24 h, de uma origem (llm ou data), com fatos
// válidos em `at`.
func (s *Store) FreshSegments(ctx context.Context, at time.Time, origin string) ([]ReadySegment, error) {
	return s.ready(ctx, `SELECT s.id, s.block, s.created_at FROM segments s WHERE `+voicedApproved+`
		AND s.origin = $2 AND s.created_at >= $3 AND `+factsValidAt+`
		AND NOT EXISTS (SELECT 1 FROM timeline t WHERE t.segment_id=s.id AND t.status <> 'skipped')
		ORDER BY s.created_at, s.id`, at, origin, at.Add(-24*time.Hour))
}

// ReplayCandidates: aprovados com voz que já foram ao ar, criados na janela,
// sem exibição nos últimos `gap` antes de `at` e com fatos válidos em `at`.
func (s *Store) ReplayCandidates(ctx context.Context, at time.Time, window, gap time.Duration) ([]ReadySegment, error) {
	return s.ready(ctx, `SELECT s.id, s.block, s.created_at FROM segments s WHERE `+voicedApproved+`
		AND s.created_at >= $2 AND `+factsValidAt+`
		AND EXISTS (SELECT 1 FROM timeline t WHERE t.segment_id=s.id AND t.status <> 'skipped' AND t.starts_at <= $1)
		AND NOT EXISTS (SELECT 1 FROM timeline t WHERE t.segment_id=s.id AND t.status <> 'skipped' AND t.starts_at > $3)
		ORDER BY (SELECT max(starts_at) FROM timeline t WHERE t.segment_id=s.id AND t.status <> 'skipped') NULLS FIRST,
		         (SELECT count(*) FROM timeline t WHERE t.segment_id=s.id AND t.status <> 'skipped'), s.id DESC`,
		at, at.Add(-window), at.Add(-gap))
}

// HasHistory: houve algum item no ar entre `at-6h` e `at-60min` (senão é partida a frio).
func (s *Store) HasHistory(ctx context.Context, at time.Time) (bool, error) {
	var ok bool
	err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM timeline WHERE status <> 'skipped' AND kind IN ('segment','data','replay')
		AND starts_at > $1 AND starts_at <= $2)`, at.Add(-6*time.Hour), at.Add(-time.Hour)).Scan(&ok)
	return ok, err
}

func (s *Store) ready(ctx context.Context, q string, args ...any) ([]ReadySegment, error) {
	rows, err := s.DB.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReadySegment
	for rows.Next() {
		var r ReadySegment
		if err := rows.Scan(&r.ID, &r.Block, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SegmentAudioLines: falas que vão ao ar, na ordem, com o áudio.
func (s *Store) SegmentAudioLines(ctx context.Context, segID int64) ([]TimelineLine, error) {
	rows, err := s.DB.Query(ctx, `SELECT l.seq, l.id, l.speaker, l.type, l.text, COALESCE(l.spoken_text, l.text), la.hash, la.duration_ms
		FROM lines l JOIN line_audio la ON la.line_id=l.id
		WHERE l.segment_id=$1 AND l.status IN ('ok','rewritten') ORDER BY l.seq`, segID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TimelineLine
	for rows.Next() {
		var l TimelineLine
		var id int64
		if err := rows.Scan(&l.Seq, &id, &l.Speaker, &l.Type, &l.Text, &l.SpokenText, &l.AudioHash, &l.DurationMS); err != nil {
			return nil, err
		}
		l.LineID = &id
		out = append(out, l)
	}
	return out, rows.Err()
}

// MarkAired: itens que já terminaram viram 'aired' (horários não mudam).
func (s *Store) MarkAired(ctx context.Context, now time.Time) error {
	_, err := s.DB.Exec(ctx, `UPDATE timeline SET status='aired' WHERE status='scheduled' AND ends_at <= $1`, now)
	return err
}

// TimelineRange: itens que cruzam [from, to), com falas, visemas e fontes.
func (s *Store) TimelineRange(ctx context.Context, from, to time.Time) ([]TimelineItem, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, kind, segment_id, block, starts_at, ends_at, status FROM timeline
		WHERE ends_at > $1 AND starts_at < $2 AND status <> 'skipped' ORDER BY starts_at`, from, to)
	if err != nil {
		return nil, err
	}
	var items []TimelineItem
	for rows.Next() {
		var it TimelineItem
		if err := rows.Scan(&it.ID, &it.Kind, &it.SegmentID, &it.Block, &it.StartsAt, &it.EndsAt, &it.Status); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, it)
	}
	rows.Close()
	for i := range items {
		ls, err := s.timelineLines(ctx, items[i].ID)
		if err != nil {
			return nil, err
		}
		items[i].Lines = ls
	}
	return items, nil
}

func (s *Store) timelineLines(ctx context.Context, itemID int64) ([]TimelineLine, error) {
	rows, err := s.DB.Query(ctx, `SELECT tl.seq, tl.line_id, tl.speaker, tl.type, tl.text, tl.spoken_text, tl.audio_hash, tl.offset_ms, tl.duration_ms, aa.visemes
		FROM timeline_lines tl JOIN audio_assets aa ON aa.hash=tl.audio_hash WHERE tl.timeline_id=$1 ORDER BY tl.seq`, itemID)
	if err != nil {
		return nil, err
	}
	var out []TimelineLine
	for rows.Next() {
		var l TimelineLine
		if err := rows.Scan(&l.Seq, &l.LineID, &l.Speaker, &l.Type, &l.Text, &l.SpokenText, &l.AudioHash, &l.OffsetMS, &l.DurationMS, &l.Visemes); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, l)
	}
	rows.Close()
	for i := range out {
		if out[i].Type != "fact" || out[i].LineID == nil {
			continue
		}
		srows, err := s.DB.Query(ctx, `SELECT DISTINCT f.source_name, f.source_url, COALESCE(a.title, '') FROM line_claims lc JOIN facts f ON f.id=lc.fact_id
			LEFT JOIN articles a ON a.id=f.article_id WHERE lc.line_id=$1 ORDER BY 1, 2`, *out[i].LineID)
		if err != nil {
			return nil, err
		}
		for srows.Next() {
			var src Source
			if err := srows.Scan(&src.Name, &src.URL, &src.Title); err != nil {
				srows.Close()
				return nil, err
			}
			out[i].Sources = append(out[i].Sources, src)
		}
		srows.Close()
	}
	return out, nil
}

// TimelineGaps: buracos (início de um item depois do fim do anterior) em [from, to).
func (s *Store) TimelineGaps(ctx context.Context, from, to time.Time) (int, time.Duration, error) {
	var n int
	var total float64
	err := s.DB.QueryRow(ctx, `SELECT count(*), COALESCE(sum(extract(epoch FROM gap)),0) FROM (
		SELECT starts_at - lag(ends_at) OVER (ORDER BY starts_at) AS gap FROM timeline WHERE starts_at >= $1 AND starts_at < $2 AND status <> 'skipped') g
		WHERE gap > interval '0'`, from, to).Scan(&n, &total)
	return n, time.Duration(total * float64(time.Second)), err
}

// MaxTimelineID ajuda o SSE a detectar itens novos.
func (s *Store) MaxTimelineID(ctx context.Context) (int64, error) {
	var id *int64
	err := s.DB.QueryRow(ctx, `SELECT max(id) FROM timeline WHERE status <> 'skipped'`).Scan(&id)
	if id == nil {
		return 0, err
	}
	return *id, err
}

// TimelineKinds: itens por tipo que começam em [from, to).
func (s *Store) TimelineKinds(ctx context.Context, from, to time.Time) (map[string]int, error) {
	rows, err := s.DB.Query(ctx, `SELECT kind, count(*) FROM timeline WHERE starts_at >= $1 AND starts_at < $2 AND status <> 'skipped' GROUP BY kind`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		out[k] = n
	}
	return out, rows.Err()
}
