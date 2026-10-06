// Package deploy testa as garantias de isolamento dos arquivos de compose.
package deploy

import (
	"os"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/gloffreda/tv-ta-ligado/internal/config"
	"github.com/gloffreda/tv-ta-ligado/internal/testfix"
)

type composeFile struct {
	Name     string                    `yaml:"name"`
	Networks map[string]map[string]any `yaml:"networks"`
	Volumes  map[string]map[string]any `yaml:"volumes"`
	Services map[string]struct {
		Environment any      `yaml:"environment"`
		Networks    []string `yaml:"networks"`
		Volumes     []string `yaml:"volumes"`
		Ports       []string `yaml:"ports"`
	} `yaml:"services"`
}

func load(t *testing.T, name string) (composeFile, string) {
	b, err := os.ReadFile(testfix.Path(name))
	if err != nil {
		t.Fatal(err)
	}
	var c composeFile
	if err := yaml.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	return c, string(b)
}

// Staging com CLOCK_OFFSET não toca no banco principal.
func TestStagingIsIsolated(t *testing.T) {
	c, raw := load(t, "compose.staging.yaml")
	if c.Name != "tvtl-staging" {
		t.Fatalf("projeto %q", c.Name)
	}
	for _, forbidden := range []string{"tvtl_pgdata", "tvtl_media:", "tvtl_net\n", "tvtl_net]", "/tvtl?"} {
		if strings.Contains(raw, forbidden) {
			t.Errorf("staging referencia recurso do projeto principal: %q", forbidden)
		}
	}
	if !strings.Contains(raw, "CLOCK_OFFSET: ${STAGING_CLOCK_OFFSET:-+2h}") {
		t.Error("staging deve rodar com CLOCK_OFFSET")
	}
	if !strings.Contains(raw, "@postgres:5432/tvtl_staging") {
		t.Error("DATABASE_URL do staging deve apontar para o banco próprio")
	}
	for name, s := range c.Services {
		if len(s.Ports) > 0 {
			t.Errorf("staging/%s publica porta no host", name)
		}
		for _, n := range s.Networks {
			if n != "tvtl_staging_net" {
				t.Errorf("staging/%s na rede %s", name, n)
			}
		}
		for _, v := range s.Volumes {
			if !strings.HasPrefix(v, "./") && !strings.HasPrefix(v, "tvtl_staging_") {
				t.Errorf("staging/%s monta volume de fora: %s", name, v)
			}
		}
	}
	for v := range c.Volumes {
		if !strings.HasPrefix(v, "tvtl_staging_") {
			t.Errorf("volume %s", v)
		}
	}
}

func TestMainComposeIsolation(t *testing.T) {
	c, raw := load(t, "compose.yaml")
	if c.Name != "tvtl" || strings.Contains(raw, "container_name") || strings.Contains(raw, "network_mode") || strings.Contains(raw, "CLOCK_OFFSET") {
		t.Fatal("compose principal: nome fixo tvtl, sem container_name, sem network_mode, sem CLOCK_OFFSET")
	}
	for name, s := range c.Services {
		for _, p := range s.Ports {
			if !strings.HasPrefix(p, `"127.0.0.1:`) && !strings.HasPrefix(p, "127.0.0.1:") {
				t.Errorf("%s publica fora do 127.0.0.1: %s", name, p)
			}
		}
	}
}

func TestClockOffsetEnv(t *testing.T) {
	t.Setenv("CLOCK_OFFSET", "+2h")
	e, err := config.LoadEnv()
	if err != nil || e.ClockOffset != 2*time.Hour {
		t.Fatalf("%v %v", e.ClockOffset, err)
	}
	t.Setenv("CLOCK_OFFSET", "duas horas")
	if _, err := config.LoadEnv(); err == nil {
		t.Fatal("CLOCK_OFFSET inválido deve falhar")
	}
}
