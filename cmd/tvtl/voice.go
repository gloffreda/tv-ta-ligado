package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gloffreda/tv-ta-ligado/internal/audio"
	"github.com/gloffreda/tv-ta-ligado/internal/lipsync"
	"github.com/gloffreda/tv-ta-ligado/internal/speech"
	"github.com/gloffreda/tv-ta-ligado/internal/tts"
	"github.com/gloffreda/tv-ta-ligado/internal/voice"
)

func envOr(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func envPrice(k string, def float64) float64 {
	if v, err := strconv.ParseFloat(os.Getenv(k), 64); err == nil {
		return v
	}
	return def
}

// ttsProviders: locais aprovados no benchmark (config/tts.yaml) e nuvens com chave.
func (a *app) ttsProviders(ctx context.Context) (map[string]tts.Provider, voice.TTSConfig, []string) {
	cfg, err := voice.LoadTTSConfig(filepath.Join(a.env.ConfigDir, "tts.yaml"))
	if err != nil {
		slog.Warn("tts.yaml", "erro", err)
	}
	hc := &http.Client{Timeout: 5 * time.Minute}
	ps := map[string]tts.Provider{}
	var notes []string
	for name, info := range cfg.Providers {
		if !info.Approved {
			notes = append(notes, fmt.Sprintf("%s: fora (%s)", name, info.Reason))
			continue
		}
		ps[name] = &tts.Local{ProviderName: name, URL: localURL(name), HTTP: hc}
	}
	if k, r := os.Getenv("AZURE_SPEECH_KEY"), os.Getenv("AZURE_SPEECH_REGION"); k != "" && r != "" {
		ps["azure"] = &tts.Azure{Key: k, Region: r, Price: envPrice("TTS_PRICE_AZURE_PER_MCHAR", 15), HTTP: hc}
	} else {
		notes = append(notes, "azure: sem chave")
	}
	if k := os.Getenv("GOOGLE_TTS_KEY"); k != "" {
		ps["google"] = &tts.Google{APIKey: k, Price: envPrice("TTS_PRICE_GOOGLE_PER_MCHAR", 16), HTTP: hc}
	} else if sa := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); sa != "" {
		if tok, err := tts.GoogleServiceAccountToken(ctx, sa); err == nil {
			ps["google"] = &tts.Google{Token: tok, Price: envPrice("TTS_PRICE_GOOGLE_PER_MCHAR", 16), HTTP: hc}
		} else {
			notes = append(notes, "google: credencial inválida: "+err.Error())
		}
	} else {
		notes = append(notes, "google: sem chave")
	}
	if ak, sk, r := os.Getenv("AWS_ACCESS_KEY_ID"), os.Getenv("AWS_SECRET_ACCESS_KEY"), os.Getenv("AWS_REGION"); ak != "" && sk != "" && r != "" {
		ps["polly"] = &tts.Polly{AccessKey: ak, SecretKey: sk, Region: r, Price: envPrice("TTS_PRICE_POLLY_PER_MCHAR", 16), HTTP: hc}
	} else {
		notes = append(notes, "polly: sem chave")
	}
	if k := os.Getenv("ELEVENLABS_API_KEY"); k != "" {
		ps["elevenlabs"] = &tts.ElevenLabs{Key: k, Price: envPrice("TTS_PRICE_ELEVENLABS_PER_MCHAR", 40), HTTP: hc}
	} else {
		notes = append(notes, "elevenlabs: sem chave")
	}
	return ps, cfg, notes
}

func (a *app) voicer(ctx context.Context) (*voice.Voicer, error) {
	ps, _, notes := a.ttsProviders(ctx)
	names := make([]string, 0, len(ps))
	for n := range ps {
		names = append(names, n)
	}
	slog.Info("TTS", "provedores_ativos", names, "producao", envOr("TTS_PROVIDER", "kokoro"), "fallback", envOr("TTS_FALLBACK", "kokoro,piper"), "fora", notes)
	dict, err := speech.LoadDict(filepath.Join(a.env.ConfigDir, "pronunciation.yaml"))
	if err != nil {
		return nil, err
	}
	media := envOr("TVTL_MEDIA_DIR", "/media")
	if err := os.MkdirAll(media, 0o755); err != nil {
		return nil, err
	}
	router := &tts.Router{Providers: ps, Fallback: strings.Split(envOr("TTS_FALLBACK", "kokoro,piper"), ","),
		OnEvent: func(kind string, d map[string]any) { _ = a.store.Event(context.WithoutCancel(ctx), kind, d) }}
	return &voice.Voicer{Store: a.store, Router: router, Proc: audio.FFmpeg{}, MediaDir: media, Dict: dict, Ledger: a.store, Budget: a.metered, Gate: a.gate, Now: a.now,
		Lip: &lipsync.Client{URL: envOr("LIPSYNC_URL", "http://lipsync:8080"), HTTP: &http.Client{Timeout: 3 * time.Minute}}}, nil
}

func (a *app) audition(ctx context.Context, out string) error {
	ps, cfg, notes := a.ttsProviders(ctx)
	dict, err := speech.LoadDict(filepath.Join(a.env.ConfigDir, "pronunciation.yaml"))
	if err != nil {
		return err
	}
	key, err := voice.Audition(ctx, cfg, ps, audio.FFmpeg{}, dict, out, time.Now().UnixNano())
	if err != nil {
		return err
	}
	fmt.Println(key)
	slog.Info("audição", "pasta", out, "fora", notes)
	return nil
}
