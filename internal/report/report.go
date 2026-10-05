// Package report escreve, ao fim de cada execução, um relatório em Markdown
// na pasta output/ (fora do git).
package report

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gloffreda/tv-ta-ligado/internal/store"
)

type Run struct {
	Command  string
	Args     []string
	Started  time.Time
	Finished time.Time
	Err      error
	Notes    []string
	Every    map[string]time.Duration // grade (para a projeção diária)
}

// Write gera um único arquivo por execução: output/relatorio-AAAAMMDD-HHMMSS-<comando>.md.
func Write(ctx context.Context, dir string, st *store.Store, r Run, loc *time.Location, dayStart time.Time) (string, error) {
	md, err := Build(ctx, st, r, loc, dayStart)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := fmt.Sprintf("relatorio-%s-%s.md", r.Finished.In(loc).Format("20060102-150405"), r.Command)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(md), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func Build(ctx context.Context, st *store.Store, r Run, loc *time.Location, dayStart time.Time) (string, error) {
	var b strings.Builder
	f := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	ts := func(t time.Time) string { return t.In(loc).Format("02/01/2006 15:04:05") }

	f("# Relatório de execução — tvtl %s\n\n", r.Command)
	f("- Comando: `tvtl %s`\n", strings.TrimSpace(r.Command+" "+strings.Join(r.Args, " ")))
	f("- Início: %s · Fim: %s · Duração: %s\n", ts(r.Started), ts(r.Finished), r.Finished.Sub(r.Started).Round(time.Second))
	if r.Err != nil {
		f("- Resultado: **erro** — %s\n", r.Err)
	} else {
		f("- Resultado: ok\n")
	}
	for _, n := range r.Notes {
		f("- %s\n", n)
	}

	arts, nfacts, err := st.IngestCounts(ctx, r.Started)
	if err != nil {
		return "", err
	}
	byKind, err := st.FactsByKindSince(ctx, r.Started)
	if err != nil {
		return "", err
	}
	f("\n## Ingestão nesta execução\n\n- Artigos novos: %d\n- Fatos novos: %d\n", arts, nfacts)
	f("\n| kind | fatos novos | reconfirmados (dado idêntico, validade renovada) | total válido desta rodada |\n|---|---|---|---|\n")
	for _, k := range []string{"headline", "market", "weather"} {
		c := byKind[k]
		f("| %s | %d | %d | %d |\n", k, c.New, c.Confirmed, c.New+c.Confirmed)
	}

	exArts, extracted, kept, failed, err := st.ExtractionStats(ctx, r.Started)
	if err != nil {
		return "", err
	}
	f("\n### Extração de fatos de artigos\n\n")
	f("- Artigos processados: %d · com erro de LLM: %d\n", exArts, failed)
	if extracted > 0 {
		f("- Fatos extraídos pelo LLM: %d · aceitos pela validação literal: %d · descartados: %d (taxa de descarte %.1f%%)\n",
			extracted, kept, extracted-kept, 100*float64(extracted-kept)/float64(extracted))
	} else {
		f("- Fatos extraídos pelo LLM: 0 (taxa de descarte não se aplica)\n")
	}

	segs, err := st.SegmentsSince(ctx, r.Started)
	if err != nil {
		return "", err
	}
	f("\n## Segmentos nesta execução (%d)\n\n", len(segs))
	if len(segs) == 0 {
		f("Nenhum segmento gerado.\n")
	} else {
		f("| # | bloco | status | falas | cortadas | taxa de corte | reescritas | removidas (continuidade) | custo (US$) | motivo |\n|---|---|---|---|---|---|---|---|---|---|\n")
		total := 0.0
		for _, s := range segs {
			dropped, rewritten, removed := 0, 0, 0
			for _, l := range s.Lines {
				switch l.Status {
				case "dropped":
					dropped++
				case "rewritten":
					rewritten++
				case "removed":
					removed++
				}
			}
			rate := 0.0
			if len(s.Lines) > 0 {
				rate = 100 * float64(dropped) / float64(len(s.Lines))
			}
			reason := ""
			if s.Reason != nil {
				reason = strings.ReplaceAll(*s.Reason, "|", "/")
			}
			total += s.CostUSD
			f("| %d | %s | %s | %d | %d | %.1f%% | %d | %d | %.4f | %s |\n", s.ID, s.Block, s.Status, len(s.Lines), dropped, rate, rewritten, removed, s.CostUSD, reason)
		}
		f("\nCusto total dos segmentos: US$ %.4f · médio: US$ %.4f\n", total, total/float64(len(segs)))
		for _, s := range segs {
			var cut []store.LineView
			for _, l := range s.Lines {
				if l.Status == "dropped" {
					cut = append(cut, l)
				}
			}
			if len(cut) == 0 {
				continue
			}
			f("\n### Falas cortadas no segmento %d (%s)\n\n", s.ID, s.Block)
			for _, l := range cut {
				text := l.Text
				if l.OriginalText != nil {
					text = *l.OriginalText
				}
				reason := ""
				if l.RejectReason != nil {
					reason = *l.RejectReason
				}
				f("- %02d %s (%s): “%s”\n  - motivo: %s\n", l.Seq, l.Speaker, l.Type, text, reason)
			}
		}
	}

	stats, err := st.Stats(ctx)
	if err != nil {
		return "", err
	}
	f("\n## Acumulado por bloco (todas as execuções)\n\n")
	if len(stats) == 0 {
		f("Sem segmentos ainda.\n")
	} else {
		f("| bloco | segmentos | aprovados | falas | cortadas | taxa de corte | reescritas | removidas | custo médio (US$) |\n|---|---|---|---|---|---|---|---|---|\n")
		for _, s := range stats {
			rate := 0.0
			if s.Lines > 0 {
				rate = 100 * float64(s.Dropped) / float64(s.Lines)
			}
			f("| %s | %d | %d | %d | %d | %.1f%% | %d | %d | %.4f |\n", s.Block, s.Segments, s.Approved, s.Lines, s.Dropped, rate, s.Rewritten, s.Removed, s.AvgCostUSD)
		}
	}

	live, replay, err := st.AiringCounts(ctx, r.Started)
	if err != nil {
		return "", err
	}
	f("\n## Exibições nesta execução\n\n- Estreias: %d · Reprises: %d\n", live, replay)

	if err := costSection(ctx, &b, st, r, exArts); err != nil {
		return "", err
	}

	spent, err := st.SpentSince(ctx, dayStart)
	if err != nil {
		return "", err
	}
	f("\n## Orçamento\n\n- Gasto com LLM hoje: US$ %.4f\n", spent)

	evs, err := st.EventsSince(ctx, r.Started)
	if err != nil {
		return "", err
	}
	f("\n## Avisos nesta execução (%d)\n\n", len(evs))
	for _, e := range evs {
		f("- %s `%s` %s\n", ts(e.CreatedAt), e.Kind, e.Detail)
	}
	if len(evs) == 0 {
		f("Nenhum.\n")
	}
	return b.String(), nil
}

// costSection: custo por segmento, custo de extração por artigo e projeção diária.
func costSection(ctx context.Context, b *strings.Builder, st *store.Store, r Run, extractedArticles int) error {
	f := func(format string, a ...any) { fmt.Fprintf(b, format, a...) }
	byPurpose, err := st.CostSince(ctx, r.Started)
	if err != nil {
		return err
	}
	blocks, err := st.BlockCosts(ctx, r.Started)
	if err != nil {
		return err
	}
	f("\n## Custo\n\n")
	f("| propósito | US$ |\n|---|---|\n")
	total := 0.0
	for _, p := range []string{"rundown", "extract", "script", "judge", "rewrite", "continuity", "memory"} {
		f("| %s | %.4f |\n", p, byPurpose[p])
		total += byPurpose[p]
	}
	f("| **total** | **%.4f** |\n", total)
	if extractedArticles > 0 {
		f("\n- Custo de extração por artigo: US$ %.5f (%d artigos)\n", byPurpose["extract"]/float64(extractedArticles), extractedArticles)
	} else {
		f("\n- Custo de extração por artigo: sem extrações nesta execução\n")
	}
	if len(blocks) == 0 {
		f("- Sem segmentos para projetar custo.\n")
		return nil
	}
	// Custo por segmento aprovado = custo de todas as tentativas do bloco / aprovados.
	var allCost float64
	var allApproved int
	perBlock := map[string]float64{}
	f("\n| bloco | tentativas | aprovados | custo por segmento aprovado (US$) |\n|---|---|---|---|\n")
	for _, bc := range blocks {
		allCost += bc.TotalUSD
		allApproved += bc.Approved
		if bc.Approved > 0 {
			perBlock[bc.Block] = bc.TotalUSD / float64(bc.Approved)
			f("| %s | %d | %d | %.4f |\n", bc.Block, bc.Segments, bc.Approved, perBlock[bc.Block])
		} else {
			f("| %s | %d | 0 | — |\n", bc.Block, bc.Segments)
		}
	}
	if allApproved == 0 || len(r.Every) == 0 {
		f("\nProjeção diária: sem segmento aprovado ou sem grade.\n")
		return nil
	}
	avg := allCost / float64(allApproved)
	project := func(minutes float64) float64 {
		sum := 0.0
		for blk, every := range r.Every {
			c, ok := perBlock[blk]
			if !ok {
				c = avg
			}
			if every > 0 {
				sum += minutes / every.Minutes() * c
			}
		}
		return sum
	}
	f("\n### Projeção diária (grade atual, um segmento novo a cada `every` de cada bloco, sem reprise)\n\n")
	f("| cenário | horas no ar | US$/dia | US$/mês (30 dias) |\n|---|---|---|---|\n")
	full, prime := project(24*60), project(16*60)
	f("| 24/7 contínuo | 24 | %.2f | %.2f |\n", full, full*30)
	f("| só horário nobre (7h–23h) | 16 | %.2f | %.2f |\n", prime, prime*30)
	f("\nBlocos sem segmento aprovado nesta execução usam o custo médio (US$ %.4f). A extração está incluída no custo do segmento (é feita só para as matérias da pauta).\n", avg)
	return nil
}
