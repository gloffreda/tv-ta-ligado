package timeline

import (
	"context"
	"testing"
	"time"

	"github.com/gloffreda/tv-ta-ligado/internal/config"
	"github.com/gloffreda/tv-ta-ligado/internal/store"
	"github.com/gloffreda/tv-ta-ligado/internal/testfix"
)

var t0 = time.Date(2026, 10, 6, 15, 0, 0, 0, time.UTC)

func cfg() Config {
	return Config{BufferMin: 10 * time.Minute, PauseLines: 350 * time.Millisecond, PauseSpeakers: 500 * time.Millisecond,
		PauseItems: 800 * time.Millisecond, ReplayWindow: 6 * time.Hour, ReplayGap: time.Hour}
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newSched(t *testing.T, st *store.Store, c *clock, bo config.Blackout) *Scheduler {
	ctx := context.Background()
	if err := st.InsertAudioAsset(ctx, store.AudioAsset{Hash: "b0", Path: "/dev/null", DurationMS: 3000, Provider: "fake", Voice: "v", SpokenText: "Tá ligado? Já voltamos."}); err != nil {
		t.Fatal(err)
	}
	return &Scheduler{Store: st, Cfg: cfg(), Now: c.now,
		Blocks: func() []config.Block {
			return []config.Block{{Name: "noticias", Every: config.Duration{Duration: 20 * time.Minute}}, {Name: "economia", Every: config.Duration{Duration: 30 * time.Minute}}}
		},
		Blackout: func() config.Blackout { return bo },
		Bumper: func(context.Context) (BumperAudio, error) {
			return BumperAudio{Hash: "b0", Text: "Tá ligado? Já voltamos.", Spoken: "Tá ligado? Já voltamos.", Speaker: "duda", DurationMS: 3000}, nil
		}}
}

func items(t *testing.T, st *store.Store) []store.TimelineItem {
	it, err := st.TimelineRange(context.Background(), t0.Add(-24*time.Hour), t0.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return it
}

func TestOffsetsAndPauses(t *testing.T) {
	ls, dur := cfg().Lines([]store.TimelineLine{{Speaker: "orlando", DurationMS: 1000}, {Speaker: "orlando", DurationMS: 2000}, {Speaker: "duda", DurationMS: 500}})
	if ls[0].OffsetMS != 0 || ls[1].OffsetMS != 1350 || ls[2].OffsetMS != 3850 || dur != 4350*time.Millisecond+800*time.Millisecond {
		t.Fatalf("%+v %v", ls, dur)
	}
}

func TestBufferAlwaysFilledAndBumperWhenEmpty(t *testing.T) {
	st := testfix.DB(t, "timeline_buffer")
	c := &clock{t0}
	s := newSched(t, st, c, config.Blackout{})
	ctx := context.Background()
	if _, err := s.Fill(ctx, time.Time{}); err != nil {
		t.Fatal(err)
	}
	all := items(t, st)
	for _, it := range all {
		if it.Kind != "bumper" {
			t.Fatalf("sem segmentos, só vinheta: %s", it.Kind)
		}
	}
	end, _, _ := st.TimelineEnd(ctx)
	if end.Sub(c.t) < 10*time.Minute {
		t.Fatalf("buffer %v < 10 min", end.Sub(c.t))
	}
	// Itens contíguos: sem buraco de silêncio não planejado.
	for i := 1; i < len(all); i++ {
		if !all[i].StartsAt.Equal(all[i-1].EndsAt) {
			t.Fatalf("buraco entre %d e %d", all[i-1].ID, all[i].ID)
		}
	}
	// O tempo passa: o buffer volta a ter 10 min a cada Fill.
	for step := 0; step < 20; step++ {
		c.t = c.t.Add(97 * time.Second)
		if step == 5 {
			testfix.VoicedSegment(t, st, "noticias", c.t, 20000, 15000, 18000)
		}
		if _, err := s.Fill(ctx, time.Time{}); err != nil {
			t.Fatal(err)
		}
		end, _, _ := st.TimelineEnd(ctx)
		if end.Sub(c.t) < 10*time.Minute {
			t.Fatalf("passo %d: buffer %v < 10 min", step, end.Sub(c.t))
		}
	}
	if n, _, _ := st.TimelineGaps(ctx, t0.Add(-time.Hour), t0.Add(2*time.Hour)); n != 0 {
		t.Fatalf("%d buracos", n)
	}
	if min, ok := s.MinBuffer(); !ok || min < 10*time.Minute {
		t.Fatalf("buffer mínimo observado %v", min)
	}
}

func TestScheduledItemsAreImmutable(t *testing.T) {
	st := testfix.DB(t, "timeline_immutable")
	c := &clock{t0}
	s := newSched(t, st, c, config.Blackout{})
	ctx := context.Background()
	testfix.VoicedSegment(t, st, "economia", t0, 30000, 30000)
	if _, err := s.Fill(ctx, time.Time{}); err != nil {
		t.Fatal(err)
	}
	before := items(t, st)
	// Um segmento novo pronto não fura a fila: entra só no fim.
	testfix.VoicedSegment(t, st, "noticias", t0, 25000)
	c.t = c.t.Add(3 * time.Minute)
	if _, err := s.Fill(ctx, time.Time{}); err != nil {
		t.Fatal(err)
	}
	after := items(t, st)
	for i, b := range before {
		a := after[i]
		if a.ID != b.ID || !a.StartsAt.Equal(b.StartsAt) || !a.EndsAt.Equal(b.EndsAt) || a.Kind != b.Kind {
			t.Fatalf("item %d mudou: %+v → %+v", b.ID, b, a)
		}
	}
	if last := after[len(after)-1]; last.StartsAt.Before(before[len(before)-1].EndsAt) {
		t.Fatal("novo item só pode entrar no fim")
	}
	// O banco também recusa mudar horário de item agendado.
	if _, err := st.DB.Exec(ctx, `UPDATE timeline SET starts_at = starts_at + interval '1 second' WHERE id=$1`, before[0].ID); err == nil {
		t.Fatal("o gatilho deveria impedir a mudança de horário")
	}
	if _, err := st.DB.Exec(ctx, `UPDATE timeline SET status='aired' WHERE id=$1`, before[0].ID); err != nil {
		t.Fatalf("mudar só o status é permitido: %v", err)
	}
}

func TestReplayNotWithin60Minutes(t *testing.T) {
	st := testfix.DB(t, "timeline_replay")
	c := &clock{t0}
	s := newSched(t, st, c, config.Blackout{})
	ctx := context.Background()
	seg := testfix.VoicedSegment(t, st, "noticias", t0.Add(-time.Hour), 40000, 40000)
	// Agenda 3 horas de grade de uma vez.
	if _, err := s.Fill(ctx, t0.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var starts []time.Time
	kinds := map[string]int{}
	for _, it := range items(t, st) {
		kinds[it.Kind]++
		if it.SegmentID != nil && *it.SegmentID == seg {
			starts = append(starts, it.StartsAt)
		}
	}
	if len(starts) < 3 || kinds["segment"] != 1 || kinds["replay"] < 2 {
		t.Fatalf("esperava estreia + reprises: %v %v", kinds, starts)
	}
	for i := 1; i < len(starts); i++ {
		if starts[i].Sub(starts[i-1]) < time.Hour {
			t.Fatalf("reprise em menos de 60 min: %v → %v", starts[i-1], starts[i])
		}
	}
	// Fora da janela de 6 h (o segmento foi criado 1 h antes de t0), não reprisa mais.
	if _, err := s.Fill(ctx, t0.Add(8*time.Hour)); err != nil {
		t.Fatal(err)
	}
	for _, it := range items(t, st) {
		if it.Kind == "replay" && it.StartsAt.After(t0.Add(5*time.Hour)) {
			t.Fatalf("reprise fora da janela de 6 h: %v", it.StartsAt)
		}
	}
}

func TestBlackoutOnlyReplayAndBumper(t *testing.T) {
	st := testfix.DB(t, "timeline_blackout")
	c := &clock{t0}
	bo := config.Blackout{Windows: []config.Window{{Name: "teste", Start: t0.Add(-time.Hour), End: t0.Add(time.Hour)}}}
	s := newSched(t, st, c, bo)
	ctx := context.Background()
	old := testfix.VoicedSegment(t, st, "economia", t0.Add(-2*time.Hour), 30000)
	// Já foi ao ar antes da janela: pode ser reprisado.
	if _, err := st.InsertTimelineItem(ctx, store.TimelineItem{Kind: "segment", SegmentID: &old, StartsAt: t0.Add(-2 * time.Hour), EndsAt: t0.Add(-2*time.Hour + 31*time.Second)}); err != nil {
		t.Fatal(err)
	}
	testfix.VoicedSegment(t, st, "noticias", t0, 20000) // novo, pronto: não pode estrear no bloqueio
	if _, err := s.Fill(ctx, time.Time{}); err != nil {
		t.Fatal(err)
	}
	for _, it := range items(t, st) {
		if it.StartsAt.Before(t0) {
			continue
		}
		if it.Kind == "segment" {
			t.Fatalf("estreia dentro do bloqueio: %+v", it)
		}
	}
	kinds := map[string]int{}
	for _, it := range items(t, st) {
		if !it.StartsAt.Before(t0) {
			kinds[it.Kind]++
		}
	}
	if kinds["replay"] != 1 || kinds["bumper"] == 0 {
		t.Fatalf("no bloqueio: reprise e vinheta: %v", kinds)
	}
}

func TestBlockPriorityFollowsSchedule(t *testing.T) {
	st := testfix.DB(t, "timeline_priority")
	c := &clock{t0}
	s := newSched(t, st, c, config.Blackout{})
	ctx := context.Background()
	// Notícias foi ao ar há 25 min (every 20 → 1,25), economia há 30 min (every 30 → 1,0).
	n0 := testfix.VoicedSegment(t, st, "noticias", t0.Add(-time.Hour), 1000)
	e0 := testfix.VoicedSegment(t, st, "economia", t0.Add(-time.Hour), 1000)
	st.InsertTimelineItem(ctx, store.TimelineItem{Kind: "segment", SegmentID: &n0, Block: ptr("noticias"), StartsAt: t0.Add(-25 * time.Minute), EndsAt: t0.Add(-25*time.Minute + 2*time.Second)})
	st.InsertTimelineItem(ctx, store.TimelineItem{Kind: "segment", SegmentID: &e0, Block: ptr("economia"), StartsAt: t0.Add(-30 * time.Minute), EndsAt: t0.Add(-30*time.Minute + 2*time.Second)})
	testfix.VoicedSegment(t, st, "economia", t0, 30000)
	nNew := testfix.VoicedSegment(t, st, "noticias", t0, 30000)
	added, err := s.Fill(ctx, t0.Add(time.Minute))
	if err != nil || len(added) == 0 {
		t.Fatal(err)
	}
	if added[0].SegmentID == nil || *added[0].SegmentID != nNew {
		t.Fatalf("notícias está mais atrasada na grade e deveria entrar primeiro: %+v", added[0])
	}
}

func ptr(s string) *string { return &s }
