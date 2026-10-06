# TV Tá Ligado: texto checado, voz e linha do tempo (Sprints 1 e 2)

Canal de notícias 24/7 apresentado por dois avatares de IA (Orlando Pimenta e Duda
Faísca). Este sprint entrega só o texto: ingerir fontes, montar a pauta, escrever o
roteiro, checar cada fato e gravar os segmentos aprovados. Ainda não há voz, vídeo
nem frontend.

> **Regra inegociável:** nenhuma fala com fato vai ao ar sem uma fonte que a sustente.
> Na dúvida, a fala é cortada.

## Como rodar

Pré-requisitos: Docker com Compose v2. **Nada é instalado no host**: Go, testes,
migrações e build rodam em containers. Tudo usa o projeto Compose `tvtl`, a rede
`tvtl_net` e volumes `tvtl_*`, sem publicar portas.

```bash
cp .env.example .env        # preencha ANTHROPIC_API_KEY
make up                     # Postgres + build da imagem + migrações (não gera nada)
make test                   # testes offline (LLM mockado) + integração em Postgres efêmero
make run                    # inicia o loop: ingestão a cada 5 min + blocos da grade
make logs                   # acompanha o loop
make show N=5               # últimos 5 segmentos aprovados, com fontes
make render MIN=15          # out/tvtl-*.mp3 + out/legendas.srt: ouvir o canal
make debug-up               # API em http://127.0.0.1:58080/v1/now (só local)
```

Vozes: `audition/index.html` (abra no navegador) traz as candidatas às cegas; o
mapa está em `audition/key.md`. Para escolher, edite `voice:` em
`config/personas/<avatar>.yaml`.

Ao final de cada execução (`ingest`, `write`, `check`, `generate`, `rundown`, `run`) o
`tvtl` grava um relatório em Markdown em `output/` (na raiz do projeto, fora do git):
`output/relatorio-AAAAMMDD-HHMMSS-<comando>.md` (um arquivo por execução; no
`run`, ao encerrar o loop). O relatório traz o que foi ingerido, os fatos novos por
`kind`, a taxa de descarte da validação literal (fatos propostos pelo LLM × aceitos),
os segmentos gerados (status, cortes, reescritas, custo), as falas cortadas e os
motivos, o acumulado por bloco (taxa de corte e custo médio), o gasto do dia e os
avisos (inclusive erros de LLM na extração).

Outros alvos e subcomandos:

| Comando | O que faz |
|---|---|
| `make down` | derruba só o projeto `tvtl` (volumes preservados) |
| `make clean` | apaga containers **e volumes** do projeto (pede confirmação) |
| `make migrate` | aplica migrações |
| `make audition` | regera a audição às cegas das vozes (provedores aprovados) |
| `make bench` | benchmark dos TTS locais (fator de tempo real) |
| `make render FROM=-15m MIN=15` | MP3 + legendas.srt da linha do tempo |
| `make staging-up` / `staging-down` | homologação no futuro (`CLOCK_OFFSET=+2h`), projeto `tvtl-staging` isolado |
| `make audit` | auditoria adversarial: 60 casos contra o juiz **real** (custa ~US$ 0,07) |
| `make ingest` | uma rodada de ingestão (sem LLM: título, resumo e, se CC BY, corpo) |
| `make debug-up` / `make debug-down` | expõe o Postgres em `127.0.0.1:${TVTL_PG_PORT:-55432}` |
| `docker compose -p tvtl run --rm tvtl feeds-check` | valida as URLs de `config/feeds.yaml` |
| `… tvtl glossary` | valida as fontes e carrega `config/glossary.yaml` (também roda na partida do `run`) |
| `… tvtl rundown --block noticias` | mostra a pauta que seria montada |
| `… tvtl write --block economia` | pauta + fatos + roteiro → segmento `draft` |
| `… tvtl check --segment 42` | checa um `draft` e decide `approved`/`rejected` |
| `… tvtl generate --block humor` | `write` + `check` |
| `… tvtl show --last 5 --status rejected` | segmentos rejeitados e os motivos |
| `… tvtl stats` | taxa de corte e custo médio por bloco |

## Como funciona

```
RSS / BCB / Open-Meteo ──► articles (título/resumo), facts (mercado, clima)
glossary.yaml (BCB, INMET) ──► facts kind=glossary (fonte validada, sem validade)
                │
   pauta pelo TÍTULO (MODEL_FAST; saúde excluída) ──► matérias escolhidas
                │
   extração preguiçosa de fatos (MODEL_FAST), só das escolhidas
   + validação literal por código; + termos de glossário citados na pauta
                ▼
   roteiro (MODEL_SMART, JSON estrito validado por schema; 1 nova tentativa)
                │
   checagem por fala:
     1. determinística: números (formato BR, arredondamento), nomes × entities,
        banter sem número/nome, fact_ids existentes e válidos
     2. juiz (MODEL_SMART): entailed / unsupported / real_person_mocked
     reprovou → até 2 reescritas com o motivo e todas as regras; cada uma passa
     antes pelo estágio 1 (sem custo de juiz) → reprovou de novo → cortada
                │
   continuidade (MODEL_FAST): só remove ou encurta banter órfão (verificado por código)
                │
   segmento: >30% das falas fact cortadas ou <6 falas → rejected; senão approved
                │
   memória dos avatares (MODEL_FAST), nunca sobre pessoas reais
```

- Blocos (`config/schedule.yaml`): `noticias` (Orlando conduz, Duda comenta, tempo no
  fim), `economia` (só fatos; termina com "Isso não é recomendação de
  investimento."), `humor` (Duda conduz; nunca sobre pessoas reais).
- Nomes próprios: `config/allowlist.yaml` (instituições, siglas, lugares, meses,
  palavras comuns capitalizadas) e `config/first_names.txt` (prenomes que indicam
  pessoa real). Banter nunca cita pessoa real.
- Glossário com fonte oficial: `config/glossary.yaml`.
- Personas em `config/personas/*.yaml`. A pasta `config/` é montada só-leitura e
  relida a cada geração: mudar o YAML muda o comportamento sem recompilar.
- Bloqueio eleitoral (`config/blackout.yaml`): lista de janelas verificada antes de
  cada geração. Inclui 22/10/2026 00:00 a 27/10/2026 00:00 (−03:00).
- Validade dos fatos: mercado 24 h, clima 12 h, manchete 48 h.
- Cada chamada de LLM vai para `llm_calls` com tokens e custo, atribuída ao segmento.
  O teto diário é `MAX_DAILY_USD`.

As decisões de projeto, a arquitetura detectada no host, as portas e os limites
aplicados estão em [DECISIONS.md](DECISIONS.md).

## Variáveis de ambiente

| Variável | Padrão | Uso |
|---|---|---|
| `ANTHROPIC_API_KEY` | — | obrigatória para gerar; sem ela, só a ingestão roda |
| `MODEL_FAST` | `claude-haiku-4-5-20251001` | pauta, extração de fatos, memória |
| `MODEL_SMART` | `claude-sonnet-5-5` | roteiro, reescrita, juiz de falas fact |
| `JUDGE_BANTER_MODEL` | `MODEL_FAST` | juiz de banter (validado na auditoria) |
| `PRICE_FAST_INPUT_PER_MTOK` / `PRICE_FAST_OUTPUT_PER_MTOK` | `1.00` / `5.00` | US$ por milhão de tokens |
| `PRICE_SMART_INPUT_PER_MTOK` / `PRICE_SMART_OUTPUT_PER_MTOK` | `2.00` / `10.00` | US$ por milhão de tokens |
| `MAX_DAILY_USD` | `8.00` | teto diário (fuso de Brasília) |
| `GENERATE` | `on` | chave geral (`on`/`off`) |
| `REPLAY_WHEN_IDLE` | `off` | `on`: com `VIEWERS=0`, reprisa em vez de gerar |
| `VIEWERS` | `1` | audiência (variável por ora; Sprint 3: contagem real) |
| `REPLAY_WINDOW` | `6h` | janela dos segmentos aprovados que podem ser reprisados |
| `INGEST_INTERVAL` | `5m` | intervalo da ingestão |
| `MEMORY_HALF_LIFE_DAYS` | `7` | meia-vida do peso das memórias |
| `TVTL_TIMEZONE` | `America/Sao_Paulo` | fuso do orçamento e das datas |
| `HTTP_USER_AGENT` | `tvtl/0.1 (…)` | user-agent da ingestão |
| `TTS_PROVIDER` | `kokoro` | provedor de voz de produção (melhor local aprovado no benchmark) |
| `TTS_FALLBACK` | `kokoro,piper` | ordem de fallback (só provedores aprovados e disponíveis) |
| `TTS_KOKORO_URL` etc. | `http://tts-<nome>:8080` | endereço dos TTS locais |
| `AZURE_SPEECH_KEY`, `AZURE_SPEECH_REGION`, `GOOGLE_TTS_KEY` ou `GOOGLE_APPLICATION_CREDENTIALS`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_REGION`, `ELEVENLABS_API_KEY` | — | nuvem: cada uma só entra se a chave existir |
| `TTS_PRICE_<PROVEDOR>_PER_MCHAR` | 15 / 16 / 16 / 40 | US$ por milhão de caracteres (azure/google/polly/elevenlabs) |
| `LIPSYNC_URL` | `http://lipsync:8080` | serviço de visemas (Rhubarb) |
| `TVTL_MEDIA_DIR` | `/media` | áudio das falas (volume `tvtl_media`) |
| `BUFFER_MIN` | `10m` | quanto da linha do tempo fica agendado à frente |
| `CLOCK_OFFSET` | `0s` | desloca o relógio (homologação: `+2h`) |
| `TVTL_API_PORT` | `58080` | porta local da API no perfil `debug` (só 127.0.0.1) |
| `TVTL_KOKORO_CPUS` / `TVTL_KOKORO_MEM` | `2` / `1536m` | limites do TTS Kokoro (também as threads do onnxruntime) |
| `TVTL_CHATTERBOX_CPUS` / `_MEM`, `TVTL_PIPER_CPUS` / `_MEM`, `TVTL_LIPSYNC_*`, `TVTL_API_*` | ver `compose.yaml` | limites dos demais serviços |
| `TVTL_OUTPUT_DIR` | `output` (`/app/output` no container) | pasta dos relatórios |
| `TVTL_UID` / `TVTL_GID` | `1000` | usuário do container `tvtl` (dono dos arquivos em `output/`) |
| `POSTGRES_USER` / `POSTGRES_PASSWORD` / `POSTGRES_DB` | `tvtl` | banco interno |
| `TVTL_PG_PORT` | `55432` | porta local do perfil `debug` (só 127.0.0.1) |
| `TVTL_PG_MEM` / `TVTL_PG_CPUS` | `512m` / `1` | limites do Postgres |
| `TVTL_MEM` / `TVTL_CPUS` | `256m` / `1` | limites do `tvtl` |
| `TVTL_PGTEST_MEM` / `TVTL_PGTEST_CPUS` | `512m` / `1` | limites do Postgres de teste |
| `TVTL_GO_MEM` / `TVTL_GO_CPUS` | `3g` / `2` | limites do container de build/teste |

## Testes

`make test` sobe o `postgres-test` (sem porta no host, dados em `tmpfs`) e roda
`go test -race ./...` no container `golang`. Tudo roda offline com o mock de
`llm.Client` e as respostas gravadas em `testdata/llm/`. Os testes de integração se
recusam a rodar contra qualquer banco que não seja o `postgres-test`.

Cobertura principal:

- `internal/brnum`: 33 casos de extração em formato brasileiro e 14 de arredondamento.
- `internal/check`: aceita fala correta; reprova número ausente, arredondamento
  errado, pessoa fora de `entities`, banter com número, banter com nome real e fato
  vencido; fluxo de reescrita (aprova na 2ª tentativa, corta sem 3ª tentativa);
  segmento rejeitado acima de 30%.
- `internal/pipeline` (integração): ponta a ponta com 16 falas, reescrita, corte,
  custo por segmento e memória; bloqueio eleitoral; teto de orçamento; `GENERATE=off`;
  roteiro inválido duas vezes.
- Fixtures: `testdata/facts.json` (20 fatos fictícios, 1 vencido), artigos fictícios
  e respostas de LLM gravadas.

## Voz e linha do tempo (Sprint 2)

- Voz local, sem custo: **Kokoro-82M** (Apache-2.0), o único provedor local
  aprovado no benchmark (RTF 0,66 com 2 CPUs). Chatterbox ficou lento demais em
  CPU e as vozes pt_BR do Piper não têm licença comercial (`LICENSES.md`). Nuvem
  só com chave.
- Visemas do áudio final com Rhubarb (serviço `lipsync`), independentes do
  provedor.
- `spoken_text` normalizado (reais, %, datas, siglas) com verificação de que os
  números falados são os checados; o texto checado nunca muda.
- Uma linha do tempo única (UTC), sempre com ≥ 10 min agendados, itens imutáveis,
  reprise (6 h, nunca < 60 min) e vinheta. Prova de 30 min: buffer mínimo de
  622 s e 0 buracos.
- Custo projetado (LLM + TTS): **US$ 9,52/dia em 24/7** e **US$ 6,35/dia no
  horário nobre**. TTS custa US$ 0 (local).

## Custo medido

<!-- COST-TABLE -->
Medição real em 05/10/2026, num `tvtl run` de 20 minutos (19:16–19:36, horário de
Brasília), com `MODEL_FAST=claude-haiku-4-5-20251001`, `MODEL_SMART=claude-sonnet-5-5`
e os preços padrão do `.env.example`:

| bloco | falas | cortadas | taxa de corte | reescritas aprovadas | custo do segmento (US$) |
|---|---|---|---|---|---|
| noticias | 14 | 1 | 7,1% | 2 | 0,0625 |
| economia | 15 | 3 | 20,0% | 0 | 0,0520 |
| humor | 14 | 4 | 28,6% | 0 | 0,0589 |
| **média** | | | **18,6%** | | **0,0578** |

- O custo por segmento inclui pauta, roteiro, juiz (uma chamada por fala), reescritas e
  memória. Cerca de 40% vai para o juiz.
- A extração de fatos é cobrada à parte, na ingestão: cerca de US$ 0,0015 por artigo
  (Haiku), ou seja, até ~US$ 0,06 por ciclo de 40 artigos. Nessa execução, 160
  artigos geraram 329 fatos propostos, dos quais 326 foram aceitos (0,9% de descarte
  na validação literal).
- Gasto total do dia de teste, com ingestões e extrações: US$ 0,46.
- A amostra é de 1 segmento por bloco (a grade pede `noticias` a cada 20 min e os
  outros a cada 30), então trate esses números como ordem de grandeza.
