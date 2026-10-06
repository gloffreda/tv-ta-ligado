// Package audit roda a auditoria adversarial do checador (estágio 1 + juiz
// real) contra os casos de testdata/adversarial.
package audit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/gloffreda/tv-ta-ligado/internal/check"
	"github.com/gloffreda/tv-ta-ligado/internal/facts"
)

type CaseFact struct {
	ID       int64          `yaml:"id"`
	Kind     string         `yaml:"kind"`
	Claim    string         `yaml:"claim"`
	Value    *float64       `yaml:"value"`
	Unit     string         `yaml:"unit"`
	Entities []facts.Entity `yaml:"entities"`
	Source   string         `yaml:"source"`
	// Sensitive: morte, violência, desastre ou doença (modo sério, sem piada).
	Sensitive bool `yaml:"sensitive"`
}

type Case struct {
	ID            string         `yaml:"id"`
	Why           string         `yaml:"why"`
	Type          string         `yaml:"type"`
	Speaker       string         `yaml:"speaker"`
	Line          string         `yaml:"line"`
	Facts         []CaseFact     `yaml:"facts"`
	FactIDs       []int64        `yaml:"fact_ids"`
	KnownEntities []facts.Entity `yaml:"known_entities"`
	Expect        string         `yaml:"expect"` // reject | pass
	File          string         `yaml:"-"`
}

// Load lê todos os *.yaml do diretório.
func Load(dir string) ([]Case, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	var all []Case
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var cs []Case
		if err := yaml.Unmarshal(b, &cs); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		for i := range cs {
			cs[i].File = filepath.Base(f)
			if cs[i].Speaker == "" {
				cs[i].Speaker = "duda"
				if cs[i].Type == check.TypeFact {
					cs[i].Speaker = "orlando"
				}
			}
			if cs[i].Expect != "reject" && cs[i].Expect != "pass" {
				return nil, fmt.Errorf("%s/%s: expect deve ser reject|pass", f, cs[i].ID)
			}
		}
		all = append(all, cs...)
	}
	return all, nil
}

// Asof: "agora" dos casos (as datas dos fatos são de 02 a 06/10/2026).
var Asof = time.Date(2026, 10, 6, 12, 0, 0, 0, time.FixedZone("BRT", -3*3600))

func (c Case) toFacts() []facts.Fact {
	out := make([]facts.Fact, len(c.Facts))
	for i, f := range c.Facts {
		src := f.Source
		if src == "" {
			src = "Fonte do caso"
		}
		out[i] = facts.Fact{ID: f.ID, Kind: facts.Kind(f.Kind), Claim: f.Claim, Value: f.Value, Unit: f.Unit, Entities: f.Entities,
			AsOf: Asof, SourceName: src, SourceURL: "https://exemplo.invalid/" + c.ID, ExpiresAt: facts.NoExpiry, Sensitive: f.Sensitive}
	}
	return out
}

type Result struct {
	Case       Case
	Rejected   bool
	Stage      string // deterministic | judge | "" (aprovado)
	Reasons    []string
	JudgeError string
}

func (r Result) Outcome() string {
	switch {
	case r.Case.Expect == "reject" && r.Rejected:
		return "TP"
	case r.Case.Expect == "reject" && !r.Rejected:
		return "FN"
	case r.Case.Expect == "pass" && r.Rejected:
		return "FP"
	default:
		return "TN"
	}
}

// Run avalia os casos: estágio determinístico e, se passar, o juiz real.
func Run(ctx context.Context, cases []Case, lex *check.Lexicon, judge *check.Judge, concurrency int) ([]Result, error) {
	var known []facts.Entity // o banco real conhece as entidades de todos os fatos
	for _, c := range cases {
		for _, f := range c.Facts {
			known = append(known, f.Entities...)
		}
		known = append(known, c.KnownEntities...)
	}
	results := make([]Result, len(cases))
	errs := make([]error, len(cases))
	sem := make(chan struct{}, max(1, concurrency))
	var wg sync.WaitGroup
	for i, c := range cases {
		wg.Add(1)
		go func(i int, c Case) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			fs := c.toFacts()
			fm := map[int64]facts.Fact{}
			for _, f := range fs {
				fm[f.ID] = f
			}
			env := &check.Env{Facts: fm, SegmentFacts: fs, KnownEntities: known, Lex: lex, Now: Asof, Loc: Asof.Location(), Sensitive: facts.AnySensitive(fs)}
			line := check.Line{Speaker: c.Speaker, Type: c.Type, Text: c.Line, FactIDs: c.FactIDs}
			r := Result{Case: c}
			det := check.Deterministic(line, env)
			if !det.Passed {
				r.Rejected, r.Stage, r.Reasons = true, check.StageDeterministic, det.Reasons
				results[i] = r
				return
			}
			var refs []facts.Fact
			if c.Type == check.TypeBanter {
				refs = fs
			} else {
				for _, id := range c.FactIDs {
					refs = append(refs, fm[id])
				}
			}
			jr, err := judge.Evaluate(ctx, line, refs)
			if err != nil {
				errs[i] = fmt.Errorf("%s: %w", c.ID, err)
				r.JudgeError = err.Error()
				r.Rejected, r.Stage = true, check.StageJudge // juiz indisponível reprova (fail closed)
				results[i] = r
				return
			}
			switch {
			case c.Type == check.TypeBanter && jr.NewFactualClaim:
				r.Rejected, r.Reasons = true, []string{"afirmação nova: " + jr.Claim}
			case c.Type == check.TypeFact && !jr.Entailed:
				r.Rejected, r.Reasons = true, jr.Unsupported
			}
			if jr.RealPersonMocked {
				r.Rejected = true
				r.Reasons = append(r.Reasons, "zomba de pessoa real")
			}
			if env.Sensitive && jr.IsJoke {
				r.Rejected = true
				r.Reasons = append(r.Reasons, "modo sério: piada em segmento com notícia sensível")
			}
			if r.Rejected {
				r.Stage = check.StageJudge
			}
			results[i] = r
		}(i, c)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return results, err
		}
	}
	return results, nil
}

// Matrix devolve TP, FN, FP, TN.
func Matrix(rs []Result) (tp, fn, fp, tn int) {
	for _, r := range rs {
		switch r.Outcome() {
		case "TP":
			tp++
		case "FN":
			fn++
		case "FP":
			fp++
		default:
			tn++
		}
	}
	return
}

// Report formata a matriz, as categorias e os erros em Markdown.
func Report(rs []Result, title string, costUSD float64) string {
	var b strings.Builder
	tp, fn, fp, tn := Matrix(rs)
	fmt.Fprintf(&b, "## %s\n\n", title)
	fmt.Fprintf(&b, "| | reprovado | aprovado |\n|---|---|---|\n")
	fmt.Fprintf(&b, "| **deveria reprovar** (%d) | %d (VP) | **%d (FN)** |\n", tp+fn, tp, fn)
	fmt.Fprintf(&b, "| **deveria passar** (%d) | **%d (FP)** | %d (VN) |\n\n", fp+tn, fp, tn)
	byFile := map[string][2]int{}
	stages := map[string]int{}
	for _, r := range rs {
		v := byFile[r.Case.File]
		v[0]++
		if r.Outcome() == "TP" || r.Outcome() == "TN" {
			v[1]++
		}
		byFile[r.Case.File] = v
		if r.Outcome() == "TP" {
			stages[r.Stage]++
		}
	}
	files := make([]string, 0, len(byFile))
	for f := range byFile {
		files = append(files, f)
	}
	sort.Strings(files)
	b.WriteString("| arquivo | casos | acertos |\n|---|---|---|\n")
	for _, f := range files {
		fmt.Fprintf(&b, "| %s | %d | %d |\n", f, byFile[f][0], byFile[f][1])
	}
	fmt.Fprintf(&b, "\nReprovações corretas por estágio: determinístico %d · juiz %d\n", stages[check.StageDeterministic], stages[check.StageJudge])
	fmt.Fprintf(&b, "Custo do juiz nesta auditoria: US$ %.4f\n", costUSD)
	var errs []string
	for _, r := range rs {
		if o := r.Outcome(); o == "FN" || o == "FP" {
			errs = append(errs, fmt.Sprintf("- **%s %s** (%s): %q → %s %v %s", o, r.Case.ID, r.Case.Why, r.Case.Line, r.Stage, r.Reasons, r.JudgeError))
		}
	}
	b.WriteString("\n### Erros\n\n")
	if len(errs) == 0 {
		b.WriteString("Nenhum.\n")
	}
	for _, e := range errs {
		b.WriteString(e + "\n")
	}
	return b.String()
}

// detOnly roda só o estágio determinístico de um caso (usado em testes).
func detOnly(c Case, fs []facts.Fact, lex *check.Lexicon) check.StageResult {
	fm := map[int64]facts.Fact{}
	var known []facts.Entity
	for _, f := range fs {
		fm[f.ID] = f
		known = append(known, f.Entities...)
	}
	env := &check.Env{Facts: fm, SegmentFacts: fs, KnownEntities: append(known, c.KnownEntities...), Lex: lex, Now: Asof, Loc: Asof.Location()}
	return check.Deterministic(check.Line{Speaker: c.Speaker, Type: c.Type, Text: c.Line, FactIDs: c.FactIDs}, env)
}
