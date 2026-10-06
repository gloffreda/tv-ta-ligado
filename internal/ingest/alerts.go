package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gloffreda/tv-ta-ligado/internal/config"
	"github.com/gloffreda/tv-ta-ligado/internal/facts"
)

// inmetAlert: campos usados de https://apiprevmet3.inmet.gov.br/avisos/ativos.
type inmetAlert struct {
	ID         string `json:"id"`
	Descricao  string `json:"descricao"`
	Severidade string `json:"severidade"`
	Estados    string `json:"estados"`
	Regioes    string `json:"regioes"`
	Inicio     string `json:"inicio"` // "2026-10-05 08:55"
	Fim        string `json:"fim"`
	Encerrado  string `json:"encerrado"`
}

var severityRank = map[string]int{"grande perigo": 3, "perigo": 2, "perigo potencial": 1}

func (in *Ingester) alerts(ctx context.Context) (int, error) {
	a := in.Feeds.Alerts
	if _, err := in.Store.UpsertSource(ctx, a.SourceName, "alerts", a.URL, "cc-by"); err != nil {
		return 0, err
	}
	body, err := in.getRetry(ctx, []string{a.URL})
	if err != nil {
		return 0, err
	}
	fs, err := AlertFacts(body, a, in.now(), in.Loc)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, f := range fs {
		if _, err := in.Store.UpsertFact(ctx, f); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// AlertFacts transforma a resposta do INMET em fatos: os mais graves primeiro,
// só os vigentes agora, no máximo a.Max.
func AlertFacts(body []byte, a config.Alerts, now time.Time, loc *time.Location) ([]facts.Fact, error) {
	var res map[string][]inmetAlert
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, fmt.Errorf("json: %w", err)
	}
	all := append(append([]inmetAlert{}, res["hoje"]...), res["futuro"]...)
	type cand struct {
		al         inmetAlert
		start, end time.Time
	}
	var cs []cand
	for _, al := range all {
		if strings.EqualFold(al.Encerrado, "true") || al.Descricao == "" {
			continue
		}
		st, err1 := time.ParseInLocation("2006-01-02 15:04", al.Inicio, loc)
		en, err2 := time.ParseInLocation("2006-01-02 15:04", al.Fim, loc)
		if err1 != nil || err2 != nil || !en.After(now) || st.After(now.Add(24*time.Hour)) {
			continue
		}
		cs = append(cs, cand{al, st, en})
	}
	sort.SliceStable(cs, func(i, j int) bool {
		ri, rj := severityRank[strings.ToLower(cs[i].al.Severidade)], severityRank[strings.ToLower(cs[j].al.Severidade)]
		if ri != rj {
			return ri > rj
		}
		return cs[i].start.Before(cs[j].start)
	})
	max := a.Max
	if max <= 0 {
		max = 3
	}
	var out []facts.Fact
	for _, c := range cs {
		if len(out) >= max {
			break
		}
		out = append(out, AlertFact(c.al.ID, c.al.Descricao, c.al.Severidade, splitList(c.al.Estados), splitList(c.al.Regioes), c.start, c.end, a, now, loc))
	}
	return out, nil
}

// AlertFact: a frase é montada só com campos do aviso (lida literalmente no ar).
func AlertFact(id, desc, sev string, estados, regioes []string, start, end time.Time, a config.Alerts, now time.Time, loc *time.Location) facts.Fact {
	desc, sev = strings.ToLower(strings.TrimSpace(desc)), strings.ToLower(strings.TrimSpace(sev))
	ents := []facts.Entity{{Name: a.SourceName, Type: facts.Org}}
	var onde string
	if len(estados) <= 3 {
		onde = joinBR(estados)
		for _, e := range estados {
			ents = append(ents, facts.Entity{Name: e, Type: facts.Place})
		}
	} else {
		onde = fmt.Sprintf("áreas de %d estados das regiões %s", len(estados), joinBR(regioes))
		for _, r := range regioes {
			ents = append(ents, facts.Entity{Name: r, Type: facts.Place})
		}
	}
	if len(regioes) == 1 && len(estados) > 3 {
		onde = fmt.Sprintf("áreas de %d estados da região %s", len(estados), regioes[0])
	}
	e := end.In(loc)
	claim := fmt.Sprintf("O %s emitiu aviso de %s, com grau de severidade %s, para %s, válido até as %s de %s.",
		a.SourceName, desc, sev, onde, e.Format("15h04"), e.Format("02/01/2006"))
	return facts.Fact{
		Kind: facts.Alert, Claim: claim, Entities: ents, AsOf: start, SourceName: a.SourceName,
		SourceURL: a.URL + "#" + id, Series: "inmet:" + id, ExpiresAt: end,
	}
}

func splitList(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// joinBR: "a", "a e b", "a, b e c".
func joinBR(xs []string) string {
	switch len(xs) {
	case 0:
		return ""
	case 1:
		return xs[0]
	}
	return strings.Join(xs[:len(xs)-1], ", ") + " e " + xs[len(xs)-1]
}
