-include .env
export

SERVICES := bank exchange gateway notification trading user
COMPOSE  := docker compose

.PHONY: help proto build up down down-v restart logs ps \
        migrate migrate-create seed nuke \
        test test-integration lint fmt fmt-check tidy clean

help:
	@echo "Targets:"
	@echo "  proto             Regenerate proto stubs into gen/"
	@echo "  build             Build all service binaries"
	@echo "  up                docker compose up -d"
	@echo "  down              docker compose down"
	@echo "  down-v            docker compose down -v (wipes volumes)"
	@echo "  migrate           Apply migrations across all services"
	@echo "  migrate-create    NEW=<name> SVC=<svc>: create new migration pair"
	@echo "  seed              Load development fixtures"
	@echo "  nuke              down-v + up + migrate + seed"
	@echo "  test              Unit tests with race detector"
	@echo "  test-integration  Integration tests (requires docker)"
	@echo "  lint              golangci-lint"
	@echo "  fmt               gofumpt -w"
	@echo "  tidy              go mod tidy across all modules"

proto:
	buf generate

build:
	@for svc in $(SERVICES); do \
		echo "==> building $$svc"; \
		( cd services/$$svc && go build -o ../../bin/$$svc ./cmd/$$svc ) || exit 1; \
	done

up:
	$(COMPOSE) up -d --build

down:
	$(COMPOSE) down

down-v:
	$(COMPOSE) down -v

restart: down up

logs:
	$(COMPOSE) logs -f

ps:
	$(COMPOSE) ps

migrate:
	./scripts/db/migrate.sh up

migrate-create:
	@test -n "$(NEW)" || (echo "usage: make migrate-create NEW=<name> SVC=<svc>"; exit 1)
	@test -n "$(SVC)" || (echo "usage: make migrate-create NEW=<name> SVC=<svc>"; exit 1)
	./scripts/db/migrate.sh create $(SVC) $(NEW)

seed:
	./scripts/db/seed.sh

nuke: down-v up
	@sleep 3
	$(MAKE) migrate
	$(MAKE) seed

test:
	go test -race ./...

test-integration:
	go test -race -tags=integration ./...

lint:
	golangci-lint run

fmt:
	gofumpt -w .

fmt-check:
	@diff -u <(echo -n) <(gofumpt -d .) || (echo "run 'make fmt'"; exit 1)

tidy:
	@for m in pkg services/bank services/exchange services/gateway services/notification services/trading services/user; do \
		echo "==> tidy $$m"; \
		( cd $$m && go mod tidy ); \
	done

clean:
	rm -rf bin gen
