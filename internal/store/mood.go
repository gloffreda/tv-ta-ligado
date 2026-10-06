package store

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"
)

// Linha de base do humor (para onde ele volta com o tempo).
var MoodBase = Mood{Irritacao: 3, Animo: 6, Rivalidade: 5}

// MoodHalfLife: em quanto tempo o desvio da linha de base cai pela metade.
const MoodHalfLife = 12 * time.Hour

func decay(v, base float64, dt time.Duration) float64 {
	if dt <= 0 {
		return v
	}
	return base + (v-base)*math.Pow(0.5, dt.Hours()/MoodHalfLife.Hours())
}

func clamp10(v float64) float64 { return math.Max(0, math.Min(10, v)) }

// Decayed devolve o humor no instante now (decaimento até a linha de base).
func (m Mood) Decayed(now time.Time) Mood {
	dt := now.Sub(m.UpdatedAt)
	m.Irritacao = decay(m.Irritacao, MoodBase.Irritacao, dt)
	m.Animo = decay(m.Animo, MoodBase.Animo, dt)
	m.Rivalidade = decay(m.Rivalidade, MoodBase.Rivalidade, dt)
	m.UpdatedAt = now
	return m
}

// Moods: humor atual (com decaimento) de cada persona; sem registro = linha de base.
func (s *Store) Moods(ctx context.Context, personas []string, now time.Time) (map[string]Mood, error) {
	out := map[string]Mood{}
	for _, p := range personas {
		m := MoodBase
		m.Persona, m.UpdatedAt = p, now
		out[p] = m
	}
	rows, err := s.DB.Query(ctx, `SELECT persona, irritacao, animo, rivalidade, updated_at FROM persona_mood WHERE persona = ANY($1)`, personas)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var m Mood
		if err := rows.Scan(&m.Persona, &m.Irritacao, &m.Animo, &m.Rivalidade, &m.UpdatedAt); err != nil {
			return nil, err
		}
		out[m.Persona] = m.Decayed(now)
	}
	return out, rows.Err()
}

// NudgeMood aplica o decaimento e soma os deltas (limitado a 0–10).
func (s *Store) NudgeMood(ctx context.Context, persona string, dIrr, dAni, dRiv float64, now time.Time) error {
	ms, err := s.Moods(ctx, []string{persona}, now)
	if err != nil {
		return err
	}
	m := ms[persona]
	_, err = s.DB.Exec(ctx, `
		INSERT INTO persona_mood(persona, irritacao, animo, rivalidade, updated_at) VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (persona) DO UPDATE SET irritacao=$2, animo=$3, rivalidade=$4, updated_at=$5`,
		persona, clamp10(m.Irritacao+dIrr), clamp10(m.Animo+dAni), clamp10(m.Rivalidade+dRiv), now)
	return err
}

// Pair: chave do par (ou grupo) de personagens, em ordem alfabética.
func Pair(cast []string) string {
	c := append([]string{}, cast...)
	sort.Strings(c)
	return strings.Join(c, "+")
}

// CastMemories: memórias ativas do elenco (do par ou gerais), respeitando o
// limite de usos por semana de cada piada recorrente.
func (s *Store) CastMemories(ctx context.Context, cast []string, n int, maxUsesWeek int, halfLifeDays float64, now time.Time) ([]Memory, error) {
	week := weekStart(now)
	rows, err := s.DB.Query(ctx, `
		SELECT id, persona, kind, content,
		       weight * power(0.5, extract(epoch FROM ($3::timestamptz - created_at)) / 86400.0 / $2) AS w, created_at
		FROM persona_memory
		WHERE retired_at IS NULL AND persona = ANY($4) AND (about IS NULL OR about = $5)
		  AND (week_start IS DISTINCT FROM $6::date OR uses_week < $7)
		ORDER BY w DESC, created_at DESC LIMIT $1`, n, halfLifeDays, now, cast, Pair(cast), week, maxUsesWeek)
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

// MarkMemoriesUsed conta um uso na semana (piadas recorrentes: no máximo 3 por semana).
func (s *Store) MarkMemoriesUsed(ctx context.Context, ids []int64, now time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	week := weekStart(now)
	_, err := s.DB.Exec(ctx, `
		UPDATE persona_memory SET
		  uses_week = CASE WHEN week_start IS DISTINCT FROM $2::date THEN 1 ELSE uses_week + 1 END,
		  week_start = $2::date
		WHERE id = ANY($1)`, ids, week)
	return err
}

// InsertMemoryAbout grava a memória ligada a um par de personagens.
func (s *Store) InsertMemoryAbout(ctx context.Context, m Memory, segmentID int64, about string) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO persona_memory(persona, kind, content, source_segment_id, weight, about) VALUES ($1,$2,$3,$4,$5,NULLIF($6,''))`,
		m.Persona, m.Kind, m.Content, segmentID, m.Weight, about)
	return err
}

// weekStart: segunda-feira da semana (data, sem fuso).
func weekStart(t time.Time) time.Time {
	d := int(t.Weekday()+6) % 7
	y, mo, da := t.AddDate(0, 0, -d).Date()
	return time.Date(y, mo, da, 0, 0, 0, 0, time.UTC)
}
