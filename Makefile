# Banka-3-Backend build / run / test targets.
#
# Mirrors the pre-2026-05 Taskfile.yml one-for-one. Run `make help` for
# the list. `.env` is sourced inside any recipe that needs the Postgres
# credentials (only the migrate targets do — `docker compose` reads .env
# itself).

SHELL          := /bin/bash
.SHELLFLAGS    := -eu -o pipefail -c
.DEFAULT_GOAL  := help

SERVICES            := bank exchange gateway notification trading user
MIGRATING_SERVICES  := user exchange bank trading notification
COMPOSE             := docker compose

# Sourced by recipes that need POSTGRES_*. `.env` is optional; same
# behaviour as Task's `dotenv:` (silently no-ops if missing).
LOAD_ENV := set -a; [ -f .env ] && . ./.env; set +a;

# DB URL builder. Inlined inside recipes (after LOAD_ENV) so POSTGRES_*
# come from .env. Quote it carefully — `&` needs to stay inside the
# string passed to the migrate binary.
define DB_URL
postgres://$$POSTGRES_USER:$$POSTGRES_PASSWORD@localhost:$${POSTGRES_PORT:-5432}/$$POSTGRES_DB?sslmode=disable
endef

.PHONY: help
help: ## List available targets
	@awk 'BEGIN {FS = ":.*##"; printf "Available targets:\n"} \
		/^[a-zA-Z0-9_.-]+:.*##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 }' \
		$(MAKEFILE_LIST)

.PHONY: proto
proto: ## Regenerate proto stubs into gen/
	buf generate

.PHONY: build
build: ## Build all service binaries into bin/
	@for svc in $(SERVICES); do \
		echo "==> building $$svc"; \
		(cd services/$$svc && go build -trimpath -o ../../bin/$$svc ./cmd/$$svc); \
	done

.PHONY: up
up: proto ## Bring up the full stack (infra + every service)
	$(COMPOSE) up -d --build

.PHONY: down
down: ## Tear down the local stack
	$(COMPOSE) down

.PHONY: down-v
down-v: ## Tear down and wipe volumes (destructive)
	$(COMPOSE) down -v

.PHONY: restart
restart: down up ## down then up

.PHONY: logs
logs: ## Tail compose logs
	$(COMPOSE) logs -f

.PHONY: ps
ps: ## List running containers
	$(COMPOSE) ps

.PHONY: migrate
migrate: ## Apply migrations across all services
	@$(LOAD_ENV) \
	for svc in $(MIGRATING_SERVICES); do \
		echo "==> migrating $$svc"; \
		migrate -path services/$$svc/migrations \
			-database "$(DB_URL)&x-migrations-table=$${svc}_schema_migrations" up; \
	done

# Roll back N migrations across all services (default 1).
# Usage: make migrate-down [N=2]
.PHONY: migrate-down
migrate-down: N ?= 1
migrate-down: ## Roll back N migrations across all services (N=1 default)
	@$(LOAD_ENV) \
	for svc in $(MIGRATING_SERVICES); do \
		echo "==> rolling back $$svc by $(N)"; \
		migrate -path services/$$svc/migrations \
			-database "$(DB_URL)&x-migrations-table=$${svc}_schema_migrations" down $(N); \
	done

# Create a new migration pair.
# Usage: make migrate-create SVC=user NAME=add_index
.PHONY: migrate-create
migrate-create: ## Create a new migration pair (SVC=… NAME=…)
	@if [ -z "$(SVC)" ] || [ -z "$(NAME)" ]; then \
		echo "usage: make migrate-create SVC=<service> NAME=<migration-name>" >&2; \
		exit 2; \
	fi
	migrate create -ext sql -dir services/$(SVC)/migrations -seq $(NAME)

.PHONY: seed
seed: ## Load development fixtures
	./scripts/db/seed.sh

.PHONY: nuke
nuke: ## Wipe everything and bootstrap (down-v + up + migrate + seed)
	$(MAKE) down-v
	$(MAKE) up
	sleep 3
	$(MAKE) migrate
	$(MAKE) seed

# Modules tested individually; each has its own go.mod.
TEST_MODULES := pkg services/bank services/exchange services/gateway \
                services/notification services/trading services/user

.PHONY: test
test: ## Unit tests across every module (race detector on)
	@for mod in $(TEST_MODULES); do \
		echo "==> test $$mod"; \
		(cd $$mod && go test -race ./...); \
	done

.PHONY: test-integration
test-integration: ## Integration tests (assumes `make up` running)
	@for mod in services/user services/bank services/trading; do \
		echo "==> integration $$mod"; \
		(cd $$mod && go test -race -tags=integration ./...); \
	done

.PHONY: lint
lint: ## golangci-lint
	golangci-lint run

.PHONY: fmt
fmt: ## Format Go source
	gofumpt -w .

.PHONY: fmt-check
fmt-check: ## Fail if any file would be reformatted
	@out=$$(gofumpt -l .); \
	if [ -n "$$out" ]; then gofumpt -d .; exit 1; fi

.PHONY: tidy
tidy: ## go mod tidy across every module
	@for mod in $(TEST_MODULES); do \
		echo "==> tidy $$mod"; \
		(cd $$mod && go mod tidy); \
	done

.PHONY: clean
clean: ## Remove build outputs and generated stubs
	rm -rf bin gen
