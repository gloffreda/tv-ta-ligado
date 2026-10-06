package timeline

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gloffreda/tv-ta-ligado/internal/audio"
	"github.com/gloffreda/tv-ta-ligado/internal/store"
)

func TestRenderPlacesLinesAtOffsets(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "h1.ogg"), audio.Tone(time.Second), 0o644)
	os.WriteFile(filepath.Join(dir, "h2.ogg"), audio.Tone(500*time.Millisecond), 0o644)
	from := time.Date(2026, 10, 6, 15, 0, 0, 0, time.UTC)
	items := []store.TimelineItem{{StartsAt: from.Add(2 * time.Second), EndsAt: from.Add(5 * time.Second), Lines: []store.TimelineLine{
		{Speaker: "orlando", Text: "Boa noite.", AudioHash: "h1", OffsetMS: 0, DurationMS: 1000},
		{Speaker: "duda", Text: "Oi!", AudioHash: "h2", OffsetMS: 1500, DurationMS: 500},
	}}}
	pcm, cues, err := Render(context.Background(), items, from, 10*time.Second, dir, audio.Pure{})
	if err != nil {
		t.Fatal(err)
	}
	sr := audio.SampleRate
	nonzero := func(a, b float64) bool {
		for _, s := range pcm[int(a*float64(sr)):int(b*float64(sr))] {
			if s != 0 {
				return true
			}
		}
		return false
	}
	if nonzero(0, 2) || !nonzero(2.0, 3.0) || nonzero(3.01, 3.49) || !nonzero(3.5, 4.0) || nonzero(4.01, 10) {
		t.Fatal("áudio fora do lugar: falas devem cair nos deslocamentos, com silêncio nas pausas")
	}
	srt := SRT(cues)
	if !strings.Contains(srt, "00:00:02,000 --> 00:00:03,000\nORLANDO: Boa noite.") || !strings.Contains(srt, "00:00:03,500 --> 00:00:04,000\nDUDA: Oi!") {
		t.Fatalf("srt:\n%s", srt)
	}
}
