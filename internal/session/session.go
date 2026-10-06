// Package session controla o modo sob demanda: o canal só trabalha dentro de
// uma sessão ligada à mão pela página. Fora dela, nenhuma chamada de API, voz,
// ingestão ou agendamento.
package session

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/gloffreda/tv-ta-ligado/internal/store"
)

// ErrNoActiveSession: trabalho recusado porque não há sessão ativa.
var ErrNoActiveSession = errors.New("no_active_session")

// Manual: comando rodado à mão no terminal (TVTL_MANUAL=1, só nos alvos do
// Makefile feitos para isso). É a única exceção à trava.
func Manual() bool { return os.Getenv("TVTL_MANUAL") == "1" }

// Gate responde se há sessão ativa (com cache curto para não martelar o banco).
type Gate struct {
	Store    *store.Store
	OnDemand bool // RUN_MODE=on_demand
	Event    func(kind string, detail map[string]any)

	mu       sync.Mutex
	checked  time.Time
	active   bool
	lastWarn time.Time
}

// Allow devolve nil se o trabalho pode rodar agora.
func (g *Gate) Allow(ctx context.Context, what string) error {
	if g == nil || !g.OnDemand || Manual() {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if time.Since(g.checked) > time.Second {
		_, ok, err := g.Store.ActiveSession(ctx)
		if err != nil {
			return err
		}
		g.active, g.checked = ok, time.Now()
	}
	if g.active {
		return nil
	}
	if time.Since(g.lastWarn) > time.Minute {
		g.lastWarn = time.Now()
		slog.Warn("recusado: sem sessão ativa", "o_que", what)
		if g.Event != nil {
			g.Event("no_active_session", map[string]any{"what": what})
		}
	}
	return ErrNoActiveSession
}

// Limits de cada sessão.
type Limits struct {
	MaxDur      time.Duration // SESSION_MAX_MIN (60)
	MaxUSD      float64       // SESSION_MAX_USD (2)
	IdleTimeout time.Duration // sem espectador (5 min)
}

// CheckEnd diz se a sessão deve terminar agora e por quê.
func CheckEnd(ses store.Session, now time.Time, spent float64, lastSeen *time.Time, lim Limits) (string, bool) {
	switch {
	case !now.Before(ses.Deadline):
		return "max_time", true
	case spent >= ses.MaxUSD:
		return "max_usd", true
	}
	ref := ses.StartedAt
	if lastSeen != nil && lastSeen.After(ref) {
		ref = *lastSeen
	}
	if now.Sub(ref) >= lim.IdleTimeout {
		return "no_viewers", true
	}
	return "", false
}
