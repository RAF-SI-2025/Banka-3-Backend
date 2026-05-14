# Banka-3-Backend build / run / test targets.
#
# Run `make help` for the list. The only host requirement is Docker;
# every toolchain command (buf, migrate, gofumpt, golangci-lint, go,
# the seed binary, the integration suites) runs inside the
# `banka-tools` image declared in docker/Dockerfile.tools, brought up
# as the `tools` service in docker-compose.yml. `make HOST=1 <target>`
# bypasses the container and invokes the host toolchain instead — for
# devs who already have go/buf/migrate installed locally and want
# faster iteration.

SHELL          := /bin/bash
.SHELLFLAGS    := -eu -o pipefail -c
.DEFAULT_GOAL  := help

SERVICES            := bank exchange gateway notification trading user
COMPOSE             := docker compose

# Match the host UID/GID into the tools image so any file the
# container writes into the bind-mounted repo (gen/, go.sum, bin/)
# is owned by the developer, not root.
export HOST_UID := $(shell id -u)
export HOST_GID := $(shell id -g)

# By default, run toolchain commands inside the `tools` container.
# `make HOST=1 <target>` flips this off and uses the host PATH.
ifdef HOST
TOOLS    :=
TOOLS_SH := bash -c
else
TOOLS    := $(COMPOSE) run --rm tools
TOOLS_SH := $(TOOLS) bash -c
endif

.PHONY: help
help: ## List available targets
	@awk 'BEGIN {FS = ":.*##"; printf "Available targets:\n"} \
		/^[a-zA-Z0-9_.-]+:.*##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 }' \
		$(MAKEFILE_LIST)

.PHONY: tools-image
tools-image: ## Build the toolchain image (auto-built on first compose use)
	$(COMPOSE) build tools

.PHONY: proto
proto: ## Regenerate proto stubs into gen/
	$(TOOLS) buf generate

.PHONY: build
build: ## Build all service binaries into bin/
	@$(TOOLS_SH) 'for svc in $(SERVICES); do echo "==> building $$svc"; (cd services/$$svc && go build -trimpath -o ../../bin/$$svc ./cmd/$$svc); done'

.PHONY: up
up: proto ## Bring up the full stack (infra + migrate + every service)
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
	$(TOOLS) bash scripts/db/migrate.sh up

.PHONY: migrate-down
migrate-down: N ?= 1
migrate-down: ## Roll back N migrations across all services (N=1 default)
	$(TOOLS) bash scripts/db/migrate.sh down $(N)

.PHONY: migrate-create
migrate-create: ## Create a new migration pair (SVC=… NAME=…)
	@if [ -z "$(SVC)" ] || [ -z "$(NAME)" ]; then \
		echo "usage: make migrate-create SVC=<service> NAME=<migration-name>" >&2; \
		exit 2; \
	fi
	$(TOOLS) bash scripts/db/migrate.sh create $(SVC) $(NAME)

.PHONY: seed
seed: ## Load development fixtures
	$(TOOLS) bash scripts/db/seed.sh

.PHONY: nuke
nuke: ## Wipe everything and bootstrap (down-v + up + seed)
	$(MAKE) down-v
	$(MAKE) up
	$(MAKE) seed

# Modules tested individually; each has its own go.mod.
TEST_MODULES := pkg services/bank services/exchange services/gateway \
                services/notification services/trading services/user

.PHONY: test
test: ## Unit tests across every module (race detector on)
	@$(TOOLS_SH) 'for mod in $(TEST_MODULES); do echo "==> test $$mod"; (cd $$mod && go test -race ./...); done'

.PHONY: test-integration
test-integration: ## Integration tests (assumes `make up` running)
	@$(TOOLS_SH) 'for mod in services/user services/bank services/trading; do echo "==> integration $$mod"; (cd $$mod && go test -race -tags=integration ./...); done'

.PHONY: lint
lint: ## golangci-lint
	$(TOOLS) golangci-lint run

.PHONY: fmt
fmt: ## Format Go source
	$(TOOLS) gofumpt -w .

.PHONY: fmt-check
fmt-check: ## Fail if any file would be reformatted
	@$(TOOLS_SH) 'out=$$(gofumpt -l .); if [ -n "$$out" ]; then gofumpt -d .; exit 1; fi'

.PHONY: tidy
tidy: ## go mod tidy across every module
	@$(TOOLS_SH) 'for mod in $(TEST_MODULES); do echo "==> tidy $$mod"; (cd $$mod && go mod tidy); done'

.PHONY: clean
clean: ## Remove build outputs and generated stubs
	rm -rf bin gen
