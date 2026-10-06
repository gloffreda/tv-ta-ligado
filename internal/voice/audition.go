package voice

import (
	"context"
	"fmt"
	"html"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/gloffreda/tv-ta-ligado/internal/audio"
	"github.com/gloffreda/tv-ta-ligado/internal/speech"
	"github.com/gloffreda/tv-ta-ligado/internal/tts"
)

type ProviderInfo struct {
	Approved       bool    `yaml:"approved"`
	Reason         string  `yaml:"reason"`
	License        string  `yaml:"license"`
	CPUs           float64 `yaml:"cpus"`
	RTF            float64 `yaml:"rtf"`
	MinutesPerHour float64 `yaml:"minutes_per_hour"`
	PricePerMChar  float64 `yaml:"price_per_mchar"`
}

type TTSConfig struct {
	Providers    map[string]ProviderInfo `yaml:"providers"`
	MonthlyChars int                     `yaml:"monthly_chars"`
	Audition     struct {
		Text       map[string]string      `yaml:"text"`
		Candidates map[string][]tts.Voice `yaml:"candidates"`
	} `yaml:"audition"`
}

func LoadTTSConfig(path string) (TTSConfig, error) {
	var c TTSConfig
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	return c, yaml.Unmarshal(b, &c)
}

// Audition gera audition/<persona>/<letra>.mp3 sem o nome do provedor, o mapa
// key.md e um index.html estático para ouvir lado a lado. Só candidatas de
// provedores aprovados e disponíveis entram.
//
// only: gera só essas personas e preserva as letras das outras (lidas do
// key.md existente), para acrescentar uma voz nova sem reembaralhar as antigas.
func Audition(ctx context.Context, cfg TTSConfig, providers map[string]tts.Provider, proc audio.Processor, dict speech.Dict, out string, seed int64, only ...string) (string, error) {
	type entry struct {
		Persona, Letter string
		Voice           tts.Voice
		Info            ProviderInfo
		Seconds, Proc   float64
		Raw             string // linha preservada do key.md anterior
	}
	var all []entry
	keep := map[string]bool{}
	for _, o := range only {
		keep[o] = true
	}
	if len(only) > 0 {
		old, _ := os.ReadFile(filepath.Join(out, "key.md"))
		for _, ln := range strings.Split(string(old), "\n") {
			cols := strings.Split(ln, "|")
			if len(cols) < 4 || !strings.HasSuffix(strings.TrimSpace(cols[2]), ".mp3") {
				continue
			}
			persona := strings.TrimSpace(cols[1])
			if keep[persona] {
				continue
			}
			letter := strings.TrimSuffix(filepath.Base(strings.TrimSpace(cols[2])), ".mp3")
			all = append(all, entry{Persona: persona, Letter: letter, Raw: ln})
		}
	}
	var skipped []string
	personas := make([]string, 0, len(cfg.Audition.Candidates))
	for p := range cfg.Audition.Candidates {
		personas = append(personas, p)
	}
	sort.Strings(personas)
	rng := rand.New(rand.NewSource(seed))
	for _, persona := range personas {
		if len(only) > 0 && !keep[persona] {
			continue
		}
		cands := append([]tts.Voice{}, cfg.Audition.Candidates[persona]...)
		rng.Shuffle(len(cands), func(i, j int) { cands[i], cands[j] = cands[j], cands[i] }) // às cegas
		text := cfg.Audition.Text[persona]
		spoken := speech.Normalize(text, dict)
		if err := speech.VerifyNumbers(text, spoken); err != nil {
			return "", fmt.Errorf("texto da audição de %s: %w", persona, err)
		}
		dir := filepath.Join(out, persona)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		letter := 'A'
		for _, v := range cands {
			info := cfg.Providers[v.Provider]
			p, ok := providers[v.Provider]
			if !info.Approved || !ok {
				skipped = append(skipped, fmt.Sprintf("%s/%s (%s)", v.Provider, v.Name, firstNonEmpty(info.Reason, "provedor indisponível")))
				continue
			}
			t0 := time.Now()
			a, _, err := p.Synthesize(ctx, spoken, v)
			if err != nil {
				return "", fmt.Errorf("%s/%s: %w", v.Provider, v.Name, err)
			}
			procSec := time.Since(t0).Seconds()
			wav, dur, err := proc.Canonical(ctx, a, v.Pitch)
			if err != nil {
				return "", err
			}
			mp3, err := proc.Encode(ctx, wav, "mp3")
			if err != nil {
				return "", err
			}
			if err := os.WriteFile(filepath.Join(dir, string(letter)+".mp3"), mp3, 0o644); err != nil {
				return "", err
			}
			all = append(all, entry{Persona: persona, Letter: string(letter), Voice: v, Info: info, Seconds: dur.Seconds(), Proc: procSec})
			letter++
		}
	}

	// key.md
	var k strings.Builder
	fmt.Fprintf(&k, "# Audição às cegas — mapa das letras\n\nGerado em %s. **Não abra antes de ouvir.**\n\n", time.Now().UTC().Format("02/01/2006 15:04 UTC"))
	fmt.Fprintf(&k, "Custo/mês estimado para %s caracteres. RTF = segundos de processamento por segundo de áudio no benchmark (limite de CPU de produção).\n\n", fmtThousands(cfg.MonthlyChars))
	k.WriteString("| avatar | arquivo | provedor | voz | rate | pitch (st) | estilo | RTF (benchmark) | duração (s) | custo/mês (US$) |\n|---|---|---|---|---|---|---|---|---|---|\n")
	sort.SliceStable(all, func(i, j int) bool { return all[i].Persona < all[j].Persona })
	for _, e := range all {
		if e.Raw != "" {
			k.WriteString(e.Raw + "\n")
			continue
		}
		cost := float64(cfg.MonthlyChars) * e.Info.PricePerMChar / 1e6
		fmt.Fprintf(&k, "| %s | %s/%s.mp3 | %s | %s | %.2f | %+.1f | %s | %.2f | %.1f | %.2f |\n",
			e.Persona, e.Persona, e.Letter, e.Voice.Provider, e.Voice.Name, e.Voice.Rate, e.Voice.Pitch, firstNonEmpty(e.Voice.Style, "—"), e.Info.RTF, e.Seconds, cost)
	}
	if len(skipped) > 0 {
		k.WriteString("\nFora da audição: " + strings.Join(skipped, "; ") + "\n")
	}
	k.WriteString("\nPara escolher: copie provedor, voz, rate e pitch para `voice:` em `config/personas/<avatar>.yaml`.\n")
	if err := os.WriteFile(filepath.Join(out, "key.md"), []byte(k.String()), 0o644); err != nil {
		return "", err
	}

	// index.html (estático, sem servidor)
	var h strings.Builder
	h.WriteString(`<!doctype html><html lang="pt-BR"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Audição às cegas — TV Tá Ligado</title>
<style>:root{--bg:#fafaf7;--fg:#1d1d1b;--muted:#6b6b66;--card:#fff;--line:#e3e2dc;--accent:#d2491d}
@media (prefers-color-scheme:dark){:root{--bg:#161614;--fg:#ecebe6;--muted:#a3a29b;--card:#1f1f1c;--line:#34332e;--accent:#f0794d}}
body{margin:0;background:var(--bg);color:var(--fg);font:16px/1.5 system-ui,-apple-system,"Segoe UI",sans-serif}
main{max-width:1100px;margin:0 auto;padding:24px 16px}h1{font-size:1.6rem;margin:0 0 4px}p.lead{color:var(--muted);margin:0 0 24px}
section{margin-bottom:32px}h2{font-size:1.2rem;border-bottom:2px solid var(--accent);display:inline-block;padding-bottom:2px}
.grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(240px,1fr));gap:12px}
.card{background:var(--card);border:1px solid var(--line);border-radius:10px;padding:14px}
.card b{font-size:1.4rem;color:var(--accent)}audio{width:100%;margin-top:8px}blockquote{color:var(--muted);margin:8px 0 16px;font-style:italic}</style></head><body><main>
<h1>Audição às cegas</h1><p class="lead">Ouça cada candidata sem saber o provedor. O mapa está em <code>key.md</code>, para abrir depois.</p>`)
	for _, persona := range personas {
		fmt.Fprintf(&h, `<section><h2>%s</h2><blockquote>%s</blockquote><div class="grid">`, html.EscapeString(strings.Title(persona)), html.EscapeString(cfg.Audition.Text[persona]))
		for _, e := range all {
			if e.Persona == persona {
				fmt.Fprintf(&h, `<div class="card"><b>%s</b><audio controls preload="none" src="%s/%s.mp3"></audio></div>`, e.Letter, persona, e.Letter)
			}
		}
		h.WriteString(`</div></section>`)
	}
	h.WriteString(`</main></body></html>`)
	if err := os.WriteFile(filepath.Join(out, "index.html"), []byte(h.String()), 0o644); err != nil {
		return "", err
	}
	return k.String(), nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func fmtThousands(n int) string {
	s := fmt.Sprint(n)
	var out []string
	for len(s) > 3 {
		out = append([]string{s[len(s)-3:]}, out...)
		s = s[:len(s)-3]
	}
	return strings.Join(append([]string{s}, out...), ".")
}
