// Package lipsync extrai visemas do áudio final com Rhubarb Lip Sync em modo
// fonético (independente de idioma). Roda em container próprio (serviço
// `lipsync`); o tvtl fala com ele por HTTP. Trocar o provedor de voz nunca
// afeta a sincronia labial, porque os visemas vêm sempre do áudio final.
package lipsync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Cue é um visema (formas de boca de Rhubarb: A–H e X) com início e fim em ms.
type Cue struct {
	StartMS int    `json:"start_ms"`
	EndMS   int    `json:"end_ms"`
	Shape   string `json:"shape"`
}

type rhubarbOut struct {
	MouthCues []struct {
		Start float64 `json:"start"`
		End   float64 `json:"end"`
		Value string  `json:"value"`
	} `json:"mouthCues"`
}

// ParseRhubarb converte a saída JSON do Rhubarb.
func ParseRhubarb(b []byte) ([]Cue, error) {
	var o rhubarbOut
	if err := json.Unmarshal(b, &o); err != nil {
		return nil, err
	}
	cues := make([]Cue, len(o.MouthCues))
	for i, c := range o.MouthCues {
		cues[i] = Cue{StartMS: int(c.Start*1000 + 0.5), EndMS: int(c.End*1000 + 0.5), Shape: c.Value}
	}
	return cues, nil
}

// Serve expõe POST /visemes (corpo: WAV) → JSON de Cue. Usado no container lipsync.
func Serve(ctx context.Context, addr, rhubarb string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`{"ok":true}`)) })
	mux.HandleFunc("POST /visemes", func(w http.ResponseWriter, r *http.Request) {
		fail := func(code int, err error) {
			slog.Error("lipsync", "erro", err)
			http.Error(w, err.Error(), code)
		}
		dir, err := os.MkdirTemp("", "lip")
		if err != nil {
			fail(500, err)
			return
		}
		defer os.RemoveAll(dir)
		in := filepath.Join(dir, "in.wav")
		f, _ := os.Create(in)
		if _, err := io.Copy(f, io.LimitReader(r.Body, 64<<20)); err != nil {
			f.Close()
			fail(400, err)
			return
		}
		f.Close()
		out := filepath.Join(dir, "out.json")
		cctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(cctx, rhubarb, "-r", "phonetic", "-f", "json", "--extendedShapes", "GHX", "-q", "-o", out, in)
		if msg, err := cmd.CombinedOutput(); err != nil {
			fail(500, fmt.Errorf("rhubarb: %v: %s", err, msg))
			return
		}
		b, err := os.ReadFile(out)
		if err != nil {
			fail(500, err)
			return
		}
		cues, err := ParseRhubarb(b)
		if err != nil {
			fail(500, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(cues)
	})
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { <-ctx.Done(); srv.Close() }()
	slog.Info("lipsync", "addr", addr, "rhubarb", rhubarb)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Client chama o serviço lipsync.
type Client struct {
	URL  string
	HTTP *http.Client
}

func (c *Client) Visemes(ctx context.Context, wav []byte) ([]Cue, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL+"/visemes", bytes.NewReader(wav))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "audio/wav")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("lipsync HTTP %d: %s", resp.StatusCode, body)
	}
	var cues []Cue
	return cues, json.Unmarshal(body, &cues)
}
