package timeline

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/gloffreda/tv-ta-ligado/internal/audio"
	"github.com/gloffreda/tv-ta-ligado/internal/store"
)

// Cue é uma legenda: quem fala, o texto checado e o intervalo no arquivo.
type Cue struct {
	Start, End time.Duration
	Speaker    string
	Text       string
}

// Render monta o áudio da janela [from, from+d) exatamente como a linha do
// tempo manda: cada fala no seu deslocamento, silêncio nas pausas. Devolve
// PCM16 mono 24 kHz e as legendas alinhadas.
func Render(ctx context.Context, items []store.TimelineItem, from time.Time, d time.Duration, mediaDir string, proc audio.Processor) ([]int16, []Cue, error) {
	n := int(d.Seconds() * audio.SampleRate)
	pcm := make([]int16, n)
	cache := map[string][]int16{}
	var cues []Cue
	for _, it := range items {
		for _, l := range it.Lines {
			start := it.StartsAt.Add(time.Duration(l.OffsetMS) * time.Millisecond).Sub(from)
			end := start + time.Duration(l.DurationMS)*time.Millisecond
			if end <= 0 || start >= d {
				continue
			}
			samples, ok := cache[l.AudioHash]
			if !ok {
				var err error
				samples, err = proc.DecodePCM(ctx, filepath.Join(mediaDir, l.AudioHash+".ogg"))
				if err != nil {
					return nil, nil, fmt.Errorf("fala %q: %w", l.Text, err)
				}
				cache[l.AudioHash] = samples
			}
			at := int(start.Seconds() * audio.SampleRate)
			for i, s := range samples {
				if j := at + i; j >= 0 && j < n {
					pcm[j] = s
				}
			}
			cues = append(cues, Cue{Start: max(start, 0), End: min(end, d), Speaker: l.Speaker, Text: l.Text})
		}
	}
	return pcm, cues, nil
}

// SRT formata as legendas ("ORLANDO: texto").
func SRT(cues []Cue) string {
	var b strings.Builder
	ts := func(t time.Duration) string {
		ms := t.Milliseconds()
		return fmt.Sprintf("%02d:%02d:%02d,%03d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000)
	}
	for i, c := range cues {
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s: %s\n\n", i+1, ts(c.Start), ts(c.End), strings.ToUpper(c.Speaker), c.Text)
	}
	return b.String()
}
