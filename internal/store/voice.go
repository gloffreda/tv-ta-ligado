package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

// AudioAsset é um arquivo de áudio sintetizado (cache por hash).
type AudioAsset struct {
	Hash       string
	Path       string
	DurationMS int
	Provider   string
	Voice      string
	SpokenText string
	Visemes    json.RawMessage
	CostUSD    float64
}

func (s *Store) AudioAsset(ctx context.Context, hash string) (AudioAsset, bool, error) {
	var a AudioAsset
	err := s.DB.QueryRow(ctx, `SELECT hash, path, duration_ms, provider, voice, spoken_text, visemes, cost_usd::float8 FROM audio_assets WHERE hash=$1`, hash).
		Scan(&a.Hash, &a.Path, &a.DurationMS, &a.Provider, &a.Voice, &a.SpokenText, &a.Visemes, &a.CostUSD)
	if err == pgx.ErrNoRows {
		return a, false, nil
	}
	return a, err == nil, err
}

func (s *Store) InsertAudioAsset(ctx context.Context, a AudioAsset) error {
	if len(a.Visemes) == 0 {
		a.Visemes = json.RawMessage("[]")
	}
	_, err := s.DB.Exec(ctx, `INSERT INTO audio_assets(hash, path, duration_ms, provider, voice, spoken_text, visemes, cost_usd)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (hash) DO NOTHING`,
		a.Hash, a.Path, a.DurationMS, a.Provider, a.Voice, a.SpokenText, a.Visemes, a.CostUSD)
	return err
}

// SetLineAudio liga a fala ao áudio (cost: 0 se veio do cache).
func (s *Store) SetLineAudio(ctx context.Context, lineID int64, spoken string, a AudioAsset, cost float64) error {
	if len(a.Visemes) == 0 {
		a.Visemes = json.RawMessage("[]")
	}
	return pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE lines SET spoken_text=$2 WHERE id=$1`, lineID, spoken); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO line_audio(line_id, hash, path, duration_ms, provider, voice, visemes, cost_usd)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (line_id) DO UPDATE SET hash=EXCLUDED.hash, path=EXCLUDED.path, duration_ms=EXCLUDED.duration_ms,
			  provider=EXCLUDED.provider, voice=EXCLUDED.voice, visemes=EXCLUDED.visemes, cost_usd=EXCLUDED.cost_usd`,
			lineID, a.Hash, a.Path, a.DurationMS, a.Provider, a.Voice, a.Visemes, cost)
		return err
	})
}

// LineToVoice é uma fala que vai ao ar (ok/rewritten) e ainda não tem áudio.
type LineToVoice struct {
	ID      int64
	Seq     int
	Speaker string
	Type    string
	Text    string
}

func (s *Store) LinesToVoice(ctx context.Context, segmentID int64) ([]LineToVoice, error) {
	rows, err := s.DB.Query(ctx, `SELECT l.id, l.seq, l.speaker, l.type, l.text FROM lines l
		LEFT JOIN line_audio la ON la.line_id=l.id
		WHERE l.segment_id=$1 AND l.status IN ('ok','rewritten') AND la.line_id IS NULL ORDER BY l.seq`, segmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LineToVoice
	for rows.Next() {
		var l LineToVoice
		if err := rows.Scan(&l.ID, &l.Seq, &l.Speaker, &l.Type, &l.Text); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// UnvoicedApproved: segmentos aprovados desde t com alguma fala sem áudio.
func (s *Store) UnvoicedApproved(ctx context.Context, since time.Time) ([]int64, error) {
	rows, err := s.DB.Query(ctx, `SELECT DISTINCT s.id FROM segments s JOIN lines l ON l.segment_id=s.id
		LEFT JOIN line_audio la ON la.line_id=l.id
		WHERE s.status='approved' AND s.created_at >= $1 AND l.status IN ('ok','rewritten') AND la.line_id IS NULL
		ORDER BY s.id`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
