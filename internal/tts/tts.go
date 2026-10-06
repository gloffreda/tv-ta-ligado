// Package tts sintetiza voz. Provedores locais (chatterbox, kokoro, piper) têm
// prioridade; nuvem (azure, google, polly, elevenlabs) só entra com chave.
// Os visemas não dependem do provedor: vêm sempre do áudio final (lipsync).
package tts

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

// Voice é a voz de um avatar num provedor (config/personas/*.yaml → voice).
type Voice struct {
	Provider string             `yaml:"provider" json:"provider"`
	Name     string             `yaml:"name" json:"name"`
	Rate     float64            `yaml:"rate" json:"rate"`   // 1.0 = normal
	Pitch    float64            `yaml:"pitch" json:"pitch"` // semitons
	Style    string             `yaml:"style" json:"style"`
	Params   map[string]float64 `yaml:"params" json:"params,omitempty"` // específicos (exaggeration, cfg_weight...)
}

// Key identifica a voz para o cache de áudio.
func (v Voice) Key() string {
	return fmt.Sprintf("%s|%s|%.3f|%.2f|%s|%v", v.Provider, v.Name, v.Rate, v.Pitch, v.Style, v.Params)
}

// Audio é o que o provedor devolve. Format: wav | mp3 | pcm16 (cru, mono).
type Audio struct {
	Data       []byte
	Format     string
	SampleRate int
}

// Provider sintetiza texto numa voz e devolve o áudio e a duração (0 se
// desconhecida; o pipeline mede depois de converter).
type Provider interface {
	Name() string
	Synthesize(ctx context.Context, text string, v Voice) (Audio, time.Duration, error)
	PricePerMChar() float64 // US$ por milhão de caracteres (0 nos locais)
}

var ErrUnavailable = errors.New("provedor indisponível")

// WAVFromPCM16 embrulha PCM 16 bits mono num cabeçalho WAV.
func WAVFromPCM16(pcm []byte, sr int) []byte {
	h := make([]byte, 44)
	copy(h[0:], "RIFF")
	binary.LittleEndian.PutUint32(h[4:], uint32(36+len(pcm)))
	copy(h[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 1)
	binary.LittleEndian.PutUint16(h[22:], 1)
	binary.LittleEndian.PutUint32(h[24:], uint32(sr))
	binary.LittleEndian.PutUint32(h[28:], uint32(sr*2))
	binary.LittleEndian.PutUint16(h[32:], 2)
	binary.LittleEndian.PutUint16(h[34:], 16)
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], uint32(len(pcm)))
	return append(h, pcm...)
}

// WAVInfo lê taxa e duração de um WAV PCM16 (procura o chunk "data").
func WAVInfo(wav []byte) (sr int, dur time.Duration, pcm []byte, err error) {
	if len(wav) < 44 || string(wav[0:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
		return 0, 0, nil, errors.New("não é WAV")
	}
	var channels, bits int
	for i := 12; i+8 <= len(wav); {
		id := string(wav[i : i+4])
		size := int(binary.LittleEndian.Uint32(wav[i+4:]))
		body := i + 8
		switch id {
		case "fmt ":
			channels = int(binary.LittleEndian.Uint16(wav[body+2:]))
			sr = int(binary.LittleEndian.Uint32(wav[body+4:]))
			bits = int(binary.LittleEndian.Uint16(wav[body+14:]))
		case "data":
			end := min(body+size, len(wav))
			pcm = wav[body:end]
			if sr == 0 || channels == 0 || bits == 0 {
				return 0, 0, nil, errors.New("WAV sem fmt")
			}
			frames := len(pcm) / (channels * bits / 8)
			return sr, time.Duration(frames) * time.Second / time.Duration(sr), pcm, nil
		}
		i = body + size + size%2
	}
	return 0, 0, nil, errors.New("WAV sem data")
}
