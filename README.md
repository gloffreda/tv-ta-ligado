# TV Tá Ligado — pipeline de texto checado (Sprint 1)

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
```

Outros alvos e subcomandos:

| Comando | O que faz |
|---|---|
| `make down` | derruba só o projeto `tvtl` (volumes preservados) |
| `make clean` | apaga containers **e volumes** do projeto (pede confirmação) |
| `make migrate` | aplica migrações |
| `make ingest` | uma rodada de ingestão |
| `make debug-up` / `make debug-down` | expõe o Postgres em `127.0.0.1:${TVTL_PG_PORT:-55432}` |
| `docker compose -p tvtl run --rm tvtl feeds-check` | valida as URLs de `config/feeds.yaml` |
| `… tvtl rundown --block noticias` | mostra a pauta que seria montada |
| `… tvtl write --block economia` | pauta + fatos + roteiro → segmento `draft` |
| `… tvtl check --segment 42` | checa um `draft` e decide `approved`/`rejected` |
| `… tvtl generate --block humor` | `write` + `check` |
| `… tvtl show --last 5 --status rejected` | segmentos rejeitados e os motivos |
| `… tvtl stats` | taxa de corte e custo médio por bloco |

## Como funciona

```
RSS / BCB / Open-Meteo ──► articles, facts (mercado, clima)
                │
   pauta (MODEL_FAST) ──► artigos escolhidos ──► extração de fatos (MODEL_FAST)
                │                                  + validação por código
                ▼
   roteiro (MODEL_SMART, JSON estrito validado por schema; 1 nova tentativa)
                │
   checagem por fala:
     1. determinística: números (formato BR, arredondamento), nomes × entities,
        banter sem número/nome, fact_ids existentes e válidos
     2. juiz (MODEL_SMART): entailed / unsupported / real_person_mocked
     reprovou → 1 reescrita com o motivo → reprovou de novo → cortada
                │
   segmento: >30% das falas fact cortadas ou <6 falas → rejected; senão approved
                │
   memória dos avatares (MODEL_FAST), nunca sobre pessoas reais
```

- Blocos (`config/schedule.yaml`): `noticias` (Orlando conduz, Duda comenta, tempo no
  fim), `economia` (só fatos; termina com "Isso não é recomendação de
  investimento."), `humor` (Duda conduz; nunca sobre pessoas reais).
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
| `MODEL_SMART` | `claude-sonnet-5-5` | roteiro, reescrita, juiz |
| `PRICE_FAST_INPUT_PER_MTOK` / `PRICE_FAST_OUTPUT_PER_MTOK` | `1.00` / `5.00` | US$ por milhão de tokens |
| `PRICE_SMART_INPUT_PER_MTOK` / `PRICE_SMART_OUTPUT_PER_MTOK` | `2.00` / `10.00` | US$ por milhão de tokens |
| `MAX_DAILY_USD` | `5.00` | teto diário (fuso de Brasília) |
| `GENERATE` | `on` | chave geral (`on`/`off`) |
| `INGEST_INTERVAL` | `5m` | intervalo da ingestão |
| `MEMORY_HALF_LIFE_DAYS` | `7` | meia-vida do peso das memórias |
| `TVTL_TIMEZONE` | `America/Sao_Paulo` | fuso do orçamento e das datas |
| `HTTP_USER_AGENT` | `tvtl/0.1 (…)` | user-agent da ingestão |
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

## Custo medido

<!-- COST-TABLE -->
_Pendente: a medição exige uma rodada de `make run` com a API real._
