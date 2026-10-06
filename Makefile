# Todos os alvos rodam via Docker Compose no projeto "tvtl". Nada de Go no host.
COMPOSE := docker compose -p tvtl
GO      := $(COMPOSE) --profile tools run --rm --no-deps gotool
N       ?= 5

.PHONY: up down test migrate run logs clean show ingest tidy vet debug-up debug-down build audit audition bench render staging-up staging-down staging-logs url staging-url web-test e2e e2e-prod-rest tvtl rest-test

# Comandos manuais (no terminal): a ÚNICA exceção à trava "sem sessão, sem
# API". Só estes alvos passam TVTL_MANUAL=1. O canal em si (tvtl run) nunca.
MANUAL  := $(COMPOSE) run --rm -e TVTL_MANUAL=1

# Túnel nomeado se houver CF_TUNNEL_TOKEN no .env; senão, túnel rápido.
export CF_TUNNEL_ARGS := $(shell grep -qE '^CF_TUNNEL_TOKEN=.+' .env 2>/dev/null && echo run)

# Vozes locais que sobem com o canal (perfis do compose). Só o aprovado no benchmark.
TTS_PROFILES ?= kokoro
export COMPOSE_PROFILES := $(TTS_PROFILES)

build:
	@mkdir -p output
	$(COMPOSE) build tvtl lipsync tts-kokoro web

## up: sobe TUDO em repouso (site, api, banco, túnel, vozes, supervisor).
## Nada liga sozinho: o canal só trabalha depois do botão "Ligar a TV".
up: build
	$(COMPOSE) up -d --wait postgres
	$(COMPOSE) run --rm tvtl migrate
	@# volume de mídia gravável pelo usuário do container (volumes antigos nascem do root)
	$(COMPOSE) run --rm --no-deps -T --user 0 --entrypoint chown tvtl -R $${TVTL_UID:-1000}:$${TVTL_GID:-1000} /media
	@mkdir -p .make
	@if [ ! -f .make/env.stamp ] || [ .env -nt .make/env.stamp ]; then \
	  echo ".env novo ou alterado: recriando tvtl e api"; \
	  $(COMPOSE) up -d lipsync tts-kokoro web tunnel && $(COMPOSE) up -d --force-recreate tvtl api && touch .make/env.stamp; \
	else \
	  $(COMPOSE) up -d lipsync tts-kokoro web tunnel tvtl api; \
	fi
	@echo "em repouso. Endereço público: make url"

## url: endereço público atual (túnel rápido *.trycloudflare.com)
url:
	@docker logs tvtl-tunnel-1 2>&1 | grep -oE 'https://[a-z0-9-]+\.trycloudflare\.com' | tail -1 || true
	@grep -qE '^CF_TUNNEL_TOKEN=.+' .env 2>/dev/null && echo "(túnel nomeado: use o hostname configurado no Cloudflare)" || true

## down: derruba só o projeto tvtl (volumes preservados)
down:
	$(COMPOSE) --profile debug --profile test --profile tools down --remove-orphans

## test: testes offline (mock de LLM) + integração num Postgres efêmero (tmpfs) + site
test: web-test
	$(COMPOSE) --profile test up -d --wait postgres-test
	$(GO) go test -race -count=1 ./... ; status=$$?; \
	$(COMPOSE) --profile test rm -sf postgres-test >/dev/null; exit $$status

## web-test: testes de unidade do player (vitest) num container node
web-test:
	docker run --rm --memory 2g --cpus 2 -v $(CURDIR)/web:/web -w /web node:22.23.3-slim \
	  sh -c 'npm ci --no-audit --no-fund >/dev/null && npx vitest run && npx tsc --noEmit -p . ; s=$$?; chown -R $(shell id -u):$(shell id -g) /web; exit $$s'

migrate:
	$(COMPOSE) run --rm tvtl migrate

## run: o mesmo que up (o supervisor sobe em repouso; o botão da página liga a sessão)
run: up

## tvtl: comando manual, ex.: make tvtl ARGS="generate --block noticias" (custa API)
tvtl: build
	$(MANUAL) -T tvtl $(ARGS)

logs:
	$(COMPOSE) logs -f --tail=200 tvtl

show:
	$(COMPOSE) run --rm --no-deps -T tvtl show --last $(N)

ingest:
	$(MANUAL) tvtl ingest

## audition: candidatas de voz às cegas em audition/ (provedores aprovados)
audition: build
	@mkdir -p audition
	$(COMPOSE) up -d --wait lipsync tts-kokoro
	$(MANUAL) -T -v $(CURDIR)/audition:/app/audition tvtl audition --out audition $(if $(ONLY),--only $(ONLY))

## bench: fator de tempo real dos TTS locais (sobe cada um com o limite de produção)
bench: build
	$(COMPOSE) --profile chatterbox --profile piper up -d tts-chatterbox tts-kokoro tts-piper
	$(MANUAL) -T tvtl tts-bench --providers chatterbox,kokoro,piper --seconds 60

## audit: 73 casos adversariais contra o juiz REAL (custa API). BANTER_MODEL=... para comparar.
audit: build
	$(MANUAL) -T -v $(CURDIR)/testdata:/app/testdata:ro tvtl audit --dir testdata/adversarial $(if $(BANTER_MODEL),--banter-model $(BANTER_MODEL))

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
	@echo "homologação em repouso. Endereço: make staging-url"
staging-url:
	@docker logs tvtl-staging-tunnel-1 2>&1 | grep -oE 'https://[a-z0-9-]+\.trycloudflare\.com' | tail -1 || true

## e2e: Playwright em container contra a HOMOLOGAÇÃO (liga sessão só lá).
## Recusa rodar se o endereço for o de produção.
E2E_MIN ?= 30
e2e:
	@url=$$($(MAKE) -s staging-url); prod=$$($(MAKE) -s url); \
	test -n "$$url" || { echo "homologação sem endereço (make staging-up)"; exit 1; }; \
	test "$$url" != "$$prod" || { echo "RECUSADO: o endereço é o de produção"; exit 1; }; \
	mkdir -p out/screens out/e2e; \
	export TVTL_STAGING_ADMIN_TOKEN=$$(grep -E '^TVTL_STAGING_ADMIN_TOKEN=' .env | cut -d= -f2-); \
	docker run --rm --memory 3g --cpus 2 --ipc=host -v $(CURDIR)/e2e:/e2e -v $(CURDIR)/out:/out -w /e2e \
	  -e BASE_URL=$$url -e PROD_URL=$$prod -e E2E_MIN=$(E2E_MIN) \
	  -e TVTL_STAGING_ADMIN_TOKEN \
	  mcr.microsoft.com/playwright:v1.63.0-noble sh -c 'npm ci --no-audit --no-fund >/dev/null && npx playwright test $(E2E_ARGS); s=$$?; chown -R $(shell id -u):$(shell id -g) /out /e2e; exit $$s'

## e2e-prod-rest: produção SÓ em repouso (FORA DO AR, HTTPS, 404s). Nunca liga.
e2e-prod-rest:
	@url=$$($(MAKE) -s url); test -n "$$url" || { echo "produção sem endereço"; exit 1; }; \
	mkdir -p out/screens; \
	docker run --rm --memory 2g --cpus 2 --ipc=host -v $(CURDIR)/e2e:/e2e -v $(CURDIR)/out:/out -w /e2e \
	  -e BASE_URL=$$url -e PROD_REST=1 \
	  mcr.microsoft.com/playwright:v1.63.0-noble sh -c 'npm ci --no-audit --no-fund >/dev/null && npx playwright test prod-rest; s=$$?; chown -R $(shell id -u):$(shell id -g) /out /e2e; exit $$s'
staging-down:
	$(STAGING) down --remove-orphans
staging-logs:
	$(STAGING) logs -f --tail=200 tvtl
