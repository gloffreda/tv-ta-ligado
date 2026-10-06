// Package timeline mantém a linha do tempo única do canal: sempre pelo menos
// BUFFER_MIN agendados à frente, itens imutáveis, novos só no fim.
package timeline

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/gloffreda/tv-ta-ligado/internal/config"
	"github.com/gloffreda/tv-ta-ligado/internal/store"
)

type Config struct {
	BufferMin     time.Duration // BUFFER_MIN (padrão 10 min)
	PauseLines    time.Duration // pause_between_lines_ms (350)
	PauseSpeakers time.Duration // pause_between_speakers_ms (500)
	PauseItems    time.Duration // pause_between_items_ms (800): respiro no fim de cada item
	ReplayWindow  time.Duration // 6 h
	ReplayGap     time.Duration // nunca repetir o mesmo segmento em menos de 60 min
}

// BumperAudio é a vinheta ("Tá ligado? Já voltamos"), sintetizada uma vez.
type BumperAudio struct {
	Hash, Text, Spoken, Speaker string
	DurationMS                  int
}

type Scheduler struct {
	Store    *store.Store
	Cfg      Config
	Blocks   func() []config.Block  // grade (relida do schedule.yaml)
	Blackout func() config.Blackout // janelas de bloqueio
	Bumper   func(ctx context.Context) (BumperAudio, error)
	Now      func() time.Time
	OnEvent  func(kind string, detail map[string]any)

	mu        sync.Mutex
	minBuffer time.Duration
	filled    bool
}

func (s *Scheduler) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// MinBuffer: o menor buffer observado depois de cada Fill.
func (s *Scheduler) MinBuffer() (time.Duration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.minBuffer, s.filled
}

// Lines calcula os deslocamentos das falas e a duração do item.
func (c Config) Lines(ls []store.TimelineLine) ([]store.TimelineLine, time.Duration) {
	out := make([]store.TimelineLine, len(ls))
	var t time.Duration
	for i, l := range ls {
		if i > 0 {
			if l.Speaker != ls[i-1].Speaker {
				t += c.PauseSpeakers
			} else {
				t += c.PauseLines
			}
		}
		l.OffsetMS = int(t.Milliseconds())
		out[i] = l
		t += time.Duration(l.DurationMS) * time.Millisecond
	}
	return out, t + c.PauseItems
}

// Fill agenda itens no fim da linha do tempo até cobrir `until` (padrão:
// agora + BUFFER_MIN). Nada já agendado muda.
func (s *Scheduler) Fill(ctx context.Context, until time.Time) ([]store.TimelineItem, error) {
	now := s.now()
	if until.IsZero() {
		until = now.Add(s.Cfg.BufferMin)
	}
	if err := s.Store.MarkAired(ctx, now); err != nil {
		return nil, err
	}
	end, ok, err := s.Store.TimelineEnd(ctx)
	if err != nil {
		return nil, err
	}
	start := end
	if !ok || end.Before(now) {
		if ok {
			gap := now.Sub(end)
			slog.Warn("linha do tempo vazia por um intervalo (serviço parado?)", "buraco", gap.Round(time.Second))
			s.event("timeline_gap", map[string]any{"from": end, "to": now, "seconds": gap.Seconds()})
		}
		start = now.Truncate(time.Second).Add(time.Second)
	}
	var added []store.TimelineItem
	for start.Before(until) {
		it, err := s.next(ctx, start)
		if err != nil {
			return added, err
		}
		id, err := s.Store.InsertTimelineItem(ctx, it)
		if err != nil {
			return added, err
		}
		it.ID = id
		added = append(added, it)
		start = it.EndsAt
	}
	end, _, _ = s.Store.TimelineEnd(ctx)
	buf := end.Sub(now)
	s.mu.Lock()
	if !s.filled || buf < s.minBuffer {
		s.minBuffer = buf
	}
	s.filled = true
	s.mu.Unlock()
	if buf < s.Cfg.BufferMin {
		s.event("buffer_low", map[string]any{"buffer_s": buf.Seconds(), "min_s": s.Cfg.BufferMin.Seconds()})
	}
	return added, nil
}

func (s *Scheduler) event(kind string, d map[string]any) {
	if s.OnEvent != nil {
		s.OnEvent(kind, d)
	}
}

// pickBlock: entre os blocos com candidato, o mais atrasado em relação ao seu
// `every` na grade (nunca agendado = mais atrasado de todos).
func (s *Scheduler) pickBlock(at time.Time, last map[string]time.Time, cands []store.ReadySegment) (store.ReadySegment, bool) {
	every := map[string]time.Duration{}
	for _, b := range s.Blocks() {
		every[b.Name] = b.Every.Duration
	}
	best, bestScore, found := store.ReadySegment{}, math.Inf(-1), false
	for _, c := range cands {
		score := math.Inf(1)
		if t, ok := last[c.Block]; ok {
			e := every[c.Block]
			if e <= 0 {
				e = 30 * time.Minute
			}
			score = float64(at.Sub(t)) / float64(e)
		}
		if !found || score > bestScore {
			best, bestScore, found = c, score, true
		}
	}
	return best, found
}

func (s *Scheduler) next(ctx context.Context, at time.Time) (store.TimelineItem, error) {
	last, err := s.Store.LastStartByBlock(ctx)
	if err != nil {
		return store.TimelineItem{}, err
	}
	_, blackout := s.Blackout().Active(at)
	if !blackout {
		fresh, err := s.Store.FreshSegments(ctx)
		if err != nil {
			return store.TimelineItem{}, err
		}
		if seg, ok := s.pickBlock(at, last, fresh); ok {
			return s.segmentItem(ctx, "segment", seg, at)
		}
	}
	replays, err := s.Store.ReplayCandidates(ctx, at, s.Cfg.ReplayWindow, s.Cfg.ReplayGap)
	if err != nil {
		return store.TimelineItem{}, err
	}
	if seg, ok := s.pickBlock(at, last, replays); ok {
		return s.segmentItem(ctx, "replay", seg, at)
	}
	return s.bumperItem(ctx, at)
}

func (s *Scheduler) segmentItem(ctx context.Context, kind string, seg store.ReadySegment, at time.Time) (store.TimelineItem, error) {
	ls, err := s.Store.SegmentAudioLines(ctx, seg.ID)
	if err != nil {
		return store.TimelineItem{}, err
	}
	if len(ls) == 0 {
		return store.TimelineItem{}, fmt.Errorf("segmento %d sem falas com áudio", seg.ID)
	}
	lines, dur := s.Cfg.Lines(ls)
	id, block := seg.ID, seg.Block
	return store.TimelineItem{Kind: kind, SegmentID: &id, Block: &block, StartsAt: at, EndsAt: at.Add(dur), Lines: lines}, nil
}

func (s *Scheduler) bumperItem(ctx context.Context, at time.Time) (store.TimelineItem, error) {
	b, err := s.Bumper(ctx)
	if err != nil {
		return store.TimelineItem{}, fmt.Errorf("vinheta: %w", err)
	}
	lines, dur := s.Cfg.Lines([]store.TimelineLine{{Seq: 1, Speaker: b.Speaker, Type: "banter", Text: b.Text, SpokenText: b.Spoken, AudioHash: b.Hash, DurationMS: b.DurationMS}})
	// A vinheta respira mais: 2 s de silêncio planejado depois dela.
	dur += 2 * time.Second
	return store.TimelineItem{Kind: "bumper", StartsAt: at, EndsAt: at.Add(dur), Lines: lines}, nil
}
