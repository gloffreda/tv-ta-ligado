# Todos os alvos rodam via Docker Compose no projeto "tvtl". Nada de Go no host.
COMPOSE := docker compose -p tvtl
GO      := $(COMPOSE) --profile tools run --rm --no-deps gotool
N       ?= 5

.PHONY: up down test migrate run logs clean show ingest tidy vet debug-up debug-down build audit audition bench render staging-up staging-down staging-logs

# Vozes locais que sobem com o canal (perfis do compose). Só o aprovado no benchmark.
TTS_PROFILES ?= kokoro
export COMPOSE_PROFILES := $(TTS_PROFILES)

build:
	@mkdir -p output
	$(COMPOSE) build tvtl lipsync tts-kokoro

## up: sobe o Postgres, constrói a imagem e aplica as migrações (não inicia a geração)
up: build
	$(COMPOSE) up -d --wait postgres
	$(COMPOSE) run --rm tvtl migrate
	@# volume de mídia gravável pelo usuário do container (volumes antigos nascem do root)
	$(COMPOSE) run --rm --no-deps -T --user 0 --entrypoint chown tvtl -R $${TVTL_UID:-1000}:$${TVTL_GID:-1000} /media

## down: derruba só o projeto tvtl (volumes preservados)
down:
	$(COMPOSE) --profile debug --profile test --profile tools down --remove-orphans

## test: testes offline (mock de LLM) + integração num Postgres efêmero (tmpfs)
test:
	$(COMPOSE) --profile test up -d --wait postgres-test
	$(GO) go test -race -count=1 ./... ; status=$$?; \
	$(COMPOSE) --profile test rm -sf postgres-test >/dev/null; exit $$status

migrate:
	$(COMPOSE) run --rm tvtl migrate

## run: inicia o loop (ingestão a cada 5 min + geração conforme schedule.yaml).
## Se o .env é novo ou mudou desde a última subida, o container é recriado.
run: up
	@test -f .env || { echo "falta o .env (copie de .env.example)"; exit 1; }
	@mkdir -p .make
	@if [ ! -f .make/env.stamp ] || [ .env -nt .make/env.stamp ]; then \
	  echo ".env novo ou alterado: recriando o container tvtl"; \
	  $(COMPOSE) up -d lipsync tts-kokoro && $(COMPOSE) up -d --force-recreate tvtl api && touch .make/env.stamp; \
	else \
	  $(COMPOSE) up -d lipsync tts-kokoro tvtl api; \
	fi

logs:
	$(COMPOSE) logs -f --tail=200 tvtl

show:
	$(COMPOSE) run --rm --no-deps -T tvtl show --last $(N)

ingest:
	$(COMPOSE) run --rm tvtl ingest

## audition: candidatas de voz às cegas em audition/ (provedores aprovados)
audition: build
	@mkdir -p audition
	$(COMPOSE) up -d --wait lipsync tts-kokoro
	$(COMPOSE) run --rm -T -v $(CURDIR)/audition:/app/audition tvtl audition --out audition

## bench: fator de tempo real dos TTS locais (sobe cada um com o limite de produção)
bench: build
	$(COMPOSE) --profile chatterbox --profile piper up -d tts-chatterbox tts-kokoro tts-piper
	$(COMPOSE) run --rm -T tvtl tts-bench --providers chatterbox,kokoro,piper --seconds 60

## audit: 60 casos adversariais contra o juiz REAL (custa API). BANTER_MODEL=... para comparar.
audit: build
	$(COMPOSE) run --rm -T -v $(CURDIR)/testdata:/app/testdata:ro tvtl audit --dir testdata/adversarial $(if $(BANTER_MODEL),--banter-model $(BANTER_MODEL))

## clean: remove containers E volumes do projeto (pede confirmação)
clean:
	@printf "Isto apaga os volumes do projeto tvtl (banco incluído). Digite 'tvtl' para confirmar: "; \
	read ans; [ "$$ans" = "tvtl" ] || { echo "cancelado"; exit 1; }
	$(COMPOSE) --profile debug --profile test --profile tools down -v --remove-orphans

tidy:
	$(GO) sh -c 'go mod tidy && chown $(shell id -u):$(shell id -g) go.mod go.sum'

vet:
	$(GO) go vet ./...

debug-up:
	@ss -ltn | grep -q ":$${TVTL_PG_PORT:-55432} " && { echo "porta $${TVTL_PG_PORT:-55432} ocupada"; exit 1; } || true
	@ss -ltn | grep -q ":$${TVTL_API_PORT:-58080} " && { echo "porta $${TVTL_API_PORT:-58080} ocupada"; exit 1; } || true
	$(COMPOSE) --profile debug up -d pg-debug api-debug

debug-down:
	$(COMPOSE) --profile debug rm -sf pg-debug api-debug

## render: MP3 + legendas.srt da linha do tempo em out/ (FROM=now|-15m|RFC3339, MIN=15)
FROM ?= now
MIN  ?= 15
render:
	@mkdir -p out
	$(COMPOSE) run --rm -T -v $(CURDIR)/out:/app/out tvtl render --from $(FROM) --minutes $(MIN) --out out

## staging: instância inteira no futuro (CLOCK_OFFSET), projeto tvtl-staging, banco e mídia próprios
STAGING := docker compose -p tvtl-staging -f compose.staging.yaml
staging-up: build
	$(STAGING) up -d --wait postgres
	$(STAGING) up -d
staging-down:
	$(STAGING) down --remove-orphans
staging-logs:
	$(STAGING) logs -f --tail=200 tvtl
