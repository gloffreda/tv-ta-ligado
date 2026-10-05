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
}

// Write gera output/relatorio-AAAAMMDD-HHMMSS-<comando>.md e atualiza output/ultimo.md.
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
	_ = os.WriteFile(filepath.Join(dir, "ultimo.md"), []byte(md), 0o644)
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
	f("\n## Ingestão nesta execução\n\n- Artigos novos: %d\n- Fatos novos: %d\n", arts, nfacts)

	segs, err := st.SegmentsSince(ctx, r.Started)
	if err != nil {
		return "", err
	}
	f("\n## Segmentos nesta execução (%d)\n\n", len(segs))
	if len(segs) == 0 {
		f("Nenhum segmento gerado.\n")
	} else {
		f("| # | bloco | status | falas | cortadas | reescritas | custo (US$) | motivo |\n|---|---|---|---|---|---|---|---|\n")
		total := 0.0
		for _, s := range segs {
			dropped, rewritten := 0, 0
			for _, l := range s.Lines {
				switch l.Status {
				case "dropped":
					dropped++
				case "rewritten":
					rewritten++
				}
			}
			reason := ""
			if s.Reason != nil {
				reason = strings.ReplaceAll(*s.Reason, "|", "/")
			}
			total += s.CostUSD
			f("| %d | %s | %s | %d | %d | %d | %.4f | %s |\n", s.ID, s.Block, s.Status, len(s.Lines), dropped, rewritten, s.CostUSD, reason)
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
		f("| bloco | segmentos | aprovados | falas | cortadas | taxa de corte | reescritas | custo médio (US$) |\n|---|---|---|---|---|---|---|---|\n")
		for _, s := range stats {
			rate := 0.0
			if s.Lines > 0 {
				rate = 100 * float64(s.Dropped) / float64(s.Lines)
			}
			f("| %s | %d | %d | %d | %d | %.1f%% | %d | %.4f |\n", s.Block, s.Segments, s.Approved, s.Lines, s.Dropped, rate, s.Rewritten, s.AvgCostUSD)
		}
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
