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

- **Extração na ingestão (corrigido depois da 1ª entrega):** na versão inicial, a
  extração só rodava dentro da geração (`Draft`) e só para os artigos da pauta. Com
  isso, `tvtl ingest` não produzia nenhum fato de manchete: 351 artigos e 0 fatos
  `headline`. Agora cada rodada de ingestão extrai fatos de até **40 artigos
  pendentes por ciclo**, do mais recente para o mais antigo, publicados nas últimas
  48 h (a validade de uma manchete). A coluna `articles.facts_extracted_at`
  (migração 0002) marca o que já foi processado.
- **A extração não é geração de conteúdo:** roda mesmo com `GENERATE=off`. Fica
  **pausada dentro das janelas de bloqueio eleitoral** ("rodar fora das janelas") e
  respeita `MAX_DAILY_USD` (cada chamada passa pelo teto). Bloqueio e teto registram
  aviso em `system_events`.
- **Erros de LLM na extração nunca são engolidos:** cada falha vira um evento
  `extract_failed` (com artigo, URL e erro), que aparece em "Avisos" no relatório. O
  artigo continua pendente e é tentado de novo no ciclo seguinte. Erros fatais
  (chave ausente, teto atingido, cancelamento) param o ciclo na primeira falha, para
  não gerar 40 avisos iguais. A queda da pauta para "as mais recentes" também virou
  evento (`rundown_fallback`).
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

## Relatório por execução

- Pedido durante o sprint: um relatório na raiz, pasta `output/`, fora do git, ao
  final de cada execução. Cada subcomando que muda o estado (`ingest`, `rundown`,
  `write`, `check`, `generate`, `run`) grava **um único** arquivo,
  `output/relatorio-<data>-<comando>.md`. Consultas (`show`, `stats`, `migrate`) não
  geram relatório.
- A primeira versão gravava cada relatório duas vezes (o arquivo datado mais uma
  cópia em `output/ultimo.md`), e o `run` ainda gravava um `run-ciclo` por ciclo além
  do final. Agora é um arquivo por execução; no `run`, ele sai ao encerrar o loop.
- O relatório traz fatos novos por `kind` e a taxa de descarte da validação literal
  (fatos propostos pelo LLM × aceitos), calculada a partir dos eventos `facts_extracted`.
- `./output` é um bind mount dentro do projeto. O container `tvtl` roda com o uid/gid
  do dono do projeto (`TVTL_UID`/`TVTL_GID`, padrão 1000), para que os arquivos
  fiquem graváveis e apagáveis no host.

## Chave, URLs e clima (rodada de 05/10/2026, noite)

- **Chave de API:** o serviço `tvtl` lê o `.env` via `env_file`. Na inicialização, o
  log mostra só `ANTHROPIC_API_KEY: presente` ou `ausente`, nunca o valor. Os
  comandos que usam LLM (`ingest`, `run`, `rundown`, `write`, `check`, `generate`)
  falham logo no início, antes de ingerir, se a chave estiver ausente. `migrate`,
  `show`, `stats` e os testes não precisam dela. O `make run` recria o container
  (`--force-recreate`) quando o `.env` é mais novo que `.make/env.stamp`.
- **URLs finais:** `ingest.NormalizeURL` desfaz redirecionadores com a URL embutida
  após `*` (Folha: `redir.folha.com.br/...*https://...`) e com parâmetro
  `url=`/`u=`/`link=`/`target=`. Também remove parâmetros de rastreamento (`utm_*`,
  `at_*`, que a BBC adiciona, `fbclid`, `gclid`…) e o fragmento. A migração 0003
  corrigiu a base existente: 111 URLs da Folha e 19 da BBC, sem perder artigos.
- **"Clima com 26 capitais":** não faltou capital. Às 19:04, a previsão de Campo
  Grande era idêntica à das 18:32 (24,2 °C / 17,7 °C / 75%), mesma impressão digital.
  O `UPSERT` então só renovou a validade da linha existente, e o relatório contava
  apenas linhas *criadas*. As 27 capitais estavam válidas. Correção: a coluna
  `facts.confirmed_at` (migração 0003) marca a reconfirmação, e o relatório passou
  a mostrar novos + reconfirmados por `kind`. Uma capital sem dado de verdade
  (campo nulo) agora vira aviso `source_failed`; antes ia só para o log.

## Sprint 1.1: qualidade e custo (05/10/2026)

### Nomes próprios
- **Entidades tipadas:** a extração devolve `{name, type}` com `type` em
  `person|org|place|other`. Mercado e glossário usam `org`/`other`; clima, `place`.
  Os fatos antigos ficaram com tipo vazio (migração 0004), que não conta como pessoa.
- **Banter reprova** se citar: (a) entidade `person` de qualquer fato do banco;
  (b) sequência de 2+ palavras capitalizadas fora da allowlist e fora do início da
  frase (a palavra que abre a frase é ignorada); (c) prenome de
  `config/first_names.txt`, em qualquer posição; (d) organização ou lugar conhecido
  no banco que não esteja na allowlist **nem nos fatos do segmento**. Em banter,
  "fatos referenciados" foi lido como "fatos da pauta do segmento", porque banter
  não tem `fact_ids`. Assim "Chernobyl" pode aparecer no comentário sobre a matéria
  de Chernobyl.
- **Entidade de uma palavra só** conta com a mesma grafia, inclusive maiúsculas:
  "de novo" não é o partido "Novo" e "vitória" não é a cidade. Entidade toda em
  minúsculas ("mercado") não é nome próprio e é ignorada. Isso eliminou os falsos
  positivos da rodada anterior.
- **Falas `fact`** usam as mesmas regras, mas o nome é aceito quando está nas
  `entities` dos fatos citados ou na allowlist. Uma palavra capitalizada solta que
  não seja prenome nem entidade conhecida passa no estágio 1; quem barra uma troca
  de lugar ou de nome é o juiz, com entailment estrito.
- `first_names.txt` deixa de fora, de propósito, prenomes que também são palavras
  comuns ou lugares (Rosa, Luz, Glória, Vitória, Natal…) e os avatares.

### Glossário
- Kind `glossary`, "TTL nulo" implementado como `expires_at = 9999-12-31`. A coluna
  continua `NOT NULL` e o pgx não lê `infinity` em `time.Time`.
- **Validação da fonte:** HTTP 200 **e** a frase `check` presente no texto da fonte.
  O site do BCB responde 200 com a mesma casca JavaScript para qualquer caminho,
  inclusive caminhos que não existem. Por isso a validação usa a API de conteúdo
  (`/api/paginasite/sitebcb/<caminho>`); página inexistente vem com `metatags: null`.
  O link exibido no ar é a página pública.
- **21 termos carregados:** 8 do BCB (Selic, Copom, inflação, IPCA, índice de preços,
  meta para a inflação, reservas internacionais, Pix) e 13 do glossário do INMET
  (frente fria, frente quente, massa de ar, geada, granizo, onda de calor, nevoeiro,
  garoa, rajada de vento, ciclone extratropical, El Niño, chuva, amplitude térmica).
  As definições são trechos do próprio texto oficial.
- **Ficaram fora:** PIB e desemprego (IBGE). Todos os domínios do IBGE
  (`www.ibge.gov.br`, `agenciadenoticias`, `educa`) respondem 403 com desafio
  Cloudflare ("Just a moment…"), então a fonte não pode ser validada
  automaticamente. Dólar PTAX e câmbio também: o BCB não tem página com texto
  explicativo acessível pela API (`cotacoestodas` e `cambioecapitais` vêm vazias).
- O roteirista recebe até 4 termos citados na pauta (por termo ou sinônimo). A
  "tradução" da Duda é uma fala `fact` que cita o `fact_id` do glossário.

### Juiz, reescrita e continuidade
- O juiz tem dois modos. `fact` segue com entailment estrito. `banter` responde
  `{new_factual_claim, claim, real_person_mocked}` e recebe os fatos do segmento
  como contexto: repetir o que já foi dito não é "afirmação nova".
- **Reescrita:** até 2 por fala. O prompt traz o motivo, todas as regras do tipo
  de fala, os termos da allowlist que aparecem no segmento e a proibição explícita
  de introduzir pessoas. Cada reescrita passa primeiro pelo estágio 1 (sem custo
  de juiz). A primeira que passa vai ao juiz **uma vez**; se o juiz reprovar, a fala
  é cortada. No máximo 2 chamadas ao juiz por fala.
- **Continuidade:** só roda se houve corte. O MODEL_FAST propõe `remove`/`shorten`.
  O código aceita apenas falas banter vivas, nunca a última fala nem a frase de
  encerramento. "Encurtar" precisa ser só apagar palavras (subsequência das
  palavras originais), o que é verificado por código, e a fala encurtada volta pelo
  estágio 1. Fala removida ganha o status `removed`: não conta como corte da
  checagem, mas sai da contagem de falas na regra das 6 falas.

### Humor
- A pauta do humor pede histórias sem pessoas identificadas e reservas extras.
  Depois da extração, o código prefere matérias cujos fatos não têm entidade
  `person`. No contrato do roteiro, fato com pessoa no humor só pode ser lido pelo
  Orlando; se vier diferente, o roteiro é recusado e refeito uma vez.

### Custo
- **Extração preguiçosa (revertendo a decisão da rodada anterior, a pedido):** a
  ingestão não chama LLM. A pauta escolhe pelo **título** (o resumo saiu do prompt)
  e só então extrai fatos das escolhidas, com `max_articles + 3` reservas para o
  caso de alguma não render fatos. O custo da extração entra no custo do segmento.
- **Saúde fora da pauta:** `schedule.yaml → exclude` (trechos de URL como
  `equilibrioesaude`, `/saude/`, mais palavras-chave no título ou resumo).
- **Reprise:** `REPLAY_WHEN_IDLE=on` e `VIEWERS=0` fazem o loop reapresentar um
  segmento aprovado das últimas `REPLAY_WINDOW` (6 h) em vez de gerar. A escolha é
  o menos exibido recentemente, desde que todos os fatos citados continuem
  válidos. A tabela `airings` registra estreias e reprises, e a grade passa a
  olhar a última exibição. O padrão é `off`.
- **Relatório:** custo por propósito e por segmento aprovado (tentativas
  rejeitadas incluídas), custo de extração por artigo e projeção diária
  (24/7 e 7h–23h) a partir da grade.

### BCB
- Timeout de 20 s, 3 tentativas com backoff de 2 s e 4 s: duas pela consulta de
  intervalo e a terceira por `ultimos/20`. Se todas falharem, o último valor válido
  (dentro das 24 h) continua no ar e o evento `bcb_stale` aparece nos Avisos. Sem
  valor válido, a falha vira `source_failed`.

# Sprint 2: voz e linha do tempo

## Parte A: auditoria adversarial do checador (06/10/2026)

`testdata/adversarial/` tem 60 casos (15 números, 10 entidades, 10 inferências,
10 banters com afirmação disfarçada, 5 banters que zombam, 10 corretas). `make
audit` roda o estágio determinístico e o **juiz real** em cada caso. Juiz
indisponível conta como reprovação (fail closed).

**Ajustes feitos por causa da auditoria:**
- Unidade passou a contar no estágio determinístico: R$ × US$ × € e % × pontos
  percentuais precisam bater (`brnum.Compatible`). Número sem unidade continua
  compatível com qualquer uma. "Pontos percentuais" ganhou um `Kind` próprio.
- Prompt do juiz (modo fact): paráfrase sem informação nova ("segue em" para valor
  vigente; definição do glossário em palavras do dia a dia) é permitida. Qualquer
  detalhe que mude ou acrescente fato (quem, quanto, quando, onde, sinal, unidade,
  período, cargo, causa, "recorde", "primeira vez") reprova.

**Matriz final** (juiz fact: `claude-sonnet-5-5`; juiz banter: `claude-haiku-4-5-20251001`):

| | reprovado | aprovado |
|---|---|---|
| deveria reprovar (50) | 50 | **0 (FN)** |
| deveria passar (10) | **1 (FP)** | 9 |

Reprovações corretas: 21 no estágio determinístico e 29 no juiz. Foram 5 rodadas
(3 com Sonnet nos dois modos, 2 com Haiku no banter): **0 FN em todas**. O FP foi 2
na primeira rodada, antes do ajuste do prompt, e 1 nas quatro seguintes.
O FP restante é o C03 ("amplitude térmica é a diferença entre a maior e a menor
temperatura **do dia**"): o juiz reprova "do dia" porque a definição oficial é
genérica. É discutível e fica dentro da meta (≤ 2).

**Juiz de banter no `MODEL_FAST`:** com Haiku no modo banter, a auditoria manteve
0 FN e 1 FP (duas rodadas), acertando os 15 banters (10 afirmações disfarçadas e 5
zombarias). Custo médio por chamada: US$ 0,00105 (Haiku) contra US$ 0,00182
(Sonnet), 42% a menos. Banter é cerca de 45% das falas, então o juiz fica ~19%
mais barato e o segmento ~8% mais barato. **Adotado:** `JUDGE_BANTER_MODEL`
tem como padrão `MODEL_FAST`. O modo fact continua no `MODEL_SMART`.

## Parte B: voz

### Provedores (adendo do Sprint 2: custo zero, locais primeiro)
- Os visemas nunca dependem do provedor. São sempre extraídos do áudio final
  (WAV 24 kHz) pelo serviço `lipsync`, com Rhubarb Lip Sync 1.14 em modo fonético
  (`-r phonetic --extendedShapes GHX`), em container próprio. O servidor HTTP do
  `lipsync` é o próprio binário `tvtl lipsync-server`.
- Três provedores locais, cada um em container próprio, só CPU, com limites
  configuráveis: `tts-chatterbox` (perfil `chatterbox`), `tts-kokoro` (`kokoro`) e
  `tts-piper` (`piper`). Todos expõem a mesma API (`POST /synthesize` → WAV); o
  servidor Python comum fica em `docker/common_server.py`.
- Nuvem (`azure`, `google`, `polly`, `elevenlabs`) implementada com cliente HTTP
  próprio (Polly com SigV4 escrito à mão, Google com API key ou service account),
  testada contra servidor simulado e ativada **só se a chave existir**. Hoje
  nenhuma chave existe, então nenhuma nuvem participa. Nenhum serviço gratuito não
  oficial foi usado.
- **Licenças (ver `LICENSES.md`):** as quatro vozes pt_BR publicadas do Piper
  derivam de modelos de licença não comercial (lessac: só pesquisa; ryan: CC
  BY-NC-SA). Ficam fora do ar; o Piper entrou só na medição. Kokoro (Apache-2.0)
  e Chatterbox (MIT) estão liberados.
- **Chatterbox:** existe o finetune oficial pt-BR
  (`ResembleAI/Chatterbox-Multilingual-pt-br`, V3, MIT). O container usa o código
  de inferência da Space oficial pt-BR e mantém a marca d'água Perth. Não há
  `config/voices/orlando_ref.wav` nem `duda_ref.wav`, então só a voz padrão do
  modelo estaria disponível. As amostras da demo não têm licença declarada e não
  foram usadas.

### Benchmark (06/10/2026, 60 s de texto, limite de CPU de produção, um provedor por vez)

Demanda: ~4 h de áudio novo por dia ≈ **10 min de áudio por hora** (antes de
reprise e cache). Regra: quem não gera mais do que isso fica fora da audição.

| provedor | CPUs | memória | RTF (s de CPU por s de áudio) | min de áudio/hora | resultado |
|---|---|---|---|---|---|
| kokoro | 2 | ~640 MB | **0,66** | **91** | **aprovado** (9× a demanda) |
| piper (faber) | 1 | ~190 MB | 1,47 | 41 | capacidade ok, **fora por licença** |
| chatterbox (pt-BR) | 4 | ~4,9 GB | 8,66 | 7 | **reprovado** (< demanda) |
| chatterbox (informativo) | 8 | ~4,9 GB | 7,53 | 8 | não escala com CPU (geração sequencial) |

- O Kokoro mediu RTF 3,72 (16 min/h) na primeira tentativa: o onnxruntime abria
  threads para as 16 CPUs do host dentro de um limite de 2. Com
  `intra_op_num_threads = TTS_THREADS` (= limite de CPU) caiu para 0,66.
- **Impacto nos outros containers** (`docker stats` antes e a cada 5 s durante):
  a soma de CPU dos 19 vizinhos era 108% na linha de base. Durante o Kokoro: média
  71%, máximo 123%. Durante o Chatterbox: média 91%, um pico de 199%. Durante o
  Piper: média 26%. Nenhum aumento sustentado: os limites de CPU seguraram. Em
  **memória**, o Chatterbox (4,9 GB) derrubou a memória disponível do host de
  ~8 GB para ~3 GB (o swap já estava cheio). Mais um motivo para não deixá-lo no
  ar; ele fica desligado.
- **`TTS_PROVIDER=kokoro`** (único local aprovado). `TTS_FALLBACK=kokoro,piper`, o
  padrão pedido. Como o Piper não está aprovado, ele não é registrado e o
  roteador o ignora; na prática, o fallback é outra voz do próprio Kokoro
  (`fallback_voices` na persona). Só o perfil `kokoro` sobe com o canal
  (`TTS_PROFILES` no Makefile).
- **Fallback:** o provedor de produção é tentado até 3 vezes seguidas. Depois
  disso é rebaixado por 10 min (evento `tts_fallback` nos Avisos) e a fala sai na
  primeira voz disponível de `TTS_FALLBACK`.

### Audição às cegas
- Candidatas só dos provedores aprovados: 3 vozes pt-BR do Kokoro para cada
  avatar, com parâmetros no estilo do personagem (Orlando: rate 0,92 e pitch
  −1 st; Duda: rate 1,08–1,10 e pitch +0,5–1 st). A ordem é embaralhada a cada
  geração e o arquivo não leva o nome do provedor. Mapa em `audition/key.md` (com
  RTF e custo/mês), players em `audition/index.html` (estático).
- O pitch dos locais é aplicado depois da síntese, com ffmpeg
  (`asetrate`/`atempo`), na conversão para o WAV canônico.
- **Padrão até você escolher:** Orlando = `kokoro/pm_santa` (0,92; −1 st); Duda =
  `kokoro/pf_dora` (1,08; +0,5 st). Custo zero; entre as vozes masculinas e a
  feminina, ficou a que combina com cada personagem. Trocar é só editar `voice:`
  no YAML da persona.

### Pronúncia
- `speech.Normalize` gera o `spoken_text`. Valores em reais viram "cinco reais e
  quarenta e três centavos"; com mais de 2 decimais, leitura exata dígito a dígito
  ("quatro vírgula nove oito cinco nove reais"), para nunca arredondar no ar.
  Também cobre porcentagens, datas, horas, ordinais, "°C", "segunda-feira (5)" e
  gênero do número ("duas pessoas", "duzentas vagas"). Siglas e termos vêm de
  `config/pronunciation.yaml`.
- **O texto checado nunca muda:** `lines.text` fica intacto e `lines.spoken_text`
  guarda a versão falada. Antes de sintetizar, `speech.VerifyNumbers` relê o texto
  falado (por extenso → número, com sinal, centavos, escalas, datas e ordinais) e
  exige os mesmos números do texto checado. Se divergir, fala o texto checado como
  está e registra `speech_mismatch`.

### Armazenamento
- Opus em Ogg (32 kbps, mono), um arquivo por `hash(voz + parâmetros + spoken_text)`
  no volume `tvtl_media`. Mesma fala na mesma voz reaproveita o arquivo, sem nova
  síntese e com custo zero na `line_audio`. O render final sai em MP3.
- `audio_assets` é o cache por hash (com os visemas); `line_audio` liga cada fala
  ao arquivo (line_id, path, duration_ms, provider, voice, visemes, cost_usd).
- O custo de TTS vai para `llm_calls` com `purpose = 'tts'` (caracteres em
  `input_tokens`), no mesmo teto diário do LLM. Locais custam 0.

## Parte C: linha do tempo

- **Modelo:** `timeline` (kind segment|replay|bumper|silence, segment_id, block,
  starts_at, ends_at, status) mais `timeline_lines`, que congela cada fala do item
  no agendamento (texto checado, texto falado, hash do áudio, deslocamento e
  duração). Tudo em UTC. Um gatilho no banco recusa qualquer `UPDATE` de horário,
  tipo ou segmento de item já agendado; só o `status` muda
  (scheduled → aired). Itens novos só entram depois do último.
- **Duração do item:** soma das falas + 350 ms entre falas da mesma pessoa +
  500 ms na troca de quem fala + 800 ms de respiro no fim. A vinheta ganha 2 s de
  silêncio planejado depois dela. Tudo configurável em `schedule.yaml → timeline`.
- **Buffer:** o agendador roda a cada 10 s e enche até agora + `BUFFER_MIN`
  (10 min) + 30 s de folga. Sem a folga, as amostras mostraram o buffer caindo
  para 597–599 s entre dois ciclos.
- **O que entra em cada horário:** entre os blocos com candidato, o mais atrasado
  em relação ao seu `every` (tempo desde o último início do bloco ÷ `every`;
  nunca agendado vem primeiro). Prioridade: segmento novo (aprovado, com voz,
  nunca agendado) → reprise → vinheta.
- **Reprise:** só de segmento que **já foi ao ar** (o teste pegou um segmento novo
  entrando como "reprise" durante o bloqueio), criado nas últimas 6 h, sem
  exibição nos últimos 60 min e com todos os fatos citados ainda válidos no
  horário da reprise. Os menos exibidos vêm primeiro. A reprise preenche a grade
  sempre que falta segmento novo, independentemente de `REPLAY_WHEN_IDLE`, que
  continua controlando só se o loop gera conteúdo novo quando `VIEWERS=0`.
- **Vinheta:** "Tá ligado? Já voltamos." na voz da Duda, sintetizada uma vez e
  depois servida do cache.
- **Bloqueio eleitoral:** dentro da janela, o agendador só usa reprise e vinheta
  (a geração já estava bloqueada).
- **Partida a frio:** como o que entra no buffer é imutável, ligar a linha do
  tempo antes de ter áudio pronto daria 10 min de vinheta. O `run` dá voz primeiro
  aos segmentos aprovados da janela de reprise (9,5 min na prova de 06/10) e só
  então liga o agendador.
- **Vazão real da voz:** no pipeline completo (síntese + visemas + Opus), um
  segmento de ~14 falas e ~90 s levou de 1 a 2 min, ou seja RTF efetivo de ~1 a
  1,4 (contra 0,66 só da síntese no benchmark). São 40 a 60 min de áudio por hora,
  ainda 4 a 6 vezes a demanda.
- **`airings` (Sprint 1.1)** foi substituída pela linha do tempo. A tabela ficou
  no banco só por histórico das migrações.

### API (`tvtl serve`, serviço `api`)
- Sem porta no host. O perfil `debug` sobe `api-debug` (um repasse TCP) em
  `127.0.0.1:${TVTL_API_PORT:-58080}`; 58080 estava livre em `ss -ltn`.
- `GET /v1/now`: item no ar, fala atual, `position_ms` dentro da fala,
  `item_position_ms` e horário do servidor. Numa pausa, `line` vem nulo, com
  `next_line` e `next_line_in_ms`. Assim, qualquer cliente que entra no meio de uma
  fala calcula onde está.
- `GET /v1/timeline?from=&to=` (RFC 3339 ou unix ms; janela de até 6 h): itens com
  falas, `text`, `audio_url`, duração, deslocamento, horários absolutos, visemas
  e fontes (nome e URL) de cada fala `fact`.
- `GET /v1/events` (SSE): `item_started`, `line_started` e `item_scheduled`, com
  ping a cada 15 s.
- `GET /media/{hash}.ogg`: `Cache-Control: public, max-age=31536000, immutable`.
  O nome é validado (64 hex + `.ogg`), o que impede path traversal.
- `GET /healthz`.

### Homologação (`compose.staging.yaml`)
- Um perfil dentro do `compose.yaml` não muda o nome do projeto. Por isso a
  homologação é um arquivo separado com `name: tvtl-staging`, rede
  `tvtl_staging_net`, volumes `tvtl_staging_pgdata` e `tvtl_staging_media`,
  Postgres, Kokoro e lipsync próprios, e `CLOCK_OFFSET=${STAGING_CLOCK_OFFSET:-+2h}`.
  Comandos: `make staging-up`, `staging-logs` e `staging-down`.
- `CLOCK_OFFSET` desloca o relógio do app inteiro (geração, orçamento, linha do
  tempo e API). O `created_at` dos segmentos passou a ser gravado com o relógio do
  app, para a janela de reprise funcionar com o deslocamento.
- Um teste (`internal/deploy`) garante que o arquivo de homologação não cita o
  volume nem a rede do projeto principal, não publica porta e aponta o
  `DATABASE_URL` para o banco próprio.

## Parte D: render

- `tvtl render --from now|-15m|RFC3339 --minutes 15 --out out/` (`make render`).
  Se a janela passa do que já está agendado, ele agenda até o fim dela antes
  (sempre no fim da fila). O PCM é montado com cada fala no milissegundo exato do
  seu deslocamento e silêncio nas pausas e nos buracos, o que torna o MP3 uma
  reprodução fiel da linha do tempo. Saem `out/tvtl-<data>.mp3` e
  `out/legendas.srt` ("ORLANDO: texto"), alinhado pelos mesmos deslocamentos.

## Prova de ponta a ponta (06/10/2026)

- **`tvtl run` + `tvtl serve`, 30 min** (01:39 a 02:09 UTC, depois de 9,5 min de
  voz dos segmentos pendentes): buffer amostrado a cada 15 s direto no banco (119
  amostras), **mínimo de 622 s**, nunca abaixo de 600 s; o agendador registrou
  mínimo de 10 min 30 s. **0 buracos de silêncio não planejados.** Foram ao ar 12
  estreias (18,7 min) e 160 vinhetas (11,4 min). Nenhuma reprise: a linha do tempo
  nasceu limpa e a reprise exige exibição anterior com ≥ 60 min de distância. Na
  primeira hora de operação, a falta de segmento novo só pode ser coberta com
  vinheta.
- A primeira tentativa da prova encontrou dois defeitos, já corrigidos: o volume
  `tvtl_media` criado antes do ajuste de dono na imagem (o `make up` agora corrige
  o dono) e o glossário expirando termos por falha de rede do INMET (agora falha
  transitória mantém o termo).
- **Render:** `make render FROM=-25m MIN=15` gerou MP3 de 15:00, com volume médio
  de −24,6 dB, pico de −1,1 dB, nenhum silêncio ≥ 3 s e 146 legendas alinhadas.
- **Homologação:** `tvtl-staging` subiu com o relógio exatamente +2 h e banco
  próprio (0 segmentos). As contagens do banco principal (segmentos, linha do
  tempo, chamadas de LLM) ficaram idênticas antes e depois.
- **`make up && make test` do zero:** imagens do projeto e volumes de cache do Go
  removidos, 15 pacotes verdes em 2 min 26 s. O cache do BuildKit é compartilhado
  com os outros projetos do host e não foi apagado (seria um prune global).
- **Vizinhos:** os 19 containers de outros projetos ficaram com o mesmo ID,
  `StartedAt` e `RestartCount` do início ao fim do Sprint 2.

### Custo de voz
- **Por segmento:** ~1.400 caracteres falados e ~87 s de áudio. Com Kokoro, US$ 0.
  Só como referência, se fosse nuvem: Azure Neural US$ 0,021, Google Neural2
  US$ 0,022, ElevenLabs Flash US$ 0,056.
- **Por dia:** a grade atual (notícias a cada 20 min, economia e humor a cada 30)
  dá 7 segmentos novos por hora. 24/7: 168 segmentos e ~235 mil caracteres
  (~7 M/mês). Horário nobre (7h–23h): 112 segmentos e ~157 mil caracteres. Com
  Kokoro, **US$ 0/dia** nos dois cenários. Na nuvem seriam US$ 3,5–9,4/dia (24/7)
  ou US$ 2,4–6,3/dia (horário nobre).
- **Total projetado (LLM + TTS), medido nesta rodada:** 24/7 **US$ 9,52/dia**
  (~US$ 286/mês); horário nobre **US$ 6,35/dia** (~US$ 190/mês). Os dois passam do
  `MAX_DAILY_USD` padrão de US$ 5: com a grade atual, a geração para antes do fim
  do dia. É preciso subir o teto, espaçar a grade ou usar a reprise sem audiência.

# Sprint 3: o canal no ar (06/10/2026)

## Parte 0: ajustes do Sprint 2

### Descarte de 17,9% na extração (investigado)
Não era bug do validador. Nas matérias de resultado eleitoral, o LLM expandia
siglas de UF (MA → Maranhão; MS → Mato Grosso do Sul, 4 casos) e somava
números (DF + cinco estados → 6, 1 caso). A validação literal descartou
corretamente: "Maranhão" e "6" não estão no texto. O prompt do extrator agora
proíbe expandir siglas e calcular. O descarte alto foi sinal de que a trava
funciona, não de defeito.

### Checador
- Dígito colado em letra não é número (g1, B3, G20, 5G, COP30, Poder360). Os
  códigos foram para a allowlist (`codigos`, `veiculos`) e para o dicionário de
  pronúncia ("gê um", "cinco gê"). A normalização de fala mascara esses códigos
  antes de converter números e confere os números depois.
- "Uma fala, um tipo": numa fala `fact`, a primeira frase de conteúdo (depois
  de uma saudação curta) tem de trazer o fato (número, entidade ou duas palavras
  de conteúdo da afirmação). Senão reprova com o pedido de separar em `banter` +
  `fact`. O roteirista recebe a mesma regra.
- Cargo em banter: banter que cita pessoa real pelo cargo ou função ("o técnico
  do time", "o presidente do Banco Central") reprova no estágio determinístico.
  Motivo: com a Duda mais ácida, o juiz de banter (Haiku) deixou passar 2 casos
  assim na auditoria (Z03 e A03). Com a regra, 0 FN. Custo: a Duda não pode
  ironizar um cargo nem de leve; ela tem alvos de sobra (situação, Orlando,
  máquina, ela mesma, o canal).

### Cache de prompt
- A parte fixa (regras, personas do elenco, allowlist em ordem estável) vai no
  `system` com `cache_control`. A allowlist era lida de um mapa (ordem aleatória
  a cada leitura) e quebraria o cache: agora sai em ordem alfabética de categoria.
- Juiz de fatos: TTL padrão de 5 min. As chamadas vêm em rajada (todas as falas
  de um segmento), e o cache é lido de fato.
- Roteiro e reescrita: medido na homologação, o cache de 5 min **só gravava e
  nunca lia** (19.396 tokens gravados, 0 lidos em 4 roteiros), porque cada bloco
  roda a cada 20–30 min. Gravar custa 1,25x: o cache de 5 min encarecia o
  roteiro. Passaram para TTL de 1 h (gravação a 2x, leitura a 0,1x): com ~7
  roteiros por hora e o mesmo `system` em todos os blocos, a conta fecha com
  uma gravação e várias leituras.
- O mínimo cacheável é de 4.096 tokens no Haiku 4.5: os prompts de pauta e
  extração (Haiku) ficam abaixo e não são cacheados.

### Vozes da Glória
Kokoro tem uma só voz feminina pt-BR (`pf_dora`), que já é da Duda. Audição às
cegas em `audition/gloria/` com `pf_dora` mais lenta e grave e quatro misturas
de `pf_dora` com vozes femininas de outros idiomas (peso pt-BR ≥ 0,5, para a
pronúncia continuar inteligível). Pela distância de estilo em relação à
`pf_dora` pura, `hf_alpha` (0,209) e `if_sara` (0,174) são as mais distintas;
o padrão até a escolha é `pf_dora*0.5+if_sara*0.5`, que ainda soa pt-BR.
Google Cloud TTS: sem chave no `.env`, fica fora. Cota gratuita oficial
(consultada em 06/10/2026): Standard e WaveNet 4 milhões de caracteres/mês;
Neural2, Chirp 3 HD e Studio 1 milhão/mês. Os boletins de tempo (~600
caracteres, 4 por dia) caberiam com folga.

## Parte A: site e player
- Duas páginas, um canal: `/` (PixiJS) e `/humano` (SVG) sobre o mesmo núcleo
  (`web/src/core`). O seletor troca de visual sem recarregar, e por isso o som
  continua: o navegador não deixaria religar o áudio sozinho depois de uma
  navegação.
- PixiJS em **Canvas 2D a 30 quadros**, não WebGL. Sem GPU, o WebGL por software
  (SwiftShader) roubava tanta CPU que o áudio da página tocava a ~0,7x (deriva
  de até 900 ms). Com Canvas 2D: deriva constante de ~85 ms, zero correções. A
  cena é só retângulos e texto; Canvas 2D sobra.
- Fontes servidas como arquivo (Vite com `assetsInlineLimit: 0`): a CSP não
  aceita `data:` em `font-src`.
- O túnel rápido (`*.trycloudflare.com`) **não entrega SSE em tempo real**: a
  origem escreve, o cliente não recebe. O player não depende do SSE: segue o
  relógio, relê a sessão a cada 5 s e a linha do tempo a cada 15 s. O SSE
  continua servindo para contar a audiência (a conexão chega à origem) e
  funciona normalmente num túnel nomeado.
- O token de administração nunca é guardado: some do formulário assim que o
  pedido sai. "Desligar" usa uma chave da sessão que fica só na memória da aba;
  se a página foi recarregada, pede o token.

## Modo sob demanda
- `RUN_MODE=on_demand` (padrão). Uma trava única (`session.Gate`) recusa LLM,
  voz, ingestão e agendamento sem sessão ativa (`no_active_session`, registrado
  em `system_events` no máximo uma vez por minuto). A exceção é `TVTL_MANUAL=1`,
  que só os alvos manuais do Makefile passam (`audit`, `audition`, `ingest`,
  `bench`, `tvtl ARGS=…`).
- O `tvtl run` é um supervisor: em repouso, só consulta `sessions` a cada 2 s.
  Na partida, uma sessão ativa encontrada é **encerrada** (`restart`): reiniciar
  nunca liga nada.
- Ao ligar: abertura (uma fala do Orlando, 8 variações x 3 saudações, áudio em
  cache depois da primeira vez) → linha do tempo → dados com os fatos que ainda
  valem → ingestão → dados de novo → geração com LLM em paralelo. A vinheta é
  "preguiçosa": no máximo 15 s de vinheta agendada à frente, para que o primeiro
  segmento pronto entre logo, em vez de ficar atrás de 10 min de vinhetas.
- Tarefas periódicas só dentro de sessão: glossário (a cada 24 h) e consolidação
  da memória (no máximo a cada 7 dias), pela tabela `jobs`.
- Segmentos de dados numa rotina própria (a cada 10 s), separada da geração com
  LLM (que prende o ciclo por minutos). Manchetes com estoque de 2.
- Manchetes também pelo **título** das matérias ingeridas, lido literalmente e
  creditado ao veículo: é o insumo mais farto e não custa LLM. Fica de fora o
  título com nome fora da allowlist (sem entidade checada, o título não serve de
  fonte para um nome), o tema fora do brief e títulos com pergunta ou aspas.
  No máximo 2 por veículo por segmento. Palavras de morte, violência, desastre e
  doença marcam o segmento como sensível.
- As páginas automáticas de resultado eleitoral do g1 (uma por município e
  seção: milhares por turno) ficaram fora da pauta e das manchetes
  (`resultado-das-eleicoes` em `exclude.url_parts`). São literais e têm fonte,
  mas inundavam o ar com resultados hiperlocais repetidos.

## Composição do ar e custo (homologação, 06/10/2026, madrugada)
Quatro rodadas de 30 min com 2 navegadores (mais a página /humano), sessão
ligada pelo botão. As duas primeiras mostraram o problema (vinheta de 56% e
62% depois de 15 min); as correções acima levaram a 0%.

| rodada | estreia | dados | reprise | vinheta | vinheta após 15 min | buracos |
|---|---|---|---|---|---|---|
| 1 (antes das correções) | 20,7% | 23,2% | 0% | 56,1% | 61,8% | 0 |
| 2 | 14,8% | 28,7% | 41,1% | 15,4% | **0%** | 0 |
| 3 (manchetes secaram) | 13,4% | 29,7% | 44,9% | 12,0% | 23,5% | 0 |
| 4 (código final) | 4,4% | 16,8% | 78,8% | **0%** | **0%** | 0 |

A rodada 3 falhou porque, às 2h da manhã, chegavam poucas matérias e a janela
das manchetes era de 6 h (4 h reais, com o relógio da homologação 2 h à frente).
Na rodada 4 a estreia caiu (4,4%) porque, depois de 9 sessões seguidas na mesma
madrugada, as matérias candidatas já tinham sido usadas em pautas nas últimas
6 h ("pauta sem fatos válidos"): a reprise cobriu. Com o noticiário do dia e
sessões espaçadas, a proporção de estreias sobe.

- **Custo por hora no ar** (sessões com geração ativa): US$ 0,38 a 0,41/h
  (sessão 6: US$ 0,212 em 30,7 min; sessão 7: US$ 0,195 em 30,7 min). Sessão
  típica de 30 min: ~US$ 0,20. Voz: US$ 0 (Kokoro local).
- **Cache de prompt**, medido nas sessões 6–9: US$ 0,513 gastos contra
  US$ 0,698 sem cache: **26,5% a menos** no total de LLM (juiz de fatos e
  reescrita são os que mais leem do cache).
- **Do clique à primeira fala**: na 1ª sessão, antes das correções, 11 min 51 s
  (148 vinhetas agendadas na frente de tudo). Depois: 5,3 s com a abertura
  sintetizada na hora e **0,97 a 1,75 s** com o áudio da abertura em cache. A
  página vê o balão 2 a 12 s depois do clique (ela relê a sessão a cada 5 s pelo
  túnel rápido).
- **Custo diário**: em repouso, US$ 0. Ligado, ~US$ 0,40 por hora de sessão;
  cada sessão tem teto de US$ 2 e 60 min, e o dia continua com teto de US$ 8.

## Sincronia (rodadas 2 a 4, 30 min cada)
- Dois navegadores: 99,6% das falas com diferença < 250 ms (p95 17–18 ms).
- `/` x `/humano`: 99,6–100% < 250 ms (p95 17–33 ms); mesmo visema na boca de
  quem fala em 92,8–98,2% das amostras do mesmo instante (o resto é borda entre
  dois visemas).
- Balão x início da fala: 99,6% < 250 ms (p95 16 ms). FONTE em 100% das falas
  `fact`. Áudio x relógio: p95 de 82–100 ms (latência de saída constante).

## Bug achado nos testes: consolidação da memória toda sessão
A consolidação semanal rodava em toda sessão: `LogCapped` passava o mesmo
parâmetro como inteiro e dentro de `jsonb_build_object`, o Postgres não
inferia o tipo, o job terminava com erro e `jobs` nunca era gravado. As fusões
em si estavam certas (8 registros em `persona_memory_log`). Corrigido com
`$2::int` e um teste de integração (memória do par, limite de 3 usos por
semana, `LogCapped`, `JobDue`, humor com meia-vida).

## Isolamento no host
- Proxies e túneis encontrados antes de subir: `traefik-traefik-1` (0.0.0.0:80
  e 443), `novatrak-staging-caddy` (8087), o processo `caddy` do host (do
  novatrak) e o `tailscaled`. Nenhum `cloudflared` de outro projeto. Nada disso
  foi tocado.
- Os containers `tvtl-site-web` e `tvtl-site-tunnel` não existiam.
- Durante o sprint, 8 containers do `novatrak-staging` foram recriados (IDs
  novos entre 04:10 e 04:36 UTC) pelo próprio projeto deles (Compose em
  `/home/mini/projects/novatrak/infra`). Nenhum comando deste sprint mexeu
  neles; `traefik`, `caddy`, `postgres`, `redis` e os demais seguem com o mesmo
  ID e `StartedAt`.

## Adendo: senha do site e novo layout (06/10/2026)
- **Senha em vez de token na tela.** O site inteiro fica atrás de login
  (`SITE_GATE=on`). Só o hash vai para o `.env`, em argon2id (64 MiB, 3 passadas),
  num formato sem `$` (o Compose interpolaria `$` ao ler o `.env`). O cookie é
  assinado com HMAC-SHA256, com chave derivada do segredo e do hash da senha:
  trocar a senha invalida todos os logins.
- **Onde o portão roda.** As páginas são estáticas no nginx: `auth_request` em
  `/v1/auth/check` e 302 para `/entrar` (com `absolute_redirect off`, para o
  `Location` não sair `http://`: o túnel fala HTTP com o nginx). `/v1` e `/media`
  são barrados na própria api (401), que é quem valida o cookie.
- **Ligar a TV.** Para quem está logado, o painel tem só "Confirmar": cookie +
  Origin do site. O token de administração ficou para a linha de comando.
- **Layout.** O painel lateral "Fontes deste bloco" saiu. A TV ocupa a largura
  do conteúdo (máximo de 1440 px). Abaixo dos cards, uma linha discreta: "FONTES"
  + os veículos das falas fact do item no ar e, à direita, o aviso de IA, que
  nunca some. A etiqueta "FONTE · veículo" dos balões continua.
