// Package testfix carrega as fixtures de testdata/ para os testes.
package testfix

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/gloffreda/tv-ta-ligado/internal/facts"
)

// Root devolve a raiz do repositório.
func Root() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}

func Path(parts ...string) string { return filepath.Join(append([]string{Root()}, parts...)...) }

// Now é o "agora" fixo dos testes: 05/10/2026 12:00 em Brasília.
var Now = time.Date(2026, 10, 5, 12, 0, 0, 0, Loc())

func Loc() *time.Location {
	l, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		panic(err)
	}
	return l
}

// Facts carrega testdata/facts.json (20 fatos fictícios).
func Facts(t testing.TB) []facts.Fact {
	t.Helper()
	b, err := os.ReadFile(Path("testdata", "facts.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fs []facts.Fact
	if err := json.Unmarshal(b, &fs); err != nil {
		t.Fatal(err)
	}
	return fs
}

func FactMap(t testing.TB) map[int64]facts.Fact {
	m := map[int64]facts.Fact{}
	for _, f := range Facts(t) {
		m[f.ID] = f
	}
	return m
}

// LLM lê uma resposta gravada de testdata/llm/.
func LLM(t testing.TB, name string) string {
	t.Helper()
	b, err := os.ReadFile(Path("testdata", "llm", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
