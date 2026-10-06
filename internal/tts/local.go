package tts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Local fala com os containers tts-chatterbox, tts-kokoro e tts-piper (mesma API:
// POST /synthesize {text, voice, speed, ...params} → WAV).
type Local struct {
	ProviderName string
	URL          string
	HTTP         *http.Client
}

func (l *Local) Name() string           { return l.ProviderName }
func (l *Local) PricePerMChar() float64 { return 0 }

func (l *Local) Synthesize(ctx context.Context, text string, v Voice) (Audio, time.Duration, error) {
	req := map[string]any{"text": text, "voice": v.Name, "speed": rateOr1(v.Rate)}
	for k, p := range v.Params {
		req[k] = p
	}
	body, _ := json.Marshal(req)
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, l.URL+"/synthesize", bytes.NewReader(body))
	if err != nil {
		return Audio{}, 0, err
	}
	r.Header.Set("Content-Type", "application/json")
	resp, err := l.HTTP.Do(r)
	if err != nil {
		return Audio{}, 0, fmt.Errorf("%s: %w", l.ProviderName, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return Audio{}, 0, err
	}
	if resp.StatusCode != 200 {
		return Audio{}, 0, fmt.Errorf("%s HTTP %d: %s", l.ProviderName, resp.StatusCode, truncate(data))
	}
	sr, dur, _, err := WAVInfo(data)
	if err != nil {
		return Audio{}, 0, fmt.Errorf("%s: %w", l.ProviderName, err)
	}
	return Audio{Data: data, Format: "wav", SampleRate: sr}, dur, nil
}

// Voices lista as vozes que o container oferece.
func (l *Local) Voices(ctx context.Context) ([]string, error) {
	r, _ := http.NewRequestWithContext(ctx, http.MethodGet, l.URL+"/voices", nil)
	resp, err := l.HTTP.Do(r)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Voices []string `json:"voices"`
	}
	return out.Voices, json.NewDecoder(resp.Body).Decode(&out)
}

func rateOr1(r float64) float64 {
	if r <= 0 {
		return 1
	}
	return r
}

func truncate(b []byte) string {
	if len(b) > 300 {
		b = b[:300]
	}
	return string(b)
}
