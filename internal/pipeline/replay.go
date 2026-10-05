package pipeline

import (
	"context"
	"time"
)

// Replay reapresenta um segmento aprovado do bloco das últimas `window` horas,
// sem gerar nada (custo zero). Devolve 0 se não houver o que reprisar.
func (p *Pipeline) Replay(ctx context.Context, block string, window time.Duration) (int64, error) {
	now := p.now()
	id, ok, err := p.Store.ReplayCandidate(ctx, block, now.Add(-window), now)
	if err != nil || !ok {
		return 0, err
	}
	if err := p.Store.Air(ctx, id, block, "replay"); err != nil {
		return 0, err
	}
	return id, nil
}
