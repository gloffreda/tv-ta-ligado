// Package audio converte, codifica e decodifica áudio. Em produção usa ffmpeg
// (dentro do container tvtl); nos testes, uma implementação em Go puro.
package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"time"

	"github.com/gloffreda/tv-ta-ligado/internal/tts"
)

// SampleRate canônica de todo áudio do canal.
const SampleRate = 24000

type Processor interface {
	// Canonical converte para WAV PCM16 mono 24 kHz, aplicando pitch (semitons).
	Canonical(ctx context.Context, a tts.Audio, pitch float64) ([]byte, time.Duration, error)
	// Encode codifica WAV canônico em "opus" (Ogg) ou "mp3".
	Encode(ctx context.Context, wav []byte, format string) ([]byte, error)
	// DecodePCM lê um arquivo de áudio como PCM16 mono 24 kHz.
	DecodePCM(ctx context.Context, path string) ([]int16, error)
}

// FFmpeg é a implementação de produção.
type FFmpeg struct{ Bin string }

func (f FFmpeg) bin() string {
	if f.Bin != "" {
		return f.Bin
	}
	return "ffmpeg"
}

func (f FFmpeg) run(ctx context.Context, in []byte, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, f.bin(), append([]string{"-hide_banner", "-loglevel", "error"}, args...)...)
	cmd.Stdin = bytes.NewReader(in)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg: %v: %s", err, errb.String())
	}
	return out.Bytes(), nil
}

func (f FFmpeg) Canonical(ctx context.Context, a tts.Audio, pitch float64) ([]byte, time.Duration, error) {
	args := []string{"-i", "pipe:0"}
	if pitch != 0 {
		ratio := math.Pow(2, pitch/12)
		args = append(args, "-af", fmt.Sprintf("asetrate=%d*%.5f,aresample=%d,atempo=%.5f", a.SampleRate, ratio, SampleRate, 1/ratio))
	}
	args = append(args, "-ac", "1", "-ar", fmt.Sprint(SampleRate), "-c:a", "pcm_s16le", "-f", "wav", "pipe:1")
	wav, err := f.run(ctx, a.Data, args...)
	if err != nil {
		return nil, 0, err
	}
	wav = fixWAVSizes(wav)
	_, dur, _, err := tts.WAVInfo(wav)
	return wav, dur, err
}

// fixWAVSizes: ffmpeg escrevendo em pipe não volta para corrigir os tamanhos.
func fixWAVSizes(wav []byte) []byte {
	if len(wav) < 44 {
		return wav
	}
	binary.LittleEndian.PutUint32(wav[4:], uint32(len(wav)-8))
	for i := 12; i+8 <= len(wav); {
		id := string(wav[i : i+4])
		size := int(binary.LittleEndian.Uint32(wav[i+4:]))
		if id == "data" {
			binary.LittleEndian.PutUint32(wav[i+4:], uint32(len(wav)-i-8))
			break
		}
		i += 8 + size + size%2
	}
	return wav
}

func (f FFmpeg) Encode(ctx context.Context, wav []byte, format string) ([]byte, error) {
	switch format {
	case "opus":
		return f.run(ctx, wav, "-i", "pipe:0", "-c:a", "libopus", "-b:a", "32k", "-application", "voip", "-f", "ogg", "pipe:1")
	case "mp3":
		return f.run(ctx, wav, "-i", "pipe:0", "-c:a", "libmp3lame", "-b:a", "96k", "-f", "mp3", "pipe:1")
	}
	return nil, fmt.Errorf("formato %q", format)
}

func (f FFmpeg) DecodePCM(ctx context.Context, path string) ([]int16, error) {
	raw, err := f.run(ctx, nil, "-i", path, "-ac", "1", "-ar", fmt.Sprint(SampleRate), "-f", "s16le", "pipe:1")
	if err != nil {
		return nil, err
	}
	return bytesToPCM(raw), nil
}

func bytesToPCM(b []byte) []int16 {
	out := make([]int16, len(b)/2)
	for i := range out {
		out[i] = int16(binary.LittleEndian.Uint16(b[2*i:]))
	}
	return out
}

// PCMToBytes converte amostras em PCM16 little-endian.
func PCMToBytes(pcm []int16) []byte {
	b := make([]byte, 2*len(pcm))
	for i, s := range pcm {
		binary.LittleEndian.PutUint16(b[2*i:], uint16(s))
	}
	return b
}

// Pure é a implementação dos testes: só aceita WAV mono 24 kHz e "codifica"
// guardando o próprio WAV.
type Pure struct{}

func (Pure) Canonical(_ context.Context, a tts.Audio, _ float64) ([]byte, time.Duration, error) {
	sr, dur, _, err := tts.WAVInfo(a.Data)
	if err != nil {
		return nil, 0, err
	}
	if sr != SampleRate {
		return nil, 0, fmt.Errorf("Pure só aceita %d Hz", SampleRate)
	}
	return a.Data, dur, nil
}

func (Pure) Encode(_ context.Context, wav []byte, _ string) ([]byte, error) { return wav, nil }

func (Pure) DecodePCM(_ context.Context, path string) ([]int16, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	_, _, pcm, err := tts.WAVInfo(b)
	if err != nil {
		return nil, err
	}
	return bytesToPCM(pcm), nil
}

// Tone gera um WAV de teste (senoide) com a duração pedida.
func Tone(d time.Duration) []byte {
	n := int(d.Seconds() * SampleRate)
	pcm := make([]int16, n)
	for i := range pcm {
		pcm[i] = int16(3000 * math.Sin(2*math.Pi*440*float64(i)/SampleRate))
	}
	return tts.WAVFromPCM16(PCMToBytes(pcm), SampleRate)
}
