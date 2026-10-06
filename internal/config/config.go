// Package config carrega variáveis de ambiente e os YAML de config/.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/gloffreda/tv-ta-ligado/internal/tts"
)

// Env reúne as variáveis de ambiente do processo.
type Env struct {
	DatabaseURL      string
	AnthropicAPIKey  string
	ModelFast        string
	ModelSmart       string
	JudgeBanterModel string           // JUDGE_BANTER_MODEL (padrão: MODEL_SMART)
	Prices           map[string]Price // por modelo
	MaxDailyUSD      float64
	Generate         bool
	IngestInterval   time.Duration
	ConfigDir        string
	Location         *time.Location
	MemoryHalfLife   float64 // dias
	UserAgent        string
	OutputDir        string
	ReplayWhenIdle   bool          // REPLAY_WHEN_IDLE
	Viewers          int           // VIEWERS (no Sprint 3 vira a contagem real)
	ReplayWindow     time.Duration // REPLAY_WINDOW
}

// Price em dólares por milhão de tokens.
type Price struct {
	InputPerMTok  float64
	OutputPerMTok float64
}

func LoadEnv() (Env, error) {
	var e Env
	var err error
	e.DatabaseURL = get("DATABASE_URL", "postgres://tvtl:tvtl@localhost:5432/tvtl?sslmode=disable")
	e.AnthropicAPIKey = os.Getenv("ANTHROPIC_API_KEY")
	e.ModelFast = get("MODEL_FAST", "claude-haiku-4-5-20251001")
	e.ModelSmart = get("MODEL_SMART", "claude-sonnet-5-5")
	e.JudgeBanterModel = get("JUDGE_BANTER_MODEL", e.ModelFast)
	fast := Price{}
	smart := Price{}
	if fast.InputPerMTok, err = getFloat("PRICE_FAST_INPUT_PER_MTOK", 1.00); err != nil {
		return e, err
	}
	if fast.OutputPerMTok, err = getFloat("PRICE_FAST_OUTPUT_PER_MTOK", 5.00); err != nil {
		return e, err
	}
	if smart.InputPerMTok, err = getFloat("PRICE_SMART_INPUT_PER_MTOK", 2.00); err != nil {
		return e, err
	}
	if smart.OutputPerMTok, err = getFloat("PRICE_SMART_OUTPUT_PER_MTOK", 10.00); err != nil {
		return e, err
	}
	e.Prices = map[string]Price{e.ModelFast: fast, e.ModelSmart: smart}
	if e.MaxDailyUSD, err = getFloat("MAX_DAILY_USD", 5.00); err != nil {
		return e, err
	}
	switch strings.ToLower(get("GENERATE", "on")) {
	case "on", "true", "1":
		e.Generate = true
	case "off", "false", "0":
		e.Generate = false
	default:
		return e, fmt.Errorf("GENERATE deve ser on|off")
	}
	if e.IngestInterval, err = time.ParseDuration(get("INGEST_INTERVAL", "5m")); err != nil {
		return e, fmt.Errorf("INGEST_INTERVAL: %w", err)
	}
	e.ConfigDir = get("TVTL_CONFIG_DIR", "config")
	if e.Location, err = time.LoadLocation(get("TVTL_TIMEZONE", "America/Sao_Paulo")); err != nil {
		return e, err
	}
	if e.MemoryHalfLife, err = getFloat("MEMORY_HALF_LIFE_DAYS", 7); err != nil {
		return e, err
	}
	e.OutputDir = get("TVTL_OUTPUT_DIR", "output")
	switch strings.ToLower(get("REPLAY_WHEN_IDLE", "off")) {
	case "on", "true", "1":
		e.ReplayWhenIdle = true
	case "off", "false", "0":
	default:
		return e, fmt.Errorf("REPLAY_WHEN_IDLE deve ser on|off")
	}
	if e.Viewers, err = strconv.Atoi(get("VIEWERS", "1")); err != nil {
		return e, fmt.Errorf("VIEWERS: %w", err)
	}
	if e.ReplayWindow, err = time.ParseDuration(get("REPLAY_WINDOW", "6h")); err != nil {
		return e, fmt.Errorf("REPLAY_WINDOW: %w", err)
	}
	e.UserAgent = get("HTTP_USER_AGENT", "tvtl/0.1 (+https://github.com/gloffreda/tv-ta-ligado)")
	return e, nil
}

func get(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func getFloat(k string, def float64) (float64, error) {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return def, nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", k, err)
	}
	return f, nil
}

// ---- YAML ----

type Feeds struct {
	RSS     []RSSFeed   `yaml:"rss"`
	BCB     []BCBSeries `yaml:"bcb"`
	Weather Weather     `yaml:"weather"`
}

type RSSFeed struct {
	Name      string `yaml:"name"`   // único (tabela sources)
	Credit    string `yaml:"credit"` // como a fonte é citada no ar
	URL       string `yaml:"url"`
	License   string `yaml:"license"` // cc-by | none
	StoreBody bool   `yaml:"store_body"`
	Charset   string `yaml:"charset"` // opcional (ex.: iso-8859-1)
}

type BCBSeries struct {
	Code     int      `yaml:"code"`
	Key      string   `yaml:"key"`
	Label    string   `yaml:"label"`
	Unit     string   `yaml:"unit"`
	Claim    string   `yaml:"claim"` // modelo com {valor} e {data}
	Min      float64  `yaml:"min"`   // faixa de sanidade: confirma que o código é a série certa
	Max      float64  `yaml:"max"`
	Entities []string `yaml:"entities"`
}

type Weather struct {
	SourceName string    `yaml:"source_name"`
	URL        string    `yaml:"url"`
	Capitals   []Capital `yaml:"capitals"`
}

type Capital struct {
	City string  `yaml:"city"`
	UF   string  `yaml:"uf"`
	Lat  float64 `yaml:"lat"`
	Lon  float64 `yaml:"lon"`
}

type Schedule struct {
	RetryAfter Duration     `yaml:"retry_after"`
	Exclude    ExcludeRules `yaml:"exclude"`
	Segment    SegmentRules `yaml:"segment"`
	Check      CheckRules   `yaml:"check"`
	Blocks     []Block      `yaml:"blocks"`
}

// ExcludeRules: temas fora do brief (por ora, saúde).
type ExcludeRules struct {
	URLParts []string `yaml:"url_parts"`
	Keywords []string `yaml:"keywords"`
}

type SegmentRules struct {
	MinLines       int `yaml:"min_lines"`
	MaxLines       int `yaml:"max_lines"`
	MinSeconds     int `yaml:"min_seconds"`
	MaxSeconds     int `yaml:"max_seconds"`
	WordsPerMinute int `yaml:"words_per_minute"`
}

type CheckRules struct {
	MaxFactDropRatio float64  `yaml:"max_fact_drop_ratio"`
	MinLinesAfter    int      `yaml:"min_lines_after_check"`
	NameExceptions   []string `yaml:"name_exceptions"`
	JudgeConcurrency int      `yaml:"judge_concurrency"`
}

type Block struct {
	Name             string   `yaml:"name"`
	Every            Duration `yaml:"every"`
	Lead             string   `yaml:"lead"`
	Support          string   `yaml:"support"`
	MaxArticles      int      `yaml:"max_articles"`
	ArticleKeywords  []string `yaml:"article_keywords"`
	IncludeMarket    bool     `yaml:"include_market"`
	WeatherFixed     []string `yaml:"weather_fixed"`
	WeatherRotating  int      `yaml:"weather_rotating"`
	Instructions     string   `yaml:"instructions"`
	ClosingLine      string   `yaml:"closing_line"`
	ForbiddenPhrases []string `yaml:"forbidden_phrases"`
}

func (s Schedule) Block(name string) (Block, bool) {
	for _, b := range s.Blocks {
		if b.Name == name {
			return b, true
		}
	}
	return Block{}, false
}

type Blackout struct {
	Windows []Window `yaml:"windows"`
}

type Window struct {
	Name  string    `yaml:"name"`
	Start time.Time `yaml:"start"`
	End   time.Time `yaml:"end"`
}

// Active devolve a janela ativa em t, se houver.
func (b Blackout) Active(t time.Time) (Window, bool) {
	for _, w := range b.Windows {
		if !t.Before(w.Start) && t.Before(w.End) {
			return w, true
		}
	}
	return Window{}, false
}

type Persona struct {
	ID           string   `yaml:"id" json:"id"`
	Name         string   `yaml:"name" json:"name"`
	Role         string   `yaml:"role" json:"role"`
	Style        string   `yaml:"style" json:"style"`
	Catchphrases []string `yaml:"catchphrases" json:"catchphrases"`
	Blocks       []string `yaml:"blocks" json:"blocks"`
	Never        []string `yaml:"never" json:"never"`
	Relationship string   `yaml:"relationship" json:"relationship"`
	// Voz (Sprint 2). Trocar a voz é só editar o YAML.
	Voice          tts.Voice            `yaml:"voice" json:"-"`
	FallbackVoices map[string]tts.Voice `yaml:"fallback_voices" json:"-"`
}

// Duration aceita "5m", "1h" no YAML.
type Duration struct{ time.Duration }

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

func readYAML(path string, out any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(b, out); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func LoadFeeds(dir string) (Feeds, error) {
	var f Feeds
	return f, readYAML(filepath.Join(dir, "feeds.yaml"), &f)
}

func LoadSchedule(dir string) (Schedule, error) {
	var s Schedule
	if err := readYAML(filepath.Join(dir, "schedule.yaml"), &s); err != nil {
		return s, err
	}
	if s.Segment.WordsPerMinute == 0 {
		s.Segment.WordsPerMinute = 150
	}
	if s.Check.JudgeConcurrency == 0 {
		s.Check.JudgeConcurrency = 4
	}
	return s, nil
}

func LoadBlackout(dir string) (Blackout, error) {
	var b Blackout
	if err := readYAML(filepath.Join(dir, "blackout.yaml"), &b); err != nil {
		return b, err
	}
	for _, w := range b.Windows {
		if !w.End.After(w.Start) {
			return b, fmt.Errorf("blackout %q: fim antes do início", w.Name)
		}
	}
	return b, nil
}

// LoadPersonas lê config/personas/*.yaml; o id é o nome do arquivo.
func LoadPersonas(dir string) (map[string]Persona, error) {
	files, err := filepath.Glob(filepath.Join(dir, "personas", "*.yaml"))
	if err != nil {
		return nil, err
	}
	out := map[string]Persona{}
	for _, f := range files {
		var p Persona
		if err := readYAML(f, &p); err != nil {
			return nil, err
		}
		p.ID = strings.TrimSuffix(filepath.Base(f), ".yaml")
		out[p.ID] = p
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("nenhuma persona em %s/personas", dir)
	}
	return out, nil
}

// LoadAllowlist achata as categorias de config/allowlist.yaml.
func LoadAllowlist(dir string) ([]string, error) {
	var cats map[string][]string
	if err := readYAML(filepath.Join(dir, "allowlist.yaml"), &cats); err != nil {
		return nil, err
	}
	var out []string
	for _, terms := range cats {
		out = append(out, terms...)
	}
	return out, nil
}

// LoadFirstNames lê config/first_names.txt (uma por linha; # comenta).
func LoadFirstNames(dir string) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(dir, "first_names.txt"))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			out = append(out, l)
		}
	}
	return out, nil
}

type GlossaryTerm struct {
	Term       string   `yaml:"term"`
	Aliases    []string `yaml:"aliases"`
	Definition string   `yaml:"definition"`
	SourceName string   `yaml:"source_name"`
	URL        string   `yaml:"url"`
	CheckURL   string   `yaml:"check_url"`
	Check      string   `yaml:"check"`
	Entities   []struct {
		Name string `yaml:"name"`
		Type string `yaml:"type"`
	} `yaml:"entities"`
}

func LoadGlossary(dir string) ([]GlossaryTerm, error) {
	var g struct {
		Terms []GlossaryTerm `yaml:"terms"`
	}
	return g.Terms, readYAML(filepath.Join(dir, "glossary.yaml"), &g)
}
