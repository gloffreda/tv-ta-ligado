package tts

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func wav1s() []byte { return WAVFromPCM16(make([]byte, 48000), 24000) }

func TestAzureRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if r.Header.Get("Ocp-Apim-Subscription-Key") != "k" || !strings.Contains(string(b), `name="pt-BR-AntonioNeural"`) ||
			!strings.Contains(string(b), `rate="-8%"`) || !strings.Contains(string(b), `style="newscast"`) {
			http.Error(w, string(b), 400)
			return
		}
		w.Write(wav1s())
	}))
	defer srv.Close()
	a := &Azure{Key: "k", Region: "brazilsouth", HTTP: srv.Client(), Endpoint: srv.URL}
	_, d, err := a.Synthesize(context.Background(), "Boa noite & bem-vindos", Voice{Name: "pt-BR-AntonioNeural", Rate: 0.92, Style: "newscast"})
	if err != nil || d != time.Second {
		t.Fatalf("%v %v", d, err)
	}
}

func TestGoogleRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		if r.URL.Query().Get("key") != "g" || req["voice"]["languageCode"] != "pt-BR" {
			http.Error(w, "bad", 400)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"audioContent": base64.StdEncoding.EncodeToString(wav1s())})
	}))
	defer srv.Close()
	g := &Google{APIKey: "g", HTTP: srv.Client(), URL: srv.URL}
	if _, d, err := g.Synthesize(context.Background(), "Olá", Voice{Name: "pt-BR-Neural2-B"}); err != nil || d != time.Second {
		t.Fatalf("%v %v", d, err)
	}
}

func TestPollySigV4(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential=AK/20261006/sa-east-1/polly/aws4_request") || r.Header.Get("X-Amz-Date") != "20261006T120000Z" {
			http.Error(w, auth, 403)
			return
		}
		w.Write(make([]byte, 32000)) // 1 s de PCM 16 kHz
	}))
	defer srv.Close()
	p := &Polly{AccessKey: "AK", SecretKey: "SK", Region: "sa-east-1", HTTP: srv.Client(), Endpoint: srv.URL,
		Now: func() time.Time { return time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC) }}
	if _, d, err := p.Synthesize(context.Background(), "Olá", Voice{Name: "Thiago"}); err != nil || d != time.Second {
		t.Fatalf("%v %v", d, err)
	}
}

func TestElevenLabsRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("xi-api-key") != "e" || !strings.HasSuffix(r.URL.Path, "/v1/text-to-speech/voz123") || r.URL.Query().Get("output_format") != "pcm_24000" {
			http.Error(w, "bad", 400)
			return
		}
		w.Write(make([]byte, 48000))
	}))
	defer srv.Close()
	e := &ElevenLabs{Key: "e", HTTP: srv.Client(), URL: srv.URL}
	if _, d, err := e.Synthesize(context.Background(), "Olá", Voice{Name: "voz123"}); err != nil || d != time.Second {
		t.Fatalf("%v %v", d, err)
	}
}

func TestLocalProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		if req["voice"] != "pm_santa" || req["speed"].(float64) != 0.92 || req["exaggeration"].(float64) != 0.7 {
			http.Error(w, "bad", 400)
			return
		}
		w.Write(wav1s())
	}))
	defer srv.Close()
	l := &Local{ProviderName: "kokoro", URL: srv.URL, HTTP: srv.Client()}
	if _, d, err := l.Synthesize(context.Background(), "Olá", Voice{Name: "pm_santa", Rate: 0.92, Params: map[string]float64{"exaggeration": 0.7}}); err != nil || d != time.Second {
		t.Fatalf("%v %v", d, err)
	}
}
