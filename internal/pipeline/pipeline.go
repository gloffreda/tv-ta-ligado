// Package pipeline orquestra pauta → fatos → roteiro → checagem → gravação.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/gloffreda/tv-ta-ligado/internal/check"
	"github.com/gloffreda/tv-ta-ligado/internal/config"
	"github.com/gloffreda/tv-ta-ligado/internal/facts"
	"github.com/gloffreda/tv-ta-ligado/internal/llm"
	"github.com/gloffreda/tv-ta-ligado/internal/persona"
	"github.com/gloffreda/tv-ta-ligado/internal/rundown"
	"github.com/gloffreda/tv-ta-ligado/internal/script"
	"github.com/gloffreda/tv-ta-ligado/internal/store"
)

var (
	ErrGenerateOff = errors.New("GENERATE=off")
	ErrBlackout    = errors.New("bloqueio eleitoral ativo: só reprise")
	ErrNoFacts     = errors.New("pauta sem fatos válidos")
)

type Pipeline struct {
	Store     *store.Store
	LLM       llm.Client // já com Metered
	Budget    interface{ CheckBudget(context.Context) error }
	Env       config.Env
	ConfigDir string
	Now       func() time.Time
	// Janela de candidatas e de "já usada" na pauta.
	CandidateWindow time.Duration
	ReuseWindow     time.Duration
}

func (p *Pipeline) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

type Result struct {
	SegmentID int64
	Status    string
	Reason    string
	Outcomes  []check.Outcome
}

// Guard verifica as chaves gerais antes de qualquer geração.
func (p *Pipeline) Guard(ctx context.Context) error {
	if !p.Env.Generate {
		return ErrGenerateOff
	}
	bo, err := config.LoadBlackout(p.ConfigDir)
	if err != nil {
		return fmt.Errorf("blackout.yaml: %w", err)
	}
	if w, ok := bo.Active(p.now()); ok {
		_ = p.Store.Event(ctx, "blackout", map[string]string{"window": w.Name})
		return fmt.Errorf("%w (%s)", ErrBlackout, w.Name)
	}
	if p.Budget != nil {
		if err := p.Budget.CheckBudget(ctx); err != nil {
			return err
		}
	}
	return nil
}

// Generate produz e checa um segmento do bloco.
func (p *Pipeline) Generate(ctx context.Context, blockName string) (Result, error) {
	res, err := p.Draft(ctx, blockName)
	if err != nil || res.Status == "rejected" {
		return res, err
	}
	return p.CheckDraft(ctx, res.SegmentID)
}

// Draft monta a pauta, extrai fatos e escreve o roteiro. O segmento fica
// em 'draft' (ou 'rejected', se não houver fatos ou o roteiro falhar).
func (p *Pipeline) Draft(ctx context.Context, blockName string) (Result, error) {
	if err := p.Guard(ctx); err != nil {
		return Result{}, err
	}
	sched, err := config.LoadSchedule(p.ConfigDir)
	if err != nil {
		return Result{}, err
	}
	block, ok := sched.Block(blockName)
	if !ok {
		return Result{}, fmt.Errorf("bloco %q não está em schedule.yaml", blockName)
	}
	personas, err := persona.Load(p.ConfigDir)
	if err != nil {
		return Result{}, err
	}
	now := p.now()

	rundownID, err := p.Store.CreateRundown(ctx, blockName, now)
	if err != nil {
		return Result{}, err
	}
	segID, err := p.Store.CreateSegment(ctx, rundownID, blockName)
	if err != nil {
		return Result{}, err
	}
	res := Result{SegmentID: segID}
	ctx = llm.WithSegment(ctx, segID)
	reject := func(attempts int, reason string, cause error) (Result, error) {
		res.Status, res.Reason = "rejected", reason
		bg := context.WithoutCancel(ctx)
		_ = p.Store.SetSegmentAttempts(bg, segID, attempts)
		if err := p.Store.FinishSegment(bg, segID, "rejected", reason); err != nil {
			return res, err
		}
		return res, cause
	}

	// 1. Pauta.
	cands, err := p.Store.CandidateArticles(ctx, now.Add(-p.CandidateWindow), now.Add(-p.ReuseWindow), 200)
	if err != nil {
		return res, err
	}
	market, err := p.Store.LatestFactsByKind(ctx, facts.Market, now)
	if err != nil {
		return res, err
	}
	weather, err := p.Store.LatestFactsByKind(ctx, facts.Weather, now)
	if err != nil {
		return res, err
	}
	planner := &rundown.Planner{LLM: p.LLM, Model: p.Env.ModelFast}
	plan, err := planner.Plan(ctx, block, cands, market, weather, now.In(p.Env.Location))
	if err != nil {
		return reject(0, "pauta: "+err.Error(), fatalOrNil(err))
	}
	for _, w := range plan.Warnings {
		_ = p.Store.Event(ctx, "rundown_fallback", map[string]any{"segment_id": segID, "block": blockName, "warning": w})
	}

	// 2. Fatos dos artigos escolhidos (extração sob demanda, uma vez por artigo).
	extractor := &facts.Extractor{LLM: p.LLM, Model: p.Env.ModelFast, Now: p.Now}
	var segFacts []facts.Fact
	var items []store.RundownItem
	rank := 1
	for _, a := range plan.Articles {
		fs, err := p.articleFacts(ctx, extractor, a, now)
		if err != nil {
			if llm.Fatal(err) {
				return reject(0, err.Error(), err)
			}
			continue // já registrado em system_events (extract_failed)
		}
		if len(fs) == 0 {
			continue
		}
		id := a.ID
		items = append(items, store.RundownItem{ArticleID: &id, Rank: rank})
		rank++
		segFacts = append(segFacts, fs...)
	}
	for _, f := range append(append([]facts.Fact{}, plan.Market...), plan.Weather...) {
		id := f.ID
		items = append(items, store.RundownItem{FactID: &id, Rank: rank})
		rank++
		segFacts = append(segFacts, f)
	}
	if err := p.Store.AddRundownItems(ctx, rundownID, items); err != nil {
		return res, err
	}
	if len(segFacts) == 0 {
		_ = p.Store.SetRundownStatus(ctx, rundownID, "empty")
		return reject(0, ErrNoFacts.Error(), nil)
	}
	_ = p.Store.SetRundownStatus(ctx, rundownID, "used")

	// 3. Roteiro: só fatos da pauta, personas e as 10 memórias de maior peso.
	mems, err := p.Store.TopMemories(ctx, 10, p.Env.MemoryHalfLife, now)
	if err != nil {
		return res, err
	}
	writer := &script.Writer{LLM: p.LLM, Model: p.Env.ModelSmart, Schedule: sched, Personas: personas}
	scr, attempts, err := writer.Write(ctx, script.Input{Block: block, Facts: segFacts, Memories: mems})
	if err != nil {
		return reject(attempts, err.Error(), fatalOrNil(err))
	}
	if err := p.Store.SetSegmentAttempts(ctx, segID, attempts); err != nil {
		return res, err
	}
	for i, l := range scr.Lines {
		if _, err := p.Store.InsertLine(ctx, store.Line{SegmentID: segID, Seq: i + 1, Speaker: l.Speaker, Type: l.Type, Text: l.Text, FactIDs: l.FactIDs}); err != nil {
			return res, err
		}
	}
	res.Status = "draft"
	return res, nil
}

func fatalOrNil(err error) error {
	if llm.Fatal(err) {
		return err
	}
	return nil
}

// CheckDraft checa as falas de um segmento em rascunho e decide o destino.
func (p *Pipeline) CheckDraft(ctx context.Context, segID int64) (Result, error) {
	res := Result{SegmentID: segID}
	seg, err := p.Store.Segment(ctx, segID)
	if err != nil {
		return res, err
	}
	if seg.Status != "draft" {
		return res, fmt.Errorf("segmento %d está %s, não draft", segID, seg.Status)
	}
	sched, err := config.LoadSchedule(p.ConfigDir)
	if err != nil {
		return res, err
	}
	block, ok := sched.Block(seg.Block)
	if !ok {
		return res, fmt.Errorf("bloco %q não está em schedule.yaml", seg.Block)
	}
	personas, err := persona.Load(p.ConfigDir)
	if err != nil {
		return res, err
	}
	now := p.now()
	ctx = llm.WithSegment(ctx, segID)

	segFacts, err := p.Store.RundownFacts(ctx, seg.RundownID, now)
	if err != nil {
		return res, err
	}
	stored, err := p.Store.LinesOf(ctx, segID)
	if err != nil {
		return res, err
	}
	lines := make([]check.Line, len(stored))
	for i, l := range stored {
		lines[i] = check.Line{Speaker: l.Speaker, Type: l.Type, Text: l.Text, FactIDs: l.FactIDs}
	}

	outs, err := p.check(ctx, block, sched, segFacts, lines, now)
	res.Outcomes = outs
	// Grava o que foi checado mesmo se a checagem parou no meio (orçamento).
	for i, o := range outs {
		if len(o.Attempts) > 0 {
			p.persistOutcome(ctx, stored[i].ID, o)
		}
	}
	if err != nil {
		res.Status, res.Reason = "rejected", "checagem interrompida: "+err.Error()
		_ = p.Store.FinishSegment(context.WithoutCancel(ctx), segID, "rejected", res.Reason)
		return res, err
	}

	status, reason := check.Decide(outs, check.Rules{
		MaxFactDropRatio: sched.Check.MaxFactDropRatio, MinLines: sched.Check.MinLinesAfter, ClosingLine: block.ClosingLine,
	})
	res.Status, res.Reason = status, reason
	if err := p.Store.FinishSegment(ctx, segID, status, reason); err != nil {
		return res, err
	}
	// Memória: só de segmento aprovado; falha aqui não derruba o segmento.
	if status == "approved" {
		p.remember(ctx, segID, outs, personas, sched.Check.NameExceptions)
	}
	return res, nil
}

func (p *Pipeline) check(ctx context.Context, block config.Block, sched config.Schedule, segFacts []facts.Fact, lines []check.Line, now time.Time) ([]check.Outcome, error) {
	known, err := p.Store.AllEntities(ctx)
	if err != nil {
		return nil, err
	}
	// Carrega também fact_ids citados fora da pauta: existem? estão válidos?
	var ids []int64
	for _, l := range lines {
		ids = append(ids, l.FactIDs...)
	}
	factMap, err := p.Store.FactsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, f := range segFacts {
		factMap[f.ID] = f
	}
	env := &check.Env{
		Facts: factMap, KnownEntities: known, NameExceptions: sched.Check.NameExceptions,
		ForbiddenPhrases: block.ForbiddenPhrases, Now: now, Loc: p.Env.Location,
	}
	flow := &check.Flow{
		Env:      env,
		Judge:    &check.Judge{LLM: p.LLM, Model: p.Env.ModelSmart},
		Rewriter: &script.Rewriter{LLM: p.LLM, Model: p.Env.ModelSmart, Block: block, Facts: segFacts},
	}
	return flow.CheckAll(ctx, lines, sched.Check.JudgeConcurrency)
}

func (p *Pipeline) persistOutcome(ctx context.Context, lineID int64, o check.Outcome) {
	ctx = context.WithoutCancel(ctx)
	for _, a := range o.Attempts {
		for _, r := range a.Results {
			detail := map[string]any{"text": a.Line.Text, "fact_ids": a.Line.FactIDs, "reasons": r.Reasons}
			for k, v := range r.Detail {
				detail[k] = v
			}
			if err := p.Store.LogCheck(ctx, lineID, a.N, r.Stage, r.Passed, detail); err != nil {
				slog.Error("check_log", "erro", err)
			}
		}
	}
	status := o.Status
	if status == "" {
		status = "dropped"
	}
	var original *string
	if len(o.Attempts) > 1 {
		t := o.Original.Text
		original = &t
	}
	text := o.Final.Text
	if text == "" {
		text = o.Original.Text
	}
	if err := p.Store.UpdateLine(ctx, lineID, status, text, original, o.Reason, o.Final.FactIDs); err != nil {
		slog.Error("update line", "erro", err)
	}
}

func (p *Pipeline) remember(ctx context.Context, segID int64, outs []check.Outcome, personas map[string]config.Persona, exc []string) {
	known, err := p.Store.AllEntities(ctx)
	if err != nil {
		slog.Warn("memória: entidades", "erro", err)
		return
	}
	var lines []check.Line
	for _, o := range outs {
		if o.Status != "dropped" {
			lines = append(lines, o.Final)
		}
	}
	ex := &persona.Extractor{LLM: p.LLM, Model: p.Env.ModelFast, NameExceptions: exc}
	mems, dropped, err := ex.Extract(ctx, lines, personas, known)
	if err != nil {
		slog.Warn("memória: extração falhou", "erro", err)
		return
	}
	for _, m := range mems {
		if err := p.Store.InsertMemory(ctx, m, segID); err != nil {
			slog.Warn("memória: gravação", "erro", err)
		}
	}
	if len(dropped) > 0 {
		_ = p.Store.Event(ctx, "memory_dropped", map[string]any{"segment_id": segID, "dropped": dropped})
	}
	_ = p.Store.RefreshSegmentCost(ctx, segID)
}
