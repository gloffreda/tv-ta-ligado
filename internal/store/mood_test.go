package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/gloffreda/tv-ta-ligado/internal/store"
	"github.com/gloffreda/tv-ta-ligado/internal/testfix"
)

func TestMemoryJobsAndMood(t *testing.T) {
	st := testfix.DB(t, "store_mood")
	ctx := context.Background()
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	if _, err := st.DB.Exec(ctx, `INSERT INTO persona_memory(persona, kind, content, weight, about) VALUES ('duda','running_gag','Brinca com o teleprompter',1,'duda+orlando')`); err != nil {
		t.Fatal(err)
	}
	mems, err := st.CastMemories(ctx, []string{"duda", "orlando"}, 10, 3, 7, now)
	if err != nil || len(mems) != 1 {
		t.Fatalf("memórias do par: %v %v", mems, err)
	}
	for i := 0; i < 3; i++ {
		if err := st.MarkMemoriesUsed(ctx, []int64{mems[0].ID}, now); err != nil {
			t.Fatal(err)
		}
	}
	if left, _ := st.CastMemories(ctx, []string{"duda", "orlando"}, 10, 3, 7, now); len(left) != 0 {
		t.Fatal("piada recorrente: no máximo 3 usos na semana")
	}
	if n, err := st.LogCapped(ctx, 3, now); err != nil || n != 1 {
		t.Fatalf("LogCapped: %d %v", n, err)
	}
	if next, _ := st.CastMemories(ctx, []string{"duda", "orlando"}, 10, 3, 7, now.AddDate(0, 0, 7)); len(next) != 1 {
		t.Fatal("na semana seguinte a piada volta")
	}
	due, err := st.JobDue(ctx, "memory_consolidation", 7*24*time.Hour, now)
	if err != nil || !due {
		t.Fatal("nunca rodou: vencida")
	}
	st.JobDone(ctx, "memory_consolidation", now)
	if due, _ := st.JobDue(ctx, "memory_consolidation", 7*24*time.Hour, now.Add(24*time.Hour)); due {
		t.Fatal("rodou ontem: não vence")
	}
	// humor: sobe e volta devagar para a linha de base
	if err := st.NudgeMood(ctx, "orlando", 4, 0, 0, now); err != nil {
		t.Fatal(err)
	}
	ms, _ := st.Moods(ctx, []string{"orlando"}, now)
	if ms["orlando"].Irritacao != 7 {
		t.Fatalf("irritação %v", ms["orlando"].Irritacao)
	}
	ms, _ = st.Moods(ctx, []string{"orlando"}, now.Add(store.MoodHalfLife))
	if got := ms["orlando"].Irritacao; got < 4.9 || got > 5.1 {
		t.Fatalf("meia-vida: %v", got)
	}
}
