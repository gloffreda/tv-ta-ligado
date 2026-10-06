package llm

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/gloffreda/tv-ta-ligado/internal/config"
)

type fixed Response

func (f fixed) Complete(context.Context, Request) (Response, error) { return Response(f), nil }

func TestCostWithCacheTTL(t *testing.T) {
	p := config.Price{InputPerMTok: 2, OutputPerMTok: 10}
	m := &Metered{Inner: fixed{InputTokens: 1000, OutputTokens: 500, CacheReadTokens: 4000, CacheWriteTokens: 6000, CacheWrite1h: 5000},
		Prices: map[string]config.Price{"m": p}, MaxDailyUSD: 100, Ledger: &memLedger{}, Loc: time.UTC}
	r, err := m.Complete(context.Background(), Request{Model: "m", Purpose: "script"})
	if err != nil {
		t.Fatal(err)
	}
	// entrada 1000*2 + saída 500*10 + leitura 4000*0,1*2 + gravação 5m 1000*1,25*2 + gravação 1h 5000*2*2 (por milhão)
	want := (2000 + 5000 + 800 + 2500 + 20000) / 1e6
	if math.Abs(r.CostUSD-want) > 1e-9 {
		t.Fatalf("custo %.6f, quer %.6f", r.CostUSD, want)
	}
}

type deny struct{}

func (deny) Allow(context.Context, string) error { return errNoSession }

var errNoSession = &gateErr{}

type gateErr struct{}

func (*gateErr) Error() string { return "no_active_session" }

func TestGateBlocksBeforeAnyCall(t *testing.T) {
	called := false
	inner := ClientFunc(func(context.Context, Request) (Response, error) { called = true; return Response{}, nil })
	m := &Metered{Inner: inner, Gate: deny{}}
	if _, err := m.Complete(context.Background(), Request{Model: "m"}); err == nil || !Fatal(err) {
		t.Fatalf("sem sessão: erro fatal no_active_session, veio %v", err)
	}
	if called {
		t.Fatal("a API não pode ser chamada sem sessão")
	}
}

type ClientFunc func(context.Context, Request) (Response, error)

func (f ClientFunc) Complete(ctx context.Context, r Request) (Response, error) { return f(ctx, r) }

type memLedger struct{ calls []LLMCall }

func (l *memLedger) RecordLLMCall(_ context.Context, c LLMCall) error {
	l.calls = append(l.calls, c)
	return nil
}
func (l *memLedger) SpentSince(context.Context, time.Time) (float64, error) { return 0, nil }
func (l *memLedger) Event(context.Context, string, any) error               { return nil }
