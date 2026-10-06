package tts

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// MaxConsecutiveFailures: depois de tantas falhas seguidas, o provedor é
// rebaixado e o roteador passa a usar os fallbacks.
const MaxConsecutiveFailures = 3

// Router escolhe o provedor de cada síntese, com fallback.
type Router struct {
	Providers map[string]Provider // só os disponíveis (locais e nuvens com chave)
	Fallback  []string            // TTS_FALLBACK, em ordem
	Retry     time.Duration       // quanto tempo um provedor rebaixado fica fora (padrão 10 min)
	OnEvent   func(kind string, detail map[string]any)

	mu       sync.Mutex
	failures map[string]int
	degraded map[string]time.Time
}

// Result traz o áudio e qual provedor/voz realmente falou.
type Result struct {
	Audio    Audio
	Duration time.Duration
	Voice    Voice
	CostUSD  float64
	Fallback bool
}

func (r *Router) event(kind string, d map[string]any) {
	if r.OnEvent != nil {
		r.OnEvent(kind, d)
	}
}

func (r *Router) isDegraded(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.degraded[name]
	retry := r.Retry
	if retry == 0 {
		retry = 10 * time.Minute
	}
	if ok && time.Since(t) > retry {
		delete(r.degraded, name) // tenta de novo
		return false
	}
	return ok
}

func (r *Router) record(name string, ok bool) (justDegraded bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failures == nil {
		r.failures, r.degraded = map[string]int{}, map[string]time.Time{}
	}
	if ok {
		r.failures[name] = 0
		return false
	}
	r.failures[name]++
	if r.failures[name] >= MaxConsecutiveFailures {
		r.failures[name] = 0
		r.degraded[name] = time.Now()
		return true
	}
	return false
}

// Synthesize tenta a voz principal (até 3 vezes seguidas) e depois as vozes de
// fallback, na ordem de TTS_FALLBACK.
func (r *Router) Synthesize(ctx context.Context, text string, primary Voice, fallbacks map[string]Voice) (Result, error) {
	var errs []string
	try := func(v Voice, attempts int) (Result, bool) {
		p, ok := r.Providers[v.Provider]
		if !ok {
			errs = append(errs, v.Provider+": não configurado")
			return Result{}, false
		}
		for i := 0; i < attempts && !r.isDegraded(v.Provider); i++ {
			a, dur, err := p.Synthesize(ctx, text, v)
			if err == nil {
				r.record(v.Provider, true)
				cost := float64(len([]rune(text))) * p.PricePerMChar() / 1e6
				return Result{Audio: a, Duration: dur, Voice: v, CostUSD: cost}, true
			}
			errs = append(errs, err.Error())
			if r.record(v.Provider, false) {
				slog.Warn("TTS: provedor rebaixado após falhas seguidas", "provedor", v.Provider, "erro", err)
				r.event("tts_fallback", map[string]any{"provider": v.Provider, "error": err.Error(), "failures": MaxConsecutiveFailures})
			}
			if ctx.Err() != nil {
				return Result{}, false
			}
		}
		return Result{}, false
	}
	if res, ok := try(primary, MaxConsecutiveFailures); ok {
		return res, nil
	}
	for _, name := range r.Fallback {
		name = strings.TrimSpace(name)
		if name == "" || name == primary.Provider {
			continue
		}
		v, ok := fallbacks[name]
		if !ok {
			continue
		}
		if res, ok := try(v, 2); ok {
			res.Fallback = true
			return res, nil
		}
	}
	return Result{}, fmt.Errorf("nenhum provedor de voz respondeu: %s", strings.Join(errs, " | "))
}
