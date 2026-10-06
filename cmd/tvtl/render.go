package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gloffreda/tv-ta-ligado/internal/audio"
	"github.com/gloffreda/tv-ta-ligado/internal/timeline"
	"github.com/gloffreda/tv-ta-ligado/internal/tts"
)

// parseFrom: "now", "-15m"/"+5m" (relativo a agora) ou RFC 3339.
func parseFrom(v string, now time.Time) (time.Time, error) {
	v = strings.TrimSpace(v)
	switch {
	case v == "" || v == "now":
		return now, nil
	case strings.HasPrefix(v, "-") || strings.HasPrefix(v, "+"):
		d, err := time.ParseDuration(v)
		return now.Add(d), err
	}
	return time.Parse(time.RFC3339, v)
}

// render: concatena a linha do tempo [from, from+minutes) num MP3, com as
// pausas, e gera legendas.srt alinhadas. Se a janela vai além do que já está
// agendado, agenda até o fim dela antes (só acrescenta no fim).
func (a *app) render(ctx context.Context, fromS string, minutes int, out string) error {
	from, err := parseFrom(fromS, a.now())
	if err != nil {
		return fmt.Errorf("--from: %w", err)
	}
	from = from.UTC().Truncate(time.Second)
	d := time.Duration(minutes) * time.Minute
	to := from.Add(d)
	if end, ok, _ := a.store.TimelineEnd(ctx); !ok || end.Before(to) {
		v, err := a.voicer(ctx)
		if err != nil {
			return err
		}
		sc, err := a.scheduler(v)
		if err != nil {
			return err
		}
		if _, err := sc.Fill(ctx, to); err != nil {
			return err
		}
	}
	items, err := a.store.TimelineRange(ctx, from, to)
	if err != nil {
		return err
	}
	proc := audio.FFmpeg{}
	pcm, cues, err := timeline.Render(ctx, items, from, d, envOr("TVTL_MEDIA_DIR", "/media"), proc)
	if err != nil {
		return err
	}
	mp3, err := proc.Encode(ctx, tts.WAVFromPCM16(audio.PCMToBytes(pcm), audio.SampleRate), "mp3")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	name := fmt.Sprintf("tvtl-%s", from.In(a.env.Location).Format("20060102-1504"))
	mp3Path := filepath.Join(out, name+".mp3")
	if err := os.WriteFile(mp3Path, mp3, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "legendas.srt"), []byte(timeline.SRT(cues)), 0o644); err != nil {
		return err
	}
	kinds := map[string]int{}
	for _, it := range items {
		kinds[it.Kind]++
	}
	slog.Info("render", "mp3", mp3Path, "srt", filepath.Join(out, "legendas.srt"), "de", from.In(a.env.Location).Format("15:04:05"),
		"minutos", minutes, "itens", kinds, "falas", len(cues))
	return nil
}
