// Package voice transforma falas aprovadas em áudio: normaliza o texto para
// fala, confere os números, sintetiza (com cache por hash), extrai visemas do
// áudio final e grava em Opus no volume de mídia.
package voice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/gloffreda/tv-ta-ligado/internal/audio"
	"github.com/gloffreda/tv-ta-ligado/internal/config"
	"github.com/gloffreda/tv-ta-ligado/internal/lipsync"
	"github.com/gloffreda/tv-ta-ligado/internal/llm"
	"github.com/gloffreda/tv-ta-ligado/internal/speech"
	"github.com/gloffreda/tv-ta-ligado/internal/store"
	"github.com/gloffreda/tv-ta-ligado/internal/tts"
)

// Visemer extrai visemas de um WAV (serviço lipsync, ou mock nos testes).
type Visemer interface {
	Visemes(ctx context.Context, wav []byte) ([]lipsync.Cue, error)
}

type Voicer struct {
	Store    *store.Store
	Router   *tts.Router
	Lip      Visemer
	Proc     audio.Processor
	MediaDir string
	Dict     speech.Dict
	Ledger   llm.Ledger
	Budget   interface{ CheckBudget(context.Context) error }
}

// Ext é a extensão dos arquivos de mídia (Opus em Ogg).
const Ext = ".ogg"

// Hash identifica o áudio de um texto falado numa voz.
func Hash(v tts.Voice, spoken string) string {
	h := sha256.Sum256([]byte(v.Key() + "\n" + spoken))
	return hex.EncodeToString(h[:])
}

// Spoken devolve o texto para falar. Se a verificação de números falhar, usa o
// texto checado como está (nunca fala um número diferente do checado).
func (v *Voicer) Spoken(ctx context.Context, text string) string {
	spoken := speech.Normalize(text, v.Dict)
	if err := speech.VerifyNumbers(text, spoken); err != nil {
		slog.Warn("normalização divergente; falando o texto checado", "texto", text, "erro", err)
		if v.Ledger != nil {
			_ = v.Ledger.Event(ctx, "speech_mismatch", map[string]string{"text": text, "spoken": spoken, "error": err.Error()})
		}
		return text
	}
	return spoken
}

// Asset devolve o áudio de `spoken` na voz pedida, sintetizando só se não
// houver cache. cached=true quando não houve síntese (custo zero).
func (v *Voicer) Asset(ctx context.Context, primary tts.Voice, fallbacks map[string]tts.Voice, spoken string, segmentID *int64) (store.AudioAsset, bool, error) {
	if a, ok, err := v.cached(ctx, Hash(primary, spoken)); err != nil || ok {
		return a, ok, err
	}
	if v.Budget != nil {
		if err := v.Budget.CheckBudget(ctx); err != nil {
			return store.AudioAsset{}, false, err
		}
	}
	res, err := v.Router.Synthesize(ctx, spoken, primary, fallbacks)
	if err != nil {
		return store.AudioAsset{}, false, err
	}
	// O hash final é o da voz que realmente falou (fallback tem outro arquivo).
	hash := Hash(res.Voice, spoken)
	if a, ok, err := v.cached(ctx, hash); err != nil || ok {
		return a, ok, err
	}
	wav, dur, err := v.Proc.Canonical(ctx, res.Audio, res.Voice.Pitch)
	if err != nil {
		return store.AudioAsset{}, false, err
	}
	cues, err := v.Lip.Visemes(ctx, wav)
	if err != nil {
		return store.AudioAsset{}, false, fmt.Errorf("visemas: %w", err)
	}
	enc, err := v.Proc.Encode(ctx, wav, "opus")
	if err != nil {
		return store.AudioAsset{}, false, err
	}
	path := filepath.Join(v.MediaDir, hash+Ext)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, enc, 0o644); err != nil {
		return store.AudioAsset{}, false, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return store.AudioAsset{}, false, err
	}
	vis, _ := json.Marshal(cues)
	a := store.AudioAsset{Hash: hash, Path: path, DurationMS: int(dur.Milliseconds()), Provider: res.Voice.Provider,
		Voice: res.Voice.Name, SpokenText: spoken, Visemes: vis, CostUSD: res.CostUSD}
	if err := v.Store.InsertAudioAsset(ctx, a); err != nil {
		return a, false, err
	}
	if v.Ledger != nil {
		call := llm.LLMCall{Purpose: "tts", Model: res.Voice.Provider + ":" + res.Voice.Name, InputTokens: len([]rune(spoken)), CostUSD: res.CostUSD, SegmentID: segmentID}
		if err := v.Ledger.RecordLLMCall(ctx, call); err != nil {
			slog.Warn("registrar custo de TTS", "erro", err)
		}
	}
	return a, false, nil
}

func (v *Voicer) cached(ctx context.Context, hash string) (store.AudioAsset, bool, error) {
	a, ok, err := v.Store.AudioAsset(ctx, hash)
	if err != nil || !ok {
		return a, false, err
	}
	if _, err := os.Stat(a.Path); err != nil {
		return a, false, nil // arquivo sumiu do volume: sintetiza de novo
	}
	return a, true, nil
}

// VoiceSegment sintetiza as falas ainda sem áudio de um segmento aprovado.
func (v *Voicer) VoiceSegment(ctx context.Context, segID int64, personas map[string]config.Persona) (int, error) {
	lines, err := v.Store.LinesToVoice(ctx, segID)
	if err != nil {
		return 0, err
	}
	ctx = llm.WithSegment(ctx, segID)
	n := 0
	for _, l := range lines {
		p, ok := personas[l.Speaker]
		if !ok {
			return n, fmt.Errorf("persona %q sem voz", l.Speaker)
		}
		spoken := v.Spoken(ctx, l.Text)
		a, cached, err := v.Asset(ctx, p.Voice, p.FallbackVoices, spoken, &segID)
		if err != nil {
			return n, fmt.Errorf("fala %d: %w", l.Seq, err)
		}
		cost := a.CostUSD
		if cached {
			cost = 0
		}
		if err := v.Store.SetLineAudio(ctx, l.ID, spoken, a, cost); err != nil {
			return n, err
		}
		n++
	}
	if n > 0 {
		_ = v.Store.RefreshSegmentCost(ctx, segID)
	}
	return n, nil
}
