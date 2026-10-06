package tts

import (
	"context"
	"fmt"
	"time"
)

// BenchCorpus: frases de noticiário já normalizadas para fala (sem algarismos).
var BenchCorpus = []string{
	"Boa noite. Começa agora mais um bloco de notícias do tê-vê Tá Ligado, com tudo checado antes de ir ao ar.",
	"O dólar comercial fechou a quatro reais e noventa e nove centavos nesta segunda-feira, segundo o Banco Central.",
	"A meta da taxa sélic, definida pelo cópom, está em treze vírgula setenta e cinco por cento ao ano.",
	"Traduzindo pra quem tá chegando agora: a sélic é a taxa básica de juros da economia. Tá ligado?",
	"A prefeitura inaugurou uma ponte de mil duzentos e cinquenta metros sobre o rio, depois de dois anos de obras.",
	"Um filhote de panda-vermelho nasceu no zoológico da cidade, informou a direção nesta manhã.",
	"Previsão para hoje em São Paulo: máxima de vinte e dois graus, mínima de dezesseis e chance de chuva de quarenta e quatro por cento.",
	"No Rio de Janeiro, o céu fica parcialmente nublado, com máxima de vinte e cinco graus.",
	"Orlando, até a máquina ficou impressionada com essa ponte. Será que ela também quer a sua cadeira?",
	"Isso está checado? Então eu leio. E por hoje é só, pessoal. Fica ligado que a gente já volta.",
	"O festival de inverno recebeu oitenta e cinco mil visitantes, segundo a organização do evento.",
	"A taxa de desemprego caiu para seis vírgula oito por cento no trimestre encerrado em agosto.",
}

type BenchResult struct {
	Provider   string
	Voice      string
	AudioSec   float64
	ProcSec    float64
	RTF        float64 // segundos de processamento por segundo de áudio
	MinPerHour float64 // minutos de áudio que dá para gerar por hora
	Requests   int
	Err        string
}

// Bench sintetiza o corpus em sequência até somar `target` de áudio.
func Bench(ctx context.Context, p Provider, v Voice, target time.Duration) BenchResult {
	r := BenchResult{Provider: p.Name(), Voice: v.Name}
	// Aquecimento (carregar modelo, caches): não conta.
	if _, _, err := p.Synthesize(ctx, "Aquecendo a voz.", v); err != nil {
		r.Err = err.Error()
		return r
	}
	var audio, proc time.Duration
	for i := 0; audio < target; i++ {
		text := BenchCorpus[i%len(BenchCorpus)]
		t0 := time.Now()
		_, dur, err := p.Synthesize(ctx, text, v)
		if err != nil {
			r.Err = err.Error()
			break
		}
		proc += time.Since(t0)
		audio += dur
		r.Requests++
		if ctx.Err() != nil {
			break
		}
	}
	r.AudioSec, r.ProcSec = audio.Seconds(), proc.Seconds()
	if audio > 0 {
		r.RTF = proc.Seconds() / audio.Seconds()
		r.MinPerHour = 60 / r.RTF
	}
	return r
}

func (r BenchResult) String() string {
	return fmt.Sprintf("%s/%s: %.1fs de áudio em %.1fs → RTF %.2f, %.0f min/h", r.Provider, r.Voice, r.AudioSec, r.ProcSec, r.RTF, r.MinPerHour)
}
