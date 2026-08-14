# Billiards — development commands.
#
#   make help    list every target

.DEFAULT_GOAL := help
.PHONY: help up down restart logs ps build test test-game test-unit arch bench cover \
        fmt vet lint check migrate migrate-up migrate-down migrate-status migrate-force \
        psql web-dev web-build web-test run pprof clean env

COMPOSE := docker compose

## ---------------------------------------------------------------------------
## Stack
## ---------------------------------------------------------------------------

up: env ## Start the full stack (traefik, web, server, postgres)
	$(COMPOSE) up -d --build
	@echo
	@echo "  app        http://$$(grep -E '^PUBLIC_HOST=' .env | cut -d= -f2)"
	@echo "  api        http://$$(grep -E '^PUBLIC_HOST=' .env | cut -d= -f2)/api/v1/health"
	@echo "  traefik    http://127.0.0.1:8081/dashboard/"
	@echo "  pprof      http://127.0.0.1:6060/debug/pprof/"
	@echo

down: ## Stop the stack, keeping the database volume
	$(COMPOSE) down

restart: down up ## Restart the stack

logs: ## Follow logs from all services
	$(COMPOSE) logs -f

ps: ## Show service status
	$(COMPOSE) ps

build: ## Rebuild container images without starting them
	$(COMPOSE) build

## ---------------------------------------------------------------------------
## Go
## ---------------------------------------------------------------------------

test: ## Run all Go tests
	go test ./...

test-game: ## Run engine tests only — must pass with no DB, no Docker, no network
	go test ./game/...

test-unit: ## Run tests that need no external services
	go test ./platform/... ./game/... ./tests/...

arch: ## Enforce import boundaries (MEMORY.md §5, ADR 0001)
	go test ./tests/arch/ -v

bench: ## Run benchmarks with allocation counts
	go test -bench=. -benchmem ./game/...

cover: ## Generate and open a coverage report
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out

fmt: ## Format Go sources
	gofmt -w .

vet: ## Run go vet
	go vet ./...

lint: ## Report unformatted files without rewriting them
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
	  echo "Not gofmt'd:"; echo "$$unformatted"; exit 1; \
	fi
	@echo "gofmt clean"

check: lint vet test ## Everything CI runs

run: ## Run the server on the host (needs DATABASE_URL pointing at localhost)
	go run ./apps/server/cmd/server

## ---------------------------------------------------------------------------
## Database
## ---------------------------------------------------------------------------

# golang-migrate reports an empty directory as `first .: file does not exist`, which reads like a
# broken setup rather than "there is nothing to apply". This wraps it in a clear message.
#
# The guard and the command must share one shell — each recipe line runs in its own, so an early
# `exit` on one line does not stop the next. It goes away once Phase 2 adds the first migration and
# the directory is never empty again.
define run-migrate
@if [ -z "$$(ls migrations/*.sql 2>/dev/null)" ]; then \
  echo "No migrations yet — the first one lands in Phase 2. Create one with: make migrate NAME=..."; \
else \
  $(COMPOSE) run --rm migrate $(1); \
fi
endef

migrate-up: ## Apply all pending migrations
	$(call run-migrate,up)

migrate-down: ## Roll back the most recent migration
	$(call run-migrate,down 1)

migrate-status: ## Show the current schema version
	$(call run-migrate,version)

migrate-force: ## Clear a dirty migration state: make migrate-force V=3
	@test -n "$(V)" || (echo "usage: make migrate-force V=<version>" && exit 1)
	$(COMPOSE) run --rm migrate force $(V)

migrate: ## Create a migration pair: make migrate NAME=add_rooms
	@test -n "$(NAME)" || (echo "usage: make migrate NAME=<snake_case_name>" && exit 1)
	$(COMPOSE) run --rm --entrypoint migrate migrate \
	  create -ext sql -dir /migrations -seq $(NAME)
	@echo "Created migrations/*_$(NAME).{up,down}.sql — write the down migration now, not later."

psql: ## Open a psql shell
	$(COMPOSE) exec postgres psql -U $$(grep -E '^POSTGRES_USER=' .env | cut -d= -f2) \
	                              -d $$(grep -E '^POSTGRES_DB=' .env | cut -d= -f2)

## ---------------------------------------------------------------------------
## Web
## ---------------------------------------------------------------------------

web-dev: ## Angular dev server with hot reload
	cd apps/web && npm start

web-build: ## Production build
	cd apps/web && npm run build

web-test: ## Angular unit tests
	cd apps/web && npm test

## ---------------------------------------------------------------------------
## Utilities
## ---------------------------------------------------------------------------

pprof: ## Open an interactive heap profile
	go tool pprof http://127.0.0.1:6060/debug/pprof/heap

env: ## Create .env from .env.example if missing
	@test -f .env || (cp .env.example .env && \
	  echo "Created .env from .env.example — change POSTGRES_PASSWORD before doing anything real.")

clean: ## Remove build artifacts and the database volume (DESTRUCTIVE)
	$(COMPOSE) down -v
	rm -rf apps/web/dist apps/web/.angular coverage.out

help: ## List available targets
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
	  | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'
