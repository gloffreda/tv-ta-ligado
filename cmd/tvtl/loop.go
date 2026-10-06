package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gloffreda/tv-ta-ligado/internal/api"
	"github.com/gloffreda/tv-ta-ligado/internal/config"
	"github.com/gloffreda/tv-ta-ligado/internal/llm"
	"github.com/gloffreda/tv-ta-ligado/internal/persona"
	"github.com/gloffreda/tv-ta-ligado/internal/pipeline"
	"github.com/gloffreda/tv-ta-ligado/internal/timeline"
	"github.com/gloffreda/tv-ta-ligado/internal/voice"
)

// now é o relógio do app: UTC do servidor + CLOCK_OFFSET (homologação).
func (a *app) now() time.Time { return time.Now().Add(a.env.ClockOffset) }

func (a *app) scheduler(v *voice.Voicer) (*timeline.Scheduler, error) {
	sched, err := config.LoadSchedule(a.env.ConfigDir)
	if err != nil {
		return nil, err
	}
	tc := sched.Timeline
	var bumperMu sync.Mutex
	var bumper *timeline.BumperAudio
	return &timeline.Scheduler{
		Store: a.store, Now: a.now,
		Cfg: timeline.Config{BufferMin: a.env.BufferMin, PauseLines: ms(tc.PauseLinesMS), PauseSpeakers: ms(tc.PauseSpeakersMS),
			PauseItems: ms(tc.PauseItemsMS), ReplayWindow: a.env.ReplayWindow, ReplayGap: tc.ReplayMinGap.Duration},
		Blocks: func() []config.Block {
			if s, err := config.LoadSchedule(a.env.ConfigDir); err == nil {
				return s.Blocks
			}
			return sched.Blocks
		},
		Blackout: func() config.Blackout {
			b, _ := config.LoadBlackout(a.env.ConfigDir)
			return b
		},
		// Vinheta: texto fixo sintetizado uma vez (depois vem do cache).
		Bumper: func(ctx context.Context) (timeline.BumperAudio, error) {
			bumperMu.Lock()
			defer bumperMu.Unlock()
			if bumper != nil {
				return *bumper, nil
			}
			ps, err := persona.Load(a.env.ConfigDir)
			if err != nil {
				return timeline.BumperAudio{}, err
			}
			p, ok := ps[tc.BumperSpeaker]
			if !ok {
				return timeline.BumperAudio{}, fmt.Errorf("bumper_speaker %q não existe", tc.BumperSpeaker)
			}
			spoken := v.Spoken(ctx, tc.BumperText)
			asset, _, err := v.Asset(ctx, p.Voice, p.FallbackVoices, spoken, nil)
			if err != nil {
				return timeline.BumperAudio{}, err
			}
			bumper = &timeline.BumperAudio{Hash: asset.Hash, Text: tc.BumperText, Spoken: spoken, Speaker: tc.BumperSpeaker, DurationMS: asset.DurationMS}
			return *bumper, nil
		},
		OnEvent: func(kind string, d map[string]any) { _ = a.store.Event(context.Background(), kind, d) },
	}, nil
}

func ms(n int) time.Duration { return time.Duration(n) * time.Millisecond }

// run: ingestão a cada 5 min, geração conforme a grade, voz das falas
// aprovadas e a linha do tempo sempre com BUFFER_MIN à frente.
func (a *app) run(ctx context.Context) error {
	in, err := a.ingester()
	if err != nil {
		return err
	}
	v, err := a.voicer(ctx)
	if err != nil {
		return err
	}
	sc, err := a.scheduler(v)
	if err != nil {
		return err
	}
	a.sched = sc
	slog.Info("tvtl run", "ingestao_a_cada", a.env.IngestInterval, "generate", a.env.Generate, "teto_usd_dia", a.env.MaxDailyUSD,
		"replay_when_idle", a.env.ReplayWhenIdle, "viewers", a.env.Viewers, "buffer_min", a.env.BufferMin,
		"clock_offset", a.env.ClockOffset, "model_fast", a.env.ModelFast, "model_smart", a.env.ModelSmart)

	// Linha do tempo numa rotina própria: a geração pode levar minutos.
	tlCtx, stopTL := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			if added, err := sc.Fill(tlCtx, time.Time{}); err != nil {
				if tlCtx.Err() == nil {
					slog.Error("linha do tempo", "erro", err)
				}
			} else if len(added) > 0 {
				kinds := map[string]int{}
				for _, it := range added {
					kinds[it.Kind]++
				}
				end, _, _ := a.store.TimelineEnd(tlCtx)
				slog.Info("linha do tempo", "agendados", kinds, "buffer", end.Sub(a.now()).Round(time.Second))
			}
			select {
			case <-tlCtx.Done():
				return
			case <-t.C:
			}
		}
	}()
	defer func() { stopTL(); wg.Wait() }()

	p := a.pipeline()
	if err := a.loadGlossary(ctx, in); err != nil {
		slog.Warn("glossário não carregado", "erro", err)
	}
	a.ingestOnce(ctx, in)
	ingestT := time.NewTicker(a.env.IngestInterval)
	defer ingestT.Stop()
	genT := time.NewTicker(time.Minute)
	defer genT.Stop()
	cycle := func() {
		a.generateDue(ctx, p, v)
		a.voiceBacklog(ctx, v, 2)
	}
	cycle()
	for {
		select {
		case <-ctx.Done():
			slog.Info("encerrando")
			return nil
		case <-ingestT.C:
			a.ingestOnce(ctx, in)
		case <-genT.C:
			cycle()
		}
	}
}

// voiceBacklog dá voz a segmentos aprovados da janela de reprise que ainda
// não têm áudio (no máximo `limit` por ciclo).
func (a *app) voiceBacklog(ctx context.Context, v *voice.Voicer, limit int) {
	ids, err := a.store.UnvoicedApproved(ctx, a.now().Add(-a.env.ReplayWindow))
	if err != nil || len(ids) == 0 {
		return
	}
	ps, err := persona.Load(a.env.ConfigDir)
	if err != nil {
		return
	}
	for i, id := range ids {
		if i >= limit || ctx.Err() != nil {
			return
		}
		t0 := time.Now()
		n, err := v.VoiceSegment(ctx, id, ps)
		slog.Info("voz (pendente)", "segmento", id, "falas", n, "duracao", time.Since(t0).Round(time.Second), "erro", err)
	}
}

// generateDue gera um segmento para cada bloco vencido (pela grade) e dá voz
// aos aprovados. Sem audiência (REPLAY_WHEN_IDLE=on e VIEWERS=0) não gera: a
// linha do tempo se preenche com reprises.
func (a *app) generateDue(ctx context.Context, p *pipeline.Pipeline, v *voice.Voicer) (n int) {
	if a.env.ReplayWhenIdle && a.env.Viewers == 0 {
		slog.Debug("sem audiência: só reprise")
		return 0
	}
	if err := p.Guard(ctx); err != nil {
		slog.Debug("geração suspensa", "motivo", err)
		return 0
	}
	sched, err := config.LoadSchedule(a.env.ConfigDir)
	if err != nil {
		slog.Error("schedule.yaml", "erro", err)
		return 0
	}
	personas, err := persona.Load(a.env.ConfigDir)
	if err != nil {
		slog.Error("personas", "erro", err)
		return 0
	}
	now := a.now()
	for _, b := range sched.Blocks {
		if ctx.Err() != nil {
			return n
		}
		lastOK, okFound, err := a.store.LastSegmentAt(ctx, b.Name, "approved")
		if err != nil {
			slog.Error("agenda", "erro", err)
			return n
		}
		if okFound && now.Sub(lastOK) < b.Every.Duration {
			continue
		}
		lastTry, tryFound, _ := a.store.LastSegmentAt(ctx, b.Name, "rejected", "draft")
		if tryFound && now.Sub(lastTry) < sched.RetryAfter.Duration {
			continue
		}
		start := time.Now()
		res, err := p.Generate(ctx, b.Name)
		if res.SegmentID != 0 {
			n++
		}
		if err != nil {
			if errors.Is(err, llm.ErrBudget) || errors.Is(err, pipeline.ErrBlackout) || errors.Is(err, pipeline.ErrGenerateOff) {
				slog.Warn("geração parada", "motivo", err)
				return n
			}
			slog.Error("geração falhou", "bloco", b.Name, "segmento", res.SegmentID, "erro", err)
			continue
		}
		dropped := 0
		for _, o := range res.Outcomes {
			if o.Status == "dropped" {
				dropped++
			}
		}
		genDur := time.Since(start)
		if res.Status == "approved" {
			t1 := time.Now()
			lines, err := v.VoiceSegment(ctx, res.SegmentID, personas)
			if err != nil {
				slog.Error("voz", "segmento", res.SegmentID, "erro", err)
				_ = a.store.Event(ctx, "voice_failed", map[string]any{"segment_id": res.SegmentID, "error": err.Error()})
			}
			slog.Info("voz", "segmento", res.SegmentID, "falas", lines, "duracao", time.Since(t1).Round(time.Second))
		}
		slog.Info("segmento", "bloco", b.Name, "id", res.SegmentID, "status", res.Status, "motivo", res.Reason,
			"falas", len(res.Outcomes), "cortadas", dropped, "geracao", genDur.Round(time.Second))
	}
	return n
}

// serve: API da linha do tempo.
func (a *app) serve(ctx context.Context, listen string) error {
	s := &api.Server{Store: a.store, Now: a.now, MediaDir: envOr("TVTL_MEDIA_DIR", "/media")}
	srv := &http.Server{Addr: listen, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() { <-ctx.Done(); srv.Close() }()
	slog.Info("serve", "listen", listen, "clock_offset", a.env.ClockOffset)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}
