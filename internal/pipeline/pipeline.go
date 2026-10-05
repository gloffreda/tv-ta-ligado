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
	"github.com/gloffreda/tv-ta-ligado/internal/textutil"
)

var (
	ErrGenerateOff = errors.New("GENERATE=off")
	ErrBlackout    = errors.New("bloqueio eleitoral ativo: só reprise")
	ErrNoFacts     = errors.New("pauta sem fatos válidos")
)

// MaxGlossaryTerms: termos de glossário por segmento.
const MaxGlossaryTerms = 4

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

// Lexicon monta a allowlist (config/allowlist.yaml + name_exceptions) e os prenomes.
func (p *Pipeline) Lexicon(sched config.Schedule) (*check.Lexicon, []string, error) {
	allow, err := config.LoadAllowlist(p.ConfigDir)
	if err != nil {
		return nil, nil, fmt.Errorf("allowlist.yaml: %w", err)
	}
	allow = append(allow, sched.Check.NameExceptions...)
	names, err := config.LoadFirstNames(p.ConfigDir)
	if err != nil {
		return nil, nil, fmt.Errorf("first_names.txt: %w", err)
	}
	return check.NewLexicon(allow, names), allow, nil
}

// Generate produz e checa um segmento do bloco.
func (p *Pipeline) Generate(ctx context.Context, blockName string) (Result, error) {
	res, err := p.Draft(ctx, blockName)
	if err != nil || res.Status == "rejected" {
		return res, err
	}
	return p.CheckDraft(ctx, res.SegmentID)
}

// Draft monta a pauta, extrai fatos das matérias escolhidas e escreve o
// roteiro. O segmento fica em 'draft' (ou 'rejected').
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

	// 1. Pauta pelo título (temas fora do brief excluídos antes).
	cands, err := p.Store.CandidateArticles(ctx, now.Add(-p.CandidateWindow), now.Add(-p.ReuseWindow), 200)
	if err != nil {
		return res, err
	}
	cands, _ = rundown.Exclude(cands, sched.Exclude.URLParts, sched.Exclude.Keywords)
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

	// 2. Extração preguiçosa: só das matérias escolhidas, em ordem, até max_articles.
	type picked struct {
		a  store.Article
		fs []facts.Fact
	}
	extractor := &facts.Extractor{LLM: p.LLM, Model: p.Env.ModelFast, Now: p.Now}
	var withPerson, chosen []picked
	for _, a := range plan.Articles {
		if len(chosen) >= block.MaxArticles {
			break
		}
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
		// Humor: prefere matérias cujos fatos não citam pessoas.
		if block.Name == "humor" && facts.HasPerson(fs) {
			withPerson = append(withPerson, picked{a, fs})
			continue
		}
		chosen = append(chosen, picked{a, fs})
	}
	for _, wp := range withPerson {
		if len(chosen) >= block.MaxArticles {
			break
		}
		chosen = append(chosen, wp)
	}

	var segFacts []facts.Fact
	var items []store.RundownItem
	rank := 1
	for _, c := range chosen {
		id := c.a.ID
		items = append(items, store.RundownItem{ArticleID: &id, Rank: rank})
		rank++
		segFacts = append(segFacts, c.fs...)
	}
	extra := append(append([]facts.Fact{}, plan.Market...), plan.Weather...)
	var titles []string
	for _, c := range chosen {
		titles = append(titles, c.a.Title)
	}
	gl, err := p.glossaryFor(ctx, now, append(append([]facts.Fact{}, segFacts...), extra...), titles)
	if err != nil {
		return res, err
	}
	for _, f := range append(extra, gl...) {
		id := f.ID
		items = append(items, store.RundownItem{FactID: &id, Rank: rank})
		rank++
		segFacts = append(segFacts, f)
	}
	if err := p.Store.AddRundownItems(ctx, rundownID, items); err != nil {
		return res, err
	}
	if len(segFacts) == len(gl) { // só glossário não é pauta
		_ = p.Store.SetRundownStatus(ctx, rundownID, "empty")
		return reject(0, ErrNoFacts.Error(), nil)
	}
	_ = p.Store.SetRundownStatus(ctx, rundownID, "used")

	// 3. Roteiro: só fatos da pauta (com glossário), personas e as 10 memórias de maior peso.
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

// glossaryFor escolhe os termos de glossário citados na pauta (por termo ou
// sinônimo), no máximo MaxGlossaryTerms.
func (p *Pipeline) glossaryFor(ctx context.Context, now time.Time, segFacts []facts.Fact, titles []string) ([]facts.Fact, error) {
	terms, err := config.LoadGlossary(p.ConfigDir)
	if err != nil {
		return nil, nil // glossário é opcional
	}
	gl, err := p.Store.LatestFactsByKind(ctx, facts.Glossary, now)
	if err != nil {
		return nil, err
	}
	bySeries := map[string]facts.Fact{}
	for _, f := range gl {
		bySeries[f.Series] = f
	}
	var text string
	for _, f := range segFacts {
		text += " " + f.Claim
	}
	for _, t := range titles {
		text += " " + t
	}
	var out []facts.Fact
	for _, t := range terms {
		f, ok := bySeries[GlossarySeries(t.Term)]
		if !ok {
			continue
		}
		for _, a := range append([]string{t.Term}, t.Aliases...) {
			if textutil.ContainsPhrase(text, a) {
				out = append(out, f)
				break
			}
		}
		if len(out) >= MaxGlossaryTerms {
			break
		}
	}
	return out, nil
}

// GlossarySeries é a chave de série de um termo do glossário.
func GlossarySeries(term string) string { return "glossary:" + textutil.Normalize(term) }

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
	lex, allow, err := p.Lexicon(sched)
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

	env, err := p.env(ctx, block, lex, segFacts, lines, now)
	if err != nil {
		return res, err
	}
	flow := &check.Flow{
		Env:      env,
		Judge:    &check.Judge{LLM: p.LLM, Model: p.Env.ModelSmart},
		Rewriter: &script.Rewriter{LLM: p.LLM, Model: p.Env.ModelSmart, Block: block, Facts: segFacts, Allow: relevantAllow(allow, segFacts)},
	}
	outs, err := flow.CheckAll(ctx, lines, sched.Check.JudgeConcurrency)
	res.Outcomes = outs
	if err == nil {
		err = p.continuity(ctx, env, block, outs)
	}
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
	if status == "approved" {
		if err := p.Store.Air(ctx, segID, block.Name, "live"); err != nil {
			slog.Warn("airing", "erro", err)
		}
		// Memória: só de segmento aprovado; falha aqui não derruba o segmento.
		p.remember(ctx, segID, outs, personas, lex)
	}
	return res, nil
}

// relevantAllow: termos da allowlist que aparecem nos fatos do segmento.
func relevantAllow(allow []string, fs []facts.Fact) []string {
	var text string
	for _, f := range fs {
		text += " " + f.Claim
	}
	out := []string{"Orlando", "Duda"}
	seen := map[string]bool{}
	for _, a := range allow {
		k := textutil.Normalize(a)
		if !seen[k] && len(k) > 2 && textutil.ContainsPhrase(text, a) {
			seen[k] = true
			out = append(out, a)
		}
	}
	return out
}

func (p *Pipeline) env(ctx context.Context, block config.Block, lex *check.Lexicon, segFacts []facts.Fact, lines []check.Line, now time.Time) (*check.Env, error) {
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
	return &check.Env{
		Facts: factMap, SegmentFacts: segFacts, KnownEntities: known, Lex: lex,
		ForbiddenPhrases: block.ForbiddenPhrases, Now: now, Loc: p.Env.Location,
	}, nil
}

// continuity: se houve cortes, um passe com MODEL_FAST pode só remover ou
// encurtar falas banter órfãs. Encurtar = apagar palavras (verificado por
// código) e a fala volta pelo estágio determinístico.
func (p *Pipeline) continuity(ctx context.Context, env *check.Env, block config.Block, outs []check.Outcome) error {
	var kept, dropped []script.Kept
	lastKept := -1
	for i, o := range outs {
		if o.Status == "dropped" {
			dropped = append(dropped, script.Kept{Seq: i + 1, Line: o.Original})
		} else {
			kept = append(kept, script.Kept{Seq: i + 1, Line: o.Final})
			lastKept = i
		}
	}
	if len(dropped) == 0 || len(kept) == 0 {
		return nil
	}
	c := &script.Continuity{LLM: p.LLM, Model: p.Env.ModelFast}
	edits, err := c.Propose(ctx, kept, dropped)
	if err != nil {
		if llm.Fatal(err) {
			return err
		}
		_ = p.Store.Event(ctx, "continuity_failed", map[string]string{"error": err.Error()})
		return nil
	}
	for _, e := range edits {
		i := e.Seq - 1
		if i < 0 || i >= len(outs) || i == lastKept {
			continue
		}
		o := &outs[i]
		if o.Status == "dropped" || o.Status == "removed" || o.Final.Type != check.TypeBanter {
			continue // só banter vivo
		}
		if block.ClosingLine != "" && textutil.ContainsPhrase(o.Final.Text, block.ClosingLine) {
			continue
		}
		switch e.Action {
		case "remove":
			o.Status, o.Reason = "removed", "continuidade: fala de transição órfã após cortes"
			o.Attempts = append(o.Attempts, check.Attempt{N: len(o.Attempts) + 1, Line: o.Final, Results: []check.StageResult{{Stage: "continuity", Passed: false, Reasons: []string{o.Reason}}}})
		case "shorten":
			if !script.IsShortening(o.Final.Text, e.Text) {
				_ = p.Store.Event(ctx, "continuity_rejected", map[string]string{"text": e.Text, "reason": "não é só um encurtamento"})
				continue
			}
			nl := check.Line{Speaker: o.Final.Speaker, Type: o.Final.Type, Text: e.Text, FactIDs: o.Final.FactIDs}
			det := check.Deterministic(nl, env)
			rs := check.StageResult{Stage: "continuity", Passed: det.Passed, Reasons: append([]string{"encurtada"}, det.Reasons...)}
			o.Attempts = append(o.Attempts, check.Attempt{N: len(o.Attempts) + 1, Line: nl, Results: []check.StageResult{rs, det}})
			if det.Passed {
				o.Final = nl
			}
		}
	}
	return nil
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
	if o.Final.Text != o.Original.Text {
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

func (p *Pipeline) remember(ctx context.Context, segID int64, outs []check.Outcome, personas map[string]config.Persona, lex *check.Lexicon) {
	known, err := p.Store.AllEntities(ctx)
	if err != nil {
		slog.Warn("memória: entidades", "erro", err)
		return
	}
	var lines []check.Line
	for _, o := range outs {
		if o.Status != "dropped" && o.Status != "removed" {
			lines = append(lines, o.Final)
		}
	}
	ex := &persona.Extractor{LLM: p.LLM, Model: p.Env.ModelFast, Lex: lex}
	mems, dropped, err := ex.Extract(ctx, lines, personas, known)
	if err != nil {
		slog.Warn("memória: extração falhou", "erro", err)
		_ = p.Store.Event(ctx, "memory_failed", map[string]any{"segment_id": segID, "error": err.Error()})
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
