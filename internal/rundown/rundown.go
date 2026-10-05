// Package rundown monta a pauta de cada bloco.
package rundown

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/gloffreda/tv-ta-ligado/internal/config"
	"github.com/gloffreda/tv-ta-ligado/internal/facts"
	"github.com/gloffreda/tv-ta-ligado/internal/llm"
	"github.com/gloffreda/tv-ta-ligado/internal/store"
	"github.com/gloffreda/tv-ta-ligado/internal/textutil"
)

type Planner struct {
	LLM   llm.Client
	Model string
}

type Plan struct {
	Articles []store.Article // em ordem de importância
	Weather  []facts.Fact
	Market   []facts.Fact
}

var selectSchema = llm.MustSchema("rundown.json", `{
  "type": "object",
  "required": ["article_ids"],
  "properties": {"article_ids": {"type": "array", "items": {"type": "integer"}}}
}`)

const maxCandidates = 40

// FilterByKeywords mantém os artigos cujo título/resumo citam alguma palavra-chave.
func FilterByKeywords(arts []store.Article, kws []string) []store.Article {
	if len(kws) == 0 {
		return arts
	}
	var out []store.Article
	for _, a := range arts {
		for _, k := range kws {
			if textutil.ContainsPhrase(a.Title+" "+a.Summary, k) {
				out = append(out, a)
				break
			}
		}
	}
	return out
}

// PickWeather: capitais fixas + N rotativas, escolhidas pela hora do slot.
func PickWeather(all []facts.Fact, fixed []string, rotating int, slot time.Time) []facts.Fact {
	byCity := map[string]facts.Fact{}
	var others []facts.Fact
	isFixed := map[string]bool{}
	for _, c := range fixed {
		isFixed[textutil.Normalize(c)] = true
	}
	for _, f := range all {
		if len(f.Entities) == 0 {
			continue
		}
		city := textutil.Normalize(f.Entities[0])
		if isFixed[city] {
			byCity[city] = f
		} else {
			others = append(others, f)
		}
	}
	var out []facts.Fact
	for _, c := range fixed {
		if f, ok := byCity[textutil.Normalize(c)]; ok {
			out = append(out, f)
		}
	}
	if len(others) > 0 {
		start := (slot.Hour()*3 + slot.Minute()/20) % len(others)
		for i := 0; i < rotating && i < len(others); i++ {
			out = append(out, others[(start+i)%len(others)])
		}
	}
	return out
}

func (p *Planner) Plan(ctx context.Context, b config.Block, cands []store.Article, market, weather []facts.Fact, slot time.Time) (Plan, error) {
	plan := Plan{}
	if b.IncludeMarket {
		plan.Market = market
	}
	if len(b.WeatherFixed) > 0 || b.WeatherRotating > 0 {
		plan.Weather = PickWeather(weather, b.WeatherFixed, b.WeatherRotating, slot)
	}
	cands = FilterByKeywords(cands, b.ArticleKeywords)
	if len(cands) > maxCandidates {
		cands = cands[:maxCandidates]
	}
	if b.MaxArticles == 0 || len(cands) == 0 {
		return plan, nil
	}
	ids, err := p.selectWithLLM(ctx, b, cands)
	if err != nil {
		if llm.Fatal(err) {
			return plan, err
		}
		slog.Warn("pauta por LLM falhou; usando as mais recentes", "bloco", b.Name, "erro", err)
	}
	byID := map[int64]store.Article{}
	for _, a := range cands {
		byID[a.ID] = a
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		if a, ok := byID[id]; ok && !seen[id] && len(plan.Articles) < b.MaxArticles {
			plan.Articles = append(plan.Articles, a)
			seen[id] = true
		}
	}
	if len(plan.Articles) == 0 { // fallback determinístico: as mais recentes
		for _, a := range cands {
			if len(plan.Articles) >= b.MaxArticles {
				break
			}
			plan.Articles = append(plan.Articles, a)
		}
	}
	return plan, nil
}

func (p *Planner) selectWithLLM(ctx context.Context, b config.Block, cands []store.Article) ([]int64, error) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Bloco: %s\nOrientação do bloco:\n%s\nEscolha até %d notícias, da mais para a menos importante.\n\nCandidatas:\n", b.Name, b.Instructions, b.MaxArticles)
	for _, a := range cands {
		fmt.Fprintf(&sb, "[%d] %s — %s (%s)\n", a.ID, a.Title, truncate(a.Summary, 200), a.SourceName)
	}
	resp, err := p.LLM.Complete(ctx, llm.Request{
		Purpose: "rundown", Model: p.Model, MaxTokens: 500, Temperature: llm.Float(0),
		System: `Você é o editor de pauta de um telejornal brasileiro. Escolha notícias de interesse público, variadas e sem repetir o mesmo assunto. Evite notícias que dependam de opinião política. Responda apenas com JSON: {"article_ids":[id, ...]}`,
		Prompt: sb.String(),
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		ArticleIDs []int64 `json:"article_ids"`
	}
	if err := selectSchema.Decode(resp.Text, &out); err != nil {
		return nil, err
	}
	return out.ArticleIDs, nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
