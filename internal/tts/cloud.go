package tts

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"strings"
	"time"
)

// ---- Azure ----

type Azure struct {
	Key, Region string
	Price       float64
	HTTP        *http.Client
	Endpoint    string // vazio = https://{region}.tts.speech.microsoft.com
}

func (a *Azure) Name() string           { return "azure" }
func (a *Azure) PricePerMChar() float64 { return a.Price }

// AzureSSML monta o SSML com prosódia (rate/pitch) e estilo opcional.
func AzureSSML(text string, v Voice) string {
	inner := fmt.Sprintf(`<prosody rate="%+.0f%%" pitch="%+.0fst">%s</prosody>`, (rateOr1(v.Rate)-1)*100, v.Pitch, html.EscapeString(text))
	if v.Style != "" {
		inner = fmt.Sprintf(`<mstts:express-as style="%s">%s</mstts:express-as>`, html.EscapeString(v.Style), inner)
	}
	return fmt.Sprintf(`<speak version="1.0" xmlns="http://www.w3.org/2001/10/synthesis" xmlns:mstts="https://www.w3.org/2001/mstts" xml:lang="pt-BR"><voice name="%s">%s</voice></speak>`, html.EscapeString(v.Name), inner)
}

func (a *Azure) Synthesize(ctx context.Context, text string, v Voice) (Audio, time.Duration, error) {
	ep := a.Endpoint
	if ep == "" {
		ep = fmt.Sprintf("https://%s.tts.speech.microsoft.com", a.Region)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, ep+"/cognitiveservices/v1", strings.NewReader(AzureSSML(text, v)))
	req.Header.Set("Ocp-Apim-Subscription-Key", a.Key)
	req.Header.Set("Content-Type", "application/ssml+xml")
	req.Header.Set("X-Microsoft-OutputFormat", "riff-24khz-16bit-mono-pcm")
	req.Header.Set("User-Agent", "tvtl")
	data, err := do(a.HTTP, req, "azure")
	if err != nil {
		return Audio{}, 0, err
	}
	sr, dur, _, err := WAVInfo(data)
	return Audio{Data: data, Format: "wav", SampleRate: sr}, dur, err
}

// ---- Google ----

type Google struct {
	APIKey string
	Token  func(ctx context.Context) (string, error) // service account (OAuth), se não houver API key
	Price  float64
	HTTP   *http.Client
	URL    string
}

func (g *Google) Name() string           { return "google" }
func (g *Google) PricePerMChar() float64 { return g.Price }

func (g *Google) Synthesize(ctx context.Context, text string, v Voice) (Audio, time.Duration, error) {
	body, _ := json.Marshal(map[string]any{
		"input":       map[string]string{"text": text},
		"voice":       map[string]string{"languageCode": "pt-BR", "name": v.Name},
		"audioConfig": map[string]any{"audioEncoding": "LINEAR16", "sampleRateHertz": 24000, "speakingRate": rateOr1(v.Rate), "pitch": v.Pitch},
	})
	u := g.URL
	if u == "" {
		u = "https://texttospeech.googleapis.com/v1/text:synthesize"
	}
	if g.APIKey != "" {
		u += "?key=" + g.APIKey
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if g.APIKey == "" && g.Token != nil {
		tok, err := g.Token(ctx)
		if err != nil {
			return Audio{}, 0, err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	data, err := do(g.HTTP, req, "google")
	if err != nil {
		return Audio{}, 0, err
	}
	var out struct {
		AudioContent string `json:"audioContent"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return Audio{}, 0, err
	}
	wav, err := base64.StdEncoding.DecodeString(out.AudioContent)
	if err != nil {
		return Audio{}, 0, err
	}
	sr, dur, _, err := WAVInfo(wav)
	return Audio{Data: wav, Format: "wav", SampleRate: sr}, dur, err
}

// ---- Amazon Polly (SigV4 escrito à mão, sem SDK) ----

type Polly struct {
	AccessKey, SecretKey, Region string
	Price                        float64
	HTTP                         *http.Client
	Endpoint                     string
	Now                          func() time.Time
}

func (p *Polly) Name() string           { return "polly" }
func (p *Polly) PricePerMChar() float64 { return p.Price }

func (p *Polly) Synthesize(ctx context.Context, text string, v Voice) (Audio, time.Duration, error) {
	body, _ := json.Marshal(map[string]string{"Text": text, "VoiceId": v.Name, "Engine": "neural", "OutputFormat": "pcm", "SampleRate": "16000", "TextType": "text"})
	host := fmt.Sprintf("polly.%s.amazonaws.com", p.Region)
	ep := p.Endpoint
	if ep == "" {
		ep = "https://" + host
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, ep+"/v1/speech", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	now := time.Now().UTC()
	if p.Now != nil {
		now = p.Now().UTC()
	}
	SignV4(req, body, host, p.Region, "polly", p.AccessKey, p.SecretKey, now)
	pcm, err := do(p.HTTP, req, "polly")
	if err != nil {
		return Audio{}, 0, err
	}
	wav := WAVFromPCM16(pcm, 16000)
	_, dur, _, err := WAVInfo(wav)
	return Audio{Data: wav, Format: "wav", SampleRate: 16000}, dur, err
}

func hmacSHA(key []byte, s string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(s))
	return m.Sum(nil)
}

// SignV4 assina a requisição AWS (Signature Version 4).
func SignV4(req *http.Request, body []byte, host, region, service, ak, sk string, now time.Time) {
	amzDate := now.Format("20060102T150405Z")
	day := now.Format("20060102")
	ph := sha256.Sum256(body)
	payloadHash := hex.EncodeToString(ph[:])
	req.Header.Set("Host", host)
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	signed := "content-type;host;x-amz-content-sha256;x-amz-date"
	canon := strings.Join([]string{req.Method, req.URL.EscapedPath(), req.URL.RawQuery,
		"content-type:" + req.Header.Get("Content-Type") + "\nhost:" + host + "\nx-amz-content-sha256:" + payloadHash + "\nx-amz-date:" + amzDate + "\n",
		signed, payloadHash}, "\n")
	ch := sha256.Sum256([]byte(canon))
	scope := day + "/" + region + "/" + service + "/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(ch[:])
	k := hmacSHA(hmacSHA(hmacSHA(hmacSHA([]byte("AWS4"+sk), day), region), service), "aws4_request")
	sig := hex.EncodeToString(hmacSHA(k, toSign))
	req.Header.Set("Authorization", fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s", ak, scope, signed, sig))
}

// ---- ElevenLabs ----

type ElevenLabs struct {
	Key   string
	Model string // padrão eleven_flash_v2_5
	Price float64
	HTTP  *http.Client
	URL   string
}

func (e *ElevenLabs) Name() string           { return "elevenlabs" }
func (e *ElevenLabs) PricePerMChar() float64 { return e.Price }

func (e *ElevenLabs) Synthesize(ctx context.Context, text string, v Voice) (Audio, time.Duration, error) {
	model := e.Model
	if model == "" {
		model = "eleven_flash_v2_5"
	}
	body, _ := json.Marshal(map[string]any{"text": text, "model_id": model, "language_code": "pt",
		"voice_settings": map[string]any{"speed": rateOr1(v.Rate), "stability": 0.5, "similarity_boost": 0.75}})
	base := e.URL
	if base == "" {
		base = "https://api.elevenlabs.io"
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/text-to-speech/"+v.Name+"?output_format=pcm_24000", bytes.NewReader(body))
	req.Header.Set("xi-api-key", e.Key)
	req.Header.Set("Content-Type", "application/json")
	pcm, err := do(e.HTTP, req, "elevenlabs")
	if err != nil {
		return Audio{}, 0, err
	}
	wav := WAVFromPCM16(pcm, 24000)
	_, dur, _, err := WAVInfo(wav)
	return Audio{Data: wav, Format: "wav", SampleRate: 24000}, dur, err
}

func do(c *http.Client, req *http.Request, name string) ([]byte, error) {
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s HTTP %d: %s", name, resp.StatusCode, truncate(data))
	}
	return data, nil
}
