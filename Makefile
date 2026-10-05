# Todos os alvos rodam via Docker Compose no projeto "tvtl". Nada de Go no host.
COMPOSE := docker compose -p tvtl
GO      := $(COMPOSE) --profile tools run --rm --no-deps gotool
N       ?= 5

.PHONY: up down test migrate run logs clean show ingest tidy vet debug-up debug-down build

build:
	@mkdir -p output
	$(COMPOSE) build tvtl

## up: sobe o Postgres, constrói a imagem e aplica as migrações (não inicia a geração)
up: build
	$(COMPOSE) up -d --wait postgres
	$(COMPOSE) run --rm tvtl migrate

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

## run: inicia o loop (ingestão a cada 5 min + geração conforme schedule.yaml)
run: up
	$(COMPOSE) up -d tvtl

logs:
	$(COMPOSE) logs -f --tail=200 tvtl

show:
	$(COMPOSE) run --rm --no-deps -T tvtl show --last $(N)

ingest:
	$(COMPOSE) run --rm tvtl ingest

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
	$(COMPOSE) --profile debug up -d pg-debug

debug-down:
	$(COMPOSE) --profile debug rm -sf pg-debug
