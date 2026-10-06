package testfix

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/gloffreda/tv-ta-ligado/internal/store"
)

var safeName = regexp.MustCompile(`^[a-z0-9_]+$`)

// DB devolve um Store num banco próprio do pacote (tvtl_test_<nome>), recriado
// do zero e migrado. Pacotes de teste rodam em paralelo; cada um tem o seu.
// Só funciona no postgres-test (TEST_DATABASE_URL); fora dele o teste é pulado.
func DB(t *testing.T, name string) *store.Store {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL não definido (rode `make test`)")
	}
	if !strings.Contains(url, "postgres-test") && os.Getenv("TVTL_ALLOW_ANY_TEST_DB") == "" {
		t.Fatalf("recusando rodar testes fora do postgres-test: %s", url)
	}
	if !safeName.MatchString(name) {
		t.Fatalf("nome de banco inválido: %s", name)
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	db := "tvtl_test_" + name
	_, _ = admin.Exec(ctx, `DROP DATABASE IF EXISTS `+db+` WITH (FORCE)`)
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+db); err != nil {
		t.Fatal(err)
	}
	admin.Close(ctx)
	st, err := store.Open(ctx, regexp.MustCompile(`/tvtl_test(\?|$)`).ReplaceAllString(url, "/"+db+"$1"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return st
}
