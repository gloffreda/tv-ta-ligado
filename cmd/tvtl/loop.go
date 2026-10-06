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
	"github.com/gloffreda/tv-ta-ligado/internal/dataseg"
	"github.com/gloffreda/tv-ta-ligado/internal/ingest"
	"github.com/gloffreda/tv-ta-ligado/internal/llm"
	"github.com/gloffreda/tv-ta-ligado/internal/persona"
	"github.com/gloffreda/tv-ta-ligado/internal/pipeline"
	"github.com/gloffreda/tv-ta-ligado/internal/session"
	"github.com/gloffreda/tv-ta-ligado/internal/store"
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
		Cfg: timeline.Config{BufferMin: a.env.BufferMin, FillMargin: 30 * time.Second, PauseLines: ms(tc.PauseLinesMS), PauseSpeakers: ms(tc.PauseSpeakersMS),
			PauseItems: ms(tc.PauseItemsMS), ReplayWindow: a.env.ReplayWindow, ReplayGap: tc.ReplayMinGap.Duration,
			BumperLead: 15 * time.Second},
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
		Gate:    a.gate,
		Prefer:  a.preferAt,
	}, nil
}

func ms(n int) time.Duration { return time.Duration(n) * time.Millisecond }

// run: supervisor. Em RUN_MODE=on_demand (padrão) fica em repouso até a
// página ligar uma sessão; então trabalha até a sessão acabar (botão, sem
// espectador, tempo ou gasto) e volta ao repouso. Reiniciar o processo nunca
// liga nada: uma sessão ativa encontrada na partida é encerrada (restart).
// Em RUN_MODE=always trabalha sem parar (modo antigo, só para testes locais).
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
	slog.Info("tvtl run", "run_mode", a.env.RunMode, "sessao_max", a.env.SessionMaxDur, "sessao_max_usd", a.env.SessionMaxUSD,
		"sem_espectador", a.env.SessionIdle, "teto_usd_dia", a.env.MaxDailyUSD, "buffer_min", a.env.BufferMin,
		"clock_offset", a.env.ClockOffset, "model_fast", a.env.ModelFast, "model_smart", a.env.ModelSmart)
	if !a.env.OnDemand() {
		return a.work(ctx, in, v, sc, nil)
	}
	if ses, ok, err := a.store.ActiveSession(ctx); err == nil && ok {
		if _, err := a.store.EndSession(ctx, ses.ID, "restart", a.now()); err == nil {
			slog.Warn("sessão ativa encontrada na partida: encerrada (reinício nunca liga nada)", "sessao", ses.ID)
			_ = a.store.Event(ctx, "session_ended", map[string]any{"session_id": ses.ID, "reason": "restart"})
		}
	}
	slog.Info("em repouso: aguardando o botão da página")
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		ses, ok, err := a.store.ActiveSession(ctx)
		if err != nil || !ok {
			continue
		}
		slog.Info("sessão ligada: começando", "sessao", ses.ID, "prazo", ses.Deadline, "teto_usd", ses.MaxUSD)
		sctx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- a.work(sctx, in, v, sc, &ses) }()
		reason := a.watch(ctx, ses, done)
		cancel()
		<-done
		if reason != "" {
			if _, err := a.store.EndSession(context.WithoutCancel(ctx), ses.ID, reason, a.now()); err != nil {
				slog.Error("encerrar sessão", "erro", err)
			}
			_ = a.store.Event(context.WithoutCancel(ctx), "session_ended", map[string]any{"session_id": ses.ID, "reason": reason})
		}
		spent, _ := a.store.SessionSpent(context.WithoutCancel(ctx), ses, a.now())
		slog.Info("sessão encerrada: em repouso", "sessao", ses.ID, "motivo", reason, "gasto_usd", spent)
		if ctx.Err() != nil {
			return nil
		}
	}
}

// watch confere os limites a cada 5 s. Devolve o motivo para encerrar, ou ""
// se a sessão já foi encerrada por outro caminho (botão Desligar).
func (a *app) watch(ctx context.Context, ses store.Session, done <-chan error) string {
	lim := session.Limits{MaxDur: a.env.SessionMaxDur, MaxUSD: a.env.SessionMaxUSD, IdleTimeout: a.env.SessionIdle}
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return "shutdown"
		case err := <-done:
			slog.Error("sessão: trabalho parou", "erro", err)
			return "error"
		case <-t.C:
		}
		cur, ok, err := a.store.ActiveSession(ctx)
		if err != nil {
			continue
		}
		if !ok || cur.ID != ses.ID {
			return ""
		}
		now := a.now()
		spent, err := a.store.SessionSpent(ctx, ses, now)
		if err != nil {
			continue
		}
		_, _, lastSeen, err := a.store.Audience(ctx)
		if err != nil {
			continue
		}
		if why, end := session.CheckEnd(ses, now, spent, lastSeen, lim); end {
			return why
		}
	}
}

// work: o canal trabalhando. Primeiro o que é rápido e sem custo (linha do
// tempo com reprises, ingestão sem LLM, segmentos de dados), depois a geração
// com LLM em paralelo.
func (a *app) work(ctx context.Context, in *ingest.Ingester, v *voice.Voicer, sc *timeline.Scheduler, ses *store.Session) error {
	var wg sync.WaitGroup
	defer wg.Wait()
	ds := a.dataBuilder()
	// Abertura: uma fala só, com áudio quase sempre em cache. É a primeira coisa no ar.
	if ses != nil {
		a.opening(ctx, ds, v)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		a.timelineLoop(ctx, sc, ses)
	}()

	// Dados com os fatos que ainda valem (rápido), depois a ingestão sem LLM.
	a.dataStock(ctx, ds, v, true)
	a.ingestOnce(ctx, in)
	a.dataStock(ctx, ds, v, true)
	a.voiceBacklog(ctx, v, 3)

	wg.Add(1)
	go func() {
		defer wg.Done()
		a.periodic(ctx, in)
	}()

	p := a.pipeline()
	ingestT := time.NewTicker(a.env.IngestInterval)
	defer ingestT.Stop()
	genT := time.NewTicker(time.Minute)
	defer genT.Stop()
	cycle := func() {
		a.dataStock(ctx, ds, v, false)
		a.generateDue(ctx, p, v)
		a.dataStock(ctx, ds, v, false)
		a.voiceBacklog(ctx, v, 2)
	}
	cycle()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ingestT.C:
			a.ingestOnce(ctx, in)
		case <-genT.C:
			cycle()
		}
	}
}

// timelineLoop mantém BUFFER_MIN à frente e marca a primeira fala da sessão.
func (a *app) timelineLoop(ctx context.Context, sc *timeline.Scheduler, ses *store.Session) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	first := ses == nil || ses.FirstLineAt != nil
	for {
		added, err := sc.Fill(ctx, time.Time{})
		if err != nil {
			if ctx.Err() == nil && !errors.Is(err, session.ErrNoActiveSession) {
				slog.Error("linha do tempo", "erro", err)
			}
		} else if len(added) > 0 {
			kinds := map[string]int{}
			for _, it := range added {
				kinds[it.Kind]++
				if !first && it.Kind != "bumper" && len(it.Lines) > 0 {
					at := it.StartsAt.Add(time.Duration(it.Lines[0].OffsetMS) * time.Millisecond)
					if err := a.store.MarkFirstLine(ctx, ses.ID, at); err == nil {
						first = true
						slog.Info("primeira fala da sessão", "sessao", ses.ID, "tipo", it.Kind, "no_ar_em", at.Sub(ses.StartedAt).Round(time.Millisecond))
					}
				}
			}
			end, _, _ := a.store.TimelineEnd(ctx)
			slog.Info("linha do tempo", "agendados", kinds, "buffer", end.Sub(a.now()).Round(time.Second))
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// periodic: tarefas que só rodam dentro de sessão — glossário (a cada 24 h)
// e consolidação da memória (no máximo a cada 7 dias).
func (a *app) periodic(ctx context.Context, in *ingest.Ingester) {
	if due, err := a.store.JobDue(ctx, "glossary", 24*time.Hour, a.now()); err == nil && due {
		if err := a.loadGlossary(ctx, in); err != nil {
			slog.Warn("glossário não carregado", "erro", err)
		} else {
			_ = a.store.JobDone(ctx, "glossary", a.now())
		}
	}
	if due, err := a.store.JobDue(ctx, "memory_consolidation", 7*24*time.Hour, a.now()); err == nil && due {
		if err := a.consolidateMemory(ctx); err != nil {
			slog.Warn("consolidação da memória", "erro", err)
		} else {
			_ = a.store.JobDone(ctx, "memory_consolidation", a.now())
		}
	}
}

// dataBuilder: segmentos de dados (sem LLM).
func (a *app) dataBuilder() *dataseg.Builder {
	lex, _, err := a.pipeline().Lexicon(config.Schedule{})
	if err != nil {
		slog.Warn("dados: allowlist", "erro", err)
	}
	return &dataseg.Builder{Store: a.store, Lex: lex, Loc: a.env.Location, Now: a.now}
}

// dataStock mantém pelo menos um segmento de cada bloco de dados pronto (com
// voz). Na partida (startup) monta tempo e mercado primeiro: são os mais rápidos.
func (a *app) dataStock(ctx context.Context, ds *dataseg.Builder, v *voice.Voicer, startup bool) {
	sched, err := config.LoadSchedule(a.env.ConfigDir)
	if err != nil {
		return
	}
	personas, err := persona.Load(a.env.ConfigDir)
	if err != nil {
		return
	}
	now := a.now()
	for _, b := range sched.Blocks {
		if b.Data == "" || ctx.Err() != nil {
			continue
		}
		if err := a.gate.Allow(ctx, "data:"+b.Name); err != nil {
			return
		}
		n, err := a.store.DataStock(ctx, b.Name, now.Add(-b.Every.Duration))
		if err != nil || n > 0 {
			continue
		}
		if last, ok, _ := a.store.LastSegmentAt(ctx, b.Name, "approved"); ok && now.Sub(last) < b.Every.Duration/2 && !startup {
			continue
		}
		t0 := time.Now()
		res, err := ds.Build(ctx, b.Name)
		if err != nil {
			slog.Info("dados: sem segmento", "bloco", b.Name, "motivo", err)
			continue
		}
		lines, err := v.VoiceSegment(ctx, res.SegmentID, personas)
		slog.Info("dados: segmento pronto", "bloco", b.Name, "segmento", res.SegmentID, "falas", lines, "cortadas", len(res.Dropped),
			"duracao", time.Since(t0).Round(100*time.Millisecond), "erro", err)
	}
}

// opening monta e dá voz à abertura da sessão; ela fura a fila no primeiro minuto.
func (a *app) opening(ctx context.Context, ds *dataseg.Builder, v *voice.Voicer) {
	personas, err := persona.Load(a.env.ConfigDir)
	if err != nil {
		return
	}
	t0 := time.Now()
	res, err := ds.Build(ctx, dataseg.Opening)
	if err != nil {
		slog.Warn("abertura", "erro", err)
		return
	}
	if _, err := v.VoiceSegment(ctx, res.SegmentID, personas); err != nil {
		slog.Warn("abertura: voz", "erro", err)
		return
	}
	a.openingUntil.Store(a.now().Add(time.Minute).UnixNano())
	slog.Info("abertura pronta", "segmento", res.SegmentID, "duracao", time.Since(t0).Round(10*time.Millisecond))
}

// preferAt: na abertura da sessão, a abertura; na janela do tempo da Glória,
// o bloco "tempo" fura a fila.
func (a *app) preferAt(at time.Time) []string {
	if at.UnixNano() < a.openingUntil.Load() {
		return []string{dataseg.Opening}
	}
	sched, err := config.LoadSchedule(a.env.ConfigDir)
	if err != nil {
		return nil
	}
	if sl, ok := sched.Programs.At(at, a.env.Location); ok && sl.Kind == "weather" && sched.Programs.Weather.Block != "" {
		return []string{sched.Programs.Weather.Block}
	}
	return nil
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
	if !a.env.OnDemand() && a.env.ReplayWhenIdle && a.env.Viewers == 0 {
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
		if b.Data != "" {
			continue // bloco de dados: sem LLM (dataStock)
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
	s := &api.Server{Store: a.store, Now: a.now, MediaDir: envOr("TVTL_MEDIA_DIR", "/media"),
		ConfigDir: a.env.ConfigDir, Loc: a.env.Location, PublicMode: a.env.PublicMode, OnDemand: a.env.OnDemand(),
		SiteOrigin: a.env.SiteOrigin, AdminToken: a.env.AdminToken, MaxSSEPerIP: 3,
		Limits: session.Limits{MaxDur: a.env.SessionMaxDur, MaxUSD: a.env.SessionMaxUSD, IdleTimeout: a.env.SessionIdle}}
	slog.Info("api", "public_mode", a.env.PublicMode, "run_mode", a.env.RunMode, "site_origin", a.env.SiteOrigin,
		"admin_token", map[bool]string{true: "presente", false: "ausente"}[a.env.AdminToken != ""])
	srv := &http.Server{Addr: listen, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() { <-ctx.Done(); srv.Close() }()
	slog.Info("serve", "listen", listen, "clock_offset", a.env.ClockOffset)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

// consolidateMemory: funde memórias parecidas, aposenta as velhas e registra
// as piadas que bateram o limite da semana (persona_memory_log).
func (a *app) consolidateMemory(ctx context.Context) error {
	lex, _, err := a.pipeline().Lexicon(config.Schedule{})
	if err != nil {
		return err
	}
	personas, err := persona.Load(a.env.ConfigDir)
	if err != nil {
		return err
	}
	known, err := a.store.AllEntities(ctx)
	if err != nil {
		return err
	}
	c := &persona.Consolidator{Store: a.store, LLM: a.metered, Model: a.env.ModelFast, Lex: lex, HalfLifeDays: a.env.MemoryHalfLife,
		MaxUsesWeek: pipeline.MaxGagUsesWeek, Personas: personas, KnownEntities: known}
	rep, err := c.Run(ctx, a.now())
	slog.Info("memória consolidada", "fundidas", rep.Merged, "aposentadas", rep.Retired, "no_limite", rep.Capped, "descartadas", len(rep.Dropped), "erro", err)
	return err
}
