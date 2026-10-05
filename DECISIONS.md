# Decisões — Sprint 1

Registro das escolhas feitas onde a especificação deixava margem. Regra geral:
a opção mais simples que respeita "na dúvida, a fala é cortada".

## Host e isolamento (levantado em 05/10/2026)

- **Arquitetura:** `x86_64`, Ubuntu, kernel 7.0, 16 CPUs, 28 GB de RAM (swap de 8 GB
  praticamente cheio, ou seja, memória disputada). Docker 29.7.2, Compose v5.4.0.
- **Vizinhos:** 19 containers do novatrak (staging, geo, traefik, mailpit…). Portas em
  uso no host incluem 80, 443, 1935, 3000, 5001–5401, 5432 (127.0.0.1, Postgres do
  novatrak), 6379, 8002, 8025, 8080, 8081, 8087, 8090.
- **Portas do tvtl:** nenhuma publicada por padrão. O perfil `debug` publica o Postgres
  só em `127.0.0.1:${TVTL_PG_PORT:-55432}`; 55432 estava livre em `ss -ltn`, e o
  `make debug-up` confere de novo antes de subir.
- **Proxy de depuração:** o Postgres não publica porta nem em `debug`. O perfil sobe o
  serviço `pg-debug`, que roda `tvtl debug-proxy` (um repasse TCP de 20 linhas em Go).
  Assim nenhuma imagem extra é necessária e o `postgres` nunca muda de configuração.
- **Rede e volumes:** rede bridge `tvtl_net`; volumes `tvtl_pgdata`, `tvtl_gomod` e
  `tvtl_gocache`. O único bind mount é `./config` (só leitura), dentro do projeto.
  O `gotool` monta o próprio diretório do projeto em `/src`.
- **Limites aplicados:** `postgres` 512 MB / 1 CPU, `tvtl` 256 MB / 1 CPU,
  `postgres-test` 512 MB / 1 CPU, `pg-debug` 64 MB / 0,25 CPU, `gotool` 3 GB / 2 CPUs.
  Todos configuráveis por variável (`.env.example`).
- **`gotool` com 3 GB:** com 1,5 GB o compilador era morto por falta de memória ao
  compilar o SDK da Anthropic (`signal: killed`). Também usa `GOFLAGS=-p=2` para
  limitar a paralelização da compilação.
- **Build da imagem:** `docker compose build` usa o BuildKit, que não aceita limite de
  memória por build. A compilação do binário roda uma única vez por mudança de
  código e usa cache de módulos. Fica registrado como risco conhecido.
- **Verificação:** antes e depois do trabalho, os 19 containers vizinhos tinham o
  mesmo ID, o mesmo `StartedAt` e `RestartCount` igual a 0. Comparação via
  `docker inspect` (arquivos `before`/`after` no scratchpad da sessão).
- **Go:** a imagem `golang:1.25`, que já estava no host, não compila as dependências
  atuais (`golang.org/x/text` exige Go 1.26). Usei `golang:1.27`, a oficial mais
  recente. O `go.mod` declara `go 1.26.0`.
- **`make up`** sobe o Postgres, constrói a imagem e aplica migrações. Ele **não** inicia
  a geração (que gasta API). Quem inicia o loop é o `make run`. Assim,
  `make up && make test` funciona sem chave de API.

## Fontes

Cada URL foi validada com `tvtl feeds-check` (download + parse + data do item mais
recente) em 05/10/2026:

| Fonte | Licença | Situação |
|---|---|---|
| Agência Brasil — Últimas | CC BY (guarda corpo) | ok, 10 itens, corpo completo no RSS |
| Agência Brasil — Economia | CC BY (guarda corpo) | ok |
| g1 (geral e Economia) | sem licença | ok |
| CNN Brasil | sem licença | ok |
| Poder360 | sem licença | ok |
| Folha de S.Paulo — Em cima da hora | sem licença | ok (ISO-8859-1, tratado pelo parser) |
| Estadão — Brasil | sem licença | ok |
| BBC News Brasil (internacional em PT) | sem licença | ok |
| RFI Brasil (internacional em PT) | sem licença | ok |
| **DW Brasil** | — | **fora**: o feed responde, mas o item mais recente tem ~3,7 anos (feed abandonado) |
| **UOL Notícias** | — | **fora**: XML inválido (`invalid UTF-8`) |

- Para portais sem licença, o banco guarda só título, resumo (até 500 caracteres),
  URL e data, e `body` fica `NULL`. Só fontes `license: cc-by` com `store_body: true`
  guardam o corpo. Itens com mais de 48 h são ignorados.
- **Deduplicação:** `articles.url UNIQUE` + `articles.title_hash UNIQUE` (sha256 do título
  sem acentos, sem pontuação e em minúsculas). Um `INSERT … ON CONFLICT DO NOTHING`
  cobre os dois casos.
- **Banco Central (SGS):** a resposta da API não traz o nome da série. Por isso cada
  código é confirmado por uma **faixa de plausibilidade** em `feeds.yaml` (dólar
  2–15, Selic 1–30, IPCA −3–5). Valor fora da faixa rejeita o dado e registra a falha
  em `system_events`. Valores observados em 05/10/2026: dólar 4,9859; Selic 13,75;
  IPCA de agosto −0,32. Os três batem com as séries 1, 432 e 433.
- **SGS, armadilhas encontradas:** `ultimos/N` aceita no máximo 20 pontos, e a
  série 432 publica mais de 20 datas *futuras* (a meta vale até a próxima reunião).
  Por isso `ultimos/N` nunca devolve a Selic de hoje. A ingestão consulta por
  intervalo (`dataInicial = hoje−120d`, `dataFinal = hoje`) e escolhe o último ponto
  com data ≤ hoje. Se essa consulta falhar, cai para `ultimos/20`. O link clicável
  da fonte é o intervalo `[dia anterior, dia citado]`, porque a API rejeita
  intervalo de um dia só.
- **Clima:** uma única chamada ao Open-Meteo com as 27 capitais. Cada capital vira um
  fato com máxima, mínima e chance de chuva. `value` = máxima; o checador também
  aceita qualquer número do texto do fato.

## Esquema (acréscimos ao mínimo pedido)

- `articles.title_hash`: dedupe por título.
- `facts.expires_at`: validade explícita. Mercado e clima ganham `agora + TTL` a cada
  ingestão, porque reingerir o mesmo dado confirma que ele ainda é o atual. Manchete
  ganha `publicação + 48 h`. `facts.fingerprint` (único) evita duplicar o mesmo dado.
  `facts.series` (`bcb:432`, `weather:Recife`) escolhe o dado mais recente de cada série.
- `rundown_items.fact_id`: um item da pauta é um artigo **ou** um fato avulso
  (mercado/clima), garantido por `CHECK`.
- `segments.reject_reason`; `lines.fact_ids` (os ids exatamente como vieram do roteiro,
  já que `line_claims` só pode guardar ids que existem); `lines.original_text`
  (texto antes da reescrita); `check_log.attempt`; `llm_calls.segment_id`, que dá o
  custo por segmento.
- `system_events`: avisos operacionais (`budget_exceeded`, `blackout`, `source_failed`,
  `facts_extracted`, `memory_dropped`).
- Migrações: SQL versionado em `migrations/`, embutido no binário e aplicado por um
  migrador mínimo (`schema_migrations`). Todo subcomando garante o esquema atual.

## Fatos

- **Extração sob demanda:** o LLM extrai fatos só dos artigos escolhidos para a pauta,
  uma vez por artigo, e não de todos os ~350 artigos ingeridos a cada 5 minutos.
  Isso corta custo sem mudar a regra.
- A validação por código (`facts.Validate`) exige que cada número do fato apareça
  **literalmente** na fonte (mesmo valor e mesma precisão; "3%" não vale se a fonte
  diz "3,2%"), que `value` exista entre os números da fonte e que cada entidade
  apareça na fonte (sem diferenciar maiúsculas ou acentos). O que não bate é
  descartado e registrado em `system_events`.

## Checagem

- **Números:** o extrator (`internal/brnum`) reconhece formato brasileiro, moedas
  (R$, US$, €, "reais", "dólares"), percentuais ("%", "por cento", "pontos
  percentuais"), escalas ("mil", "milhão", "bi"), datas (02/10/2026, 2026-10-05,
  "5 de outubro de 2026", "agosto de 2026"), horas (14h30) e números por extenso
  (dois a quinhentos, mil, milhão; "um/uma" ficam de fora porque são artigos;
  "milhares", "dezenas" etc. contam como número vago).
- **Tolerância de arredondamento:** o número da fala vale se for o valor do fato
  **arredondado corretamente** (meio para cima) nas casas que a fala usou, e na escala
  que a fala usou. 5,2079 → "5,21", "5,2" e "5" passam; "5,20" reprova. "1,2 bilhão"
  casa com 1.234.000.000. Uma fala com mais precisão que o fato reprova.
- **Sinal:** comparado em valor absoluto ("deflação de 0,32%" ↔ −0,32%). O sentido
  (alta ou queda) fica a cargo do juiz.
- **Inteiros pequenos** também casam com dia, mês e ano das datas do fato, o que
  cobre "segunda-feira (5)" e "em 2026".
- **Nomes:** a heurística pega sequências de palavras capitalizadas, admitindo
  "de/da/do" no meio. Numa sequência que abre a frase, a primeira palavra é
  descartada, como pede a spec. Somam-se as entidades de **todos** os fatos do banco
  que aparecem na fala. Em fala `fact`, cada nome precisa estar contido numa
  entidade dos fatos citados, ou conter uma. Em `banter`, qualquer nome reprova.
  Siglas de até 3 letras (SP, SE, AM) só contam com a mesma caixa, para não
  confundir com "se" ou "am". As exceções (avatares, "TV Tá Ligado", "Brasil" etc.)
  ficam em `schedule.yaml`. A heurística é conservadora: siglas e nomes de índices
  (PIB, IPCA) também exigem estar em `entities`.
- **Banter também vai ao juiz:** a spec manda o juiz checar o que passou no estágio 1.
  Para banter, `entailed=true` significa "não faz afirmação factual concreta", e
  `real_person_mocked` é o que mais importa.
- **Juiz com temperatura 0:** o Sonnet 5.5 rejeita (HTTP 400) qualquer `temperature`
  diferente do padrão. O cliente só envia temperatura a modelos que a aceitam (o
  Haiku 4.5, usado na extração, na pauta e na memória, recebe 0). No juiz, a
  determinação vem do prompt fechado, do JSON validado por schema e de
  `effort: low`. Se `MODEL_SMART` apontar para um modelo que aceita temperatura, o 0
  é enviado.
- **Falha do juiz** (rede, recusa do modelo, JSON inválido): a fala reprova (fail closed).
  Não usei o "fallback" de recusa da API: para este produto, recusar = cortar.
- **Reescrita:** "aprova na segunda tentativa; corta na terceira" foi lido assim: a 1ª
  tentativa é a fala original e a 2ª é a única reescrita; se a 2ª reprova, a fala é
  `dropped` e **não existe** 3ª tentativa. O teste verifica que o reescritor é
  chamado uma única vez.
- **Rejeição do segmento:** mais de 30% das falas `fact` *originais* cortadas, ou menos
  de 6 falas restantes. 30% exatos ainda aprova.
- **Economia:** a última fala precisa ser "Isso não é recomendação de investimento.".
  Se o roteiro esquecer e houver espaço (máximo de 16 falas), o código acrescenta essa
  fala como `banter` do Orlando. Se ela for cortada, o segmento é rejeitado. Há
  também uma lista de expressões proibidas por bloco (`forbidden_phrases`: "invista",
  "hora de comprar"…), checada no estágio 1.
- A duração (60–120 s a 150 palavras/min) e o número de falas (8–16) são validados no
  contrato do roteiro, ou seja, roteiro fora disso conta como inválido e ganha uma
  nova tentativa. Depois dos cortes vale só a regra de rejeição acima.

## Pauta e grade

- Candidatas: artigos publicados nas últimas 36 h que não entraram em nenhuma pauta
  nas últimas 6 h. O `economia` filtra por palavras-chave antes de ir ao LLM. Se o LLM
  da pauta falhar ou devolver ids inválidos, entram as mais recentes.
- O clima entra no `noticias`: São Paulo, Rio de Janeiro e Brasília fixas, mais 2
  capitais em rodízio pelo horário.
- `schedule.yaml`: um bloco é gerado quando seu último segmento **aprovado** passou de
  `every` (`noticias` 20 min, `economia` e `humor` 30 min). Depois de uma rejeição,
  espera `retry_after` (5 min). Na partida, os três blocos estão vencidos e são
  gerados em sequência.
- `config/` é montado só-leitura no container e relido a cada geração: mudar persona,
  grade ou bloqueio não exige rebuild nem restart.

## Orçamento

- O custo vem de `usage` da resposta × preços das variáveis de ambiente. Tokens de
  cache contam como entrada (não há cache configurado neste sprint).
- O teto é diário, no fuso de Brasília. É verificado antes de cada geração e antes de
  cada chamada ao LLM. Ao atingir, a geração em curso é interrompida (o segmento
  fica `rejected` com o motivo), um aviso vai para o log e para `system_events` (uma
  vez por dia) e nada novo é gerado até o dia seguinte. A ingestão continua.

## Memória

- O peso inicial é 1,0 e decai por meia-vida (`MEMORY_HALF_LIFE_DAYS`, padrão 7 dias),
  calculado na consulta. O roteirista recebe as 10 de maior peso efetivo.
- Uma memória é descartada se citar qualquer entidade do banco ou um nome próprio
  detectado pela heurística (exceto os avatares).
