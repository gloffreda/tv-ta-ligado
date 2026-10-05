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
