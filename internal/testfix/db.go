package testfix

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

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

// VoicedSegment cria um segmento aprovado do bloco com falas já com áudio
// (durações em ms, falantes alternados) e devolve o id.
func VoicedSegment(t *testing.T, st *store.Store, block string, created time.Time, durs ...int) int64 {
	t.Helper()
	ctx := context.Background()
	rid, err := st.CreateRundown(ctx, block, created)
	if err != nil {
		t.Fatal(err)
	}
	sid, err := st.CreateSegment(ctx, rid, block, created)
	if err != nil {
		t.Fatal(err)
	}
	speakers := []string{"orlando", "duda"}
	for i, d := range durs {
		lid, err := st.InsertLine(ctx, store.Line{SegmentID: sid, Seq: i + 1, Speaker: speakers[i%2], Type: "banter", Text: fmt.Sprintf("fala %d", i+1)})
		if err != nil {
			t.Fatal(err)
		}
		a := store.AudioAsset{Hash: fmt.Sprintf("%064x", sid*1000+int64(i)), Path: "/dev/null", DurationMS: d, Provider: "fake", Voice: "v", SpokenText: "x"}
		if err := st.InsertAudioAsset(ctx, a); err != nil {
			t.Fatal(err)
		}
		if err := st.SetLineAudio(ctx, lid, fmt.Sprintf("fala %d", i+1), a, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.FinishSegment(ctx, sid, "approved", ""); err != nil {
		t.Fatal(err)
	}
	return sid
}
