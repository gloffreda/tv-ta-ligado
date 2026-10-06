package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Session é uma sessão sob demanda: o canal só trabalha dentro de uma.
type Session struct {
	ID          int64      `json:"id"`
	Status      string     `json:"status"`
	StartedAt   time.Time  `json:"started_at"`
	Deadline    time.Time  `json:"deadline"`
	MaxUSD      float64    `json:"max_usd"`
	EndedAt     *time.Time `json:"ended_at,omitempty"`
	EndReason   *string    `json:"end_reason,omitempty"`
	SpentUSD    float64    `json:"spent_usd"`
	OriginIP    string     `json:"-"`
	UserAgent   string     `json:"-"`
	FirstLineAt *time.Time `json:"first_line_at,omitempty"`
}

const sessionCols = `id, status, started_at, deadline, max_usd::float8, ended_at, end_reason, spent_usd::float8, origin_ip, user_agent, first_line_at`

func scanSession(r pgx.Row) (Session, error) {
	var s Session
	err := r.Scan(&s.ID, &s.Status, &s.StartedAt, &s.Deadline, &s.MaxUSD, &s.EndedAt, &s.EndReason, &s.SpentUSD, &s.OriginIP, &s.UserAgent, &s.FirstLineAt)
	return s, err
}

// ActiveSession devolve a sessão ativa, se houver.
func (s *Store) ActiveSession(ctx context.Context) (Session, bool, error) {
	ses, err := scanSession(s.DB.QueryRow(ctx, `SELECT `+sessionCols+` FROM sessions WHERE status='active'`))
	if errors.Is(err, pgx.ErrNoRows) {
		return ses, false, nil
	}
	return ses, err == nil, err
}

// LastSession: a mais recente (ativa ou encerrada).
func (s *Store) LastSession(ctx context.Context) (Session, bool, error) {
	ses, err := scanSession(s.DB.QueryRow(ctx, `SELECT `+sessionCols+` FROM sessions ORDER BY id DESC LIMIT 1`))
	if errors.Is(err, pgx.ErrNoRows) {
		return ses, false, nil
	}
	return ses, err == nil, err
}

// StartSession cria a sessão; se já houver uma ativa, devolve essa (created=false).
func (s *Store) StartSession(ctx context.Context, now time.Time, maxDur time.Duration, maxUSD float64, ip, ua string) (Session, bool, error) {
	ses, err := scanSession(s.DB.QueryRow(ctx, `INSERT INTO sessions(started_at, deadline, max_usd, origin_ip, user_agent)
		VALUES ($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING RETURNING `+sessionCols, now, now.Add(maxDur), maxUSD, ip, ua))
	if errors.Is(err, pgx.ErrNoRows) {
		ses, _, err = s.ActiveSession(ctx)
		return ses, false, err
	}
	return ses, err == nil, err
}

// SessionSpent: custo de LLM e voz registrado durante a sessão.
func (s *Store) SessionSpent(ctx context.Context, ses Session, now time.Time) (float64, error) {
	end := now
	if ses.EndedAt != nil {
		end = *ses.EndedAt
	}
	var v float64
	err := s.DB.QueryRow(ctx, `SELECT COALESCE(sum(cost_usd),0)::float8 FROM llm_calls WHERE created_at >= $1 AND created_at <= $2`, ses.StartedAt, end).Scan(&v)
	return v, err
}

// EndSession encerra a sessão e tira do ar o que estava agendado dali em diante
// (status 'skipped'; horários continuam imutáveis).
func (s *Store) EndSession(ctx context.Context, id int64, reason string, now time.Time) (bool, error) {
	var ended bool
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		var started time.Time
		if err := tx.QueryRow(ctx, `SELECT started_at FROM sessions WHERE id=$1 AND status='active' FOR UPDATE`, id).Scan(&started); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		var spent float64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(cost_usd),0)::float8 FROM llm_calls WHERE created_at >= $1 AND created_at <= $2`, started, now).Scan(&spent); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE sessions SET status='ended', ended_at=$2, end_reason=$3, spent_usd=$4 WHERE id=$1`, id, now, reason, spent); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE timeline SET status='skipped' WHERE status='scheduled' AND ends_at > $1`, now); err != nil {
			return err
		}
		ended = true
		return nil
	})
	return ended, err
}

// MarkFirstLine registra a primeira fala no ar da sessão (uma vez).
func (s *Store) MarkFirstLine(ctx context.Context, id int64, at time.Time) error {
	_, err := s.DB.Exec(ctx, `UPDATE sessions SET first_line_at=$2 WHERE id=$1 AND first_line_at IS NULL`, id, at)
	return err
}

// ---- audiência ----

// SetViewers grava a contagem de conexões SSE ativas.
func (s *Store) SetViewers(ctx context.Context, n int, now time.Time) error {
	_, err := s.DB.Exec(ctx, `UPDATE audience SET viewers=$1, updated_at=$2, last_seen_at = CASE WHEN $1 > 0 THEN $2 ELSE last_seen_at END WHERE id=1`, n, now)
	return err
}

func (s *Store) Audience(ctx context.Context) (viewers int, updated time.Time, lastSeen *time.Time, err error) {
	err = s.DB.QueryRow(ctx, `SELECT viewers, updated_at, last_seen_at FROM audience WHERE id=1`).Scan(&viewers, &updated, &lastSeen)
	return
}
