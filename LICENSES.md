# Licenças de voz, modelos e ferramentas de áudio

Regra: só entra no ar voz/modelo com **uso comercial claramente permitido**.
Conferido nas fontes oficiais em 06/10/2026.

## Em uso

| Componente | Licença | Uso comercial | Fonte |
|---|---|---|---|
| Kokoro-82M v1.0 (pesos), vozes pt-BR `pf_dora`, `pm_alex`, `pm_santa` | Apache-2.0 | Sim | https://huggingface.co/hexgrad/Kokoro-82M (model card; VOICES.md) |
| kokoro-onnx 0.6.1 (runtime) | MIT | Sim | https://github.com/thewh1teagle/kokoro-onnx |
| espeak-ng (fonemização usada pelo Kokoro, dentro do container) | GPL-3.0 | Sim (uso como ferramenta; o áudio gerado não é obra derivada) | https://github.com/espeak-ng/espeak-ng |
| Rhubarb Lip Sync 1.14.0 (visemas) | MIT (dependências MIT/BSD/Boost) | Sim; "the resulting lip sync data belongs to you" | https://github.com/DanielSWolf/rhubarb-lip-sync/blob/master/LICENSE.md |
| FFmpeg (Alpine, libopus/libmp3lame) | LGPL/GPL | Sim (ferramenta) | https://ffmpeg.org/legal.html |

## Avaliados e fora do ar

| Componente | Licença | Por que está fora |
|---|---|---|
| Piper pt_BR `faber`, `cadu`, `jeff` (medium) | Dataset CC0, mas **ajustados a partir de en_US `lessac`**, cujo dataset (Blizzard 2013) é só para pesquisa e proíbe "development, marketing, commercialisation, sale or licencing of voice synthesis" | Sem licença comercial clara |
| Piper pt_BR `edresson` (low) | Dataset CC BY 4.0, mas ajustado a partir de en_US `ryan`, dataset **CC BY-NC-SA 4.0** (não comercial) | Sem licença comercial clara |
| Chatterbox Multilingual V3 + finetune pt-BR (Resemble AI) | MIT (código e pesos; marca d'água Perth mantida) | Licença ok; **reprovado no benchmark** (RTF 8,66 em 4 CPUs) |
| Amostras de referência da demo do Chatterbox (`pt_br_f2.wav` etc.) | Sem licença declarada | Não usadas: sem direito de uso comprovado |

## Nuvem (só com chave no `.env`; nenhuma ativa hoje)

Preço por milhão de caracteres, confirmado nas páginas/APIs oficiais em 06/10/2026.
As quatro permitem uso comercial do áudio gerado nos termos de serviço pagos.

| Provedor | Tier padrão no tvtl | US$/M caracteres | Outros tiers | Gratuidade | Fonte |
|---|---|---|---|---|---|
| Azure Speech | Neural (S1) | 15 | Neural HD 22 | 0,5 M/mês (F0) | API oficial de preços (prices.azure.com, eastus) e azure.microsoft.com/pricing/details/cognitive-services/speech-services |
| Google Cloud TTS | Neural2 | 16 | Standard/WaveNet 4, Chirp 3 HD 30, Studio 160 | 4 M (Standard/WaveNet) ou 1 M/mês | cloud.google.com/text-to-speech/pricing |
| Amazon Polly | Neural | 16 | Standard 4, Generative 30, Long-Form 100 | 1 M/mês por 12 meses (Neural) | aws.amazon.com/polly/pricing |
| ElevenLabs | Flash v2.5 | 40 (US$ 0,04/1k) | Multilingual v2/v3 80 | — (uso comercial a partir do plano Starter) | elevenlabs.io/pricing/api |

## Referências de voz próprias

`config/voices/` está vazio: nenhuma referência com direito de uso comprovado.
Para usar uma, registre aqui quem gravou, a autorização por escrito e a data.
Nunca clonar voz de pessoa pública ou de terceiro.
