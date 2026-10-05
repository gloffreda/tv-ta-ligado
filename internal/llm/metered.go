package llm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/gloffreda/tv-ta-ligado/internal/config"
)

// ErrBudget: o teto diário (MAX_DAILY_USD) foi atingido.
var ErrBudget = errors.New("teto diário de orçamento atingido")

// LLMCall é uma linha de llm_calls.
type LLMCall struct {
	Purpose      string
	Model        string
	InputTokens  int
	OutputTokens int
	CostUSD      float64
	SegmentID    *int64
}

// Ledger é o que o Metered precisa do banco.
type Ledger interface {
	RecordLLMCall(ctx context.Context, c LLMCall) error
	SpentSince(ctx context.Context, since time.Time) (float64, error)
	Event(ctx context.Context, kind string, detail any) error
}

// Metered aplica o teto diário antes de cada chamada e registra tokens e custo.
type Metered struct {
	Inner       Client
	Prices      map[string]config.Price
	MaxDailyUSD float64
	Ledger      Ledger
	Loc         *time.Location
	Now         func() time.Time

	mu        sync.Mutex
	warnedDay string
}

func (m *Metered) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

// Cost em dólares, com preços por milhão de tokens.
func Cost(p config.Price, in, out int) float64 {
	return (float64(in)*p.InputPerMTok + float64(out)*p.OutputPerMTok) / 1e6
}

// CheckBudget devolve ErrBudget se o gasto do dia já atingiu o teto.
func (m *Metered) CheckBudget(ctx context.Context) error {
	now := m.now().In(m.Loc)
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, m.Loc)
	spent, err := m.Ledger.SpentSince(ctx, day)
	if err != nil {
		return err
	}
	if spent < m.MaxDailyUSD {
		return nil
	}
	m.mu.Lock()
	first := m.warnedDay != day.Format("2006-01-02")
	m.warnedDay = day.Format("2006-01-02")
	m.mu.Unlock()
	if first {
		slog.Warn("teto diário atingido: geração suspensa", "gasto_usd", spent, "teto_usd", m.MaxDailyUSD)
		_ = m.Ledger.Event(ctx, "budget_exceeded", map[string]any{"spent_usd": spent, "max_usd": m.MaxDailyUSD, "day": day.Format("2006-01-02")})
	}
	return ErrBudget
}

func (m *Metered) Complete(ctx context.Context, req Request) (Response, error) {
	if err := m.CheckBudget(ctx); err != nil {
		return Response{}, err
	}
	resp, err := m.Inner.Complete(ctx, req)
	if resp.InputTokens == 0 && resp.OutputTokens == 0 {
		return resp, err // falha antes de cobrar
	}
	p, ok := m.Prices[req.Model]
	if !ok {
		slog.Warn("modelo sem preço configurado; custo registrado como 0", "model", req.Model)
	}
	resp.CostUSD = Cost(p, resp.InputTokens, resp.OutputTokens)
	call := LLMCall{Purpose: req.Purpose, Model: req.Model, InputTokens: resp.InputTokens, OutputTokens: resp.OutputTokens, CostUSD: resp.CostUSD}
	if id, ok := SegmentFrom(ctx); ok {
		call.SegmentID = &id
	}
	if rerr := m.Ledger.RecordLLMCall(ctx, call); rerr != nil {
		return resp, fmt.Errorf("registrar custo: %w", rerr)
	}
	return resp, err
}
