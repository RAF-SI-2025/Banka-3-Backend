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

# -----------------------------------------------------------------------
# Cross-bank dev stack — a second Banka 3 instance (bank code 334) that
# acts as a real partner over the shared `banka` network. Used by the
# cypress interbank suite.
# -----------------------------------------------------------------------
PARTNER_COMPOSE := docker compose -f docker-compose.partner.yml -p banka-partner

.PHONY: up-partner
up-partner: ## Bring up the second Banka 3 stack (partner bank, code 334)
	$(PARTNER_COMPOSE) up -d --build

.PHONY: down-partner
down-partner: ## Tear down the partner stack (keeps volumes)
	$(PARTNER_COMPOSE) down

.PHONY: down-partner-v
down-partner-v: ## Tear down the partner stack + wipe its volumes
	$(PARTNER_COMPOSE) down -v

.PHONY: seed-partner
seed-partner: ## Seed the partner stack with the same dev fixtures as the main stack
	$(PARTNER_COMPOSE) run --rm migrate bash scripts/db/seed.sh

.PHONY: interbank-up
interbank-up: ## Bring up both stacks in cross-bank wiring mode
	$(MAKE) up
	$(MAKE) up-partner
	$(MAKE) seed
	$(MAKE) seed-partner
	$(COMPOSE) restart trading gateway

.PHONY: replica
replica: ## Bring up the Postgres read replica (BonusPartitionReplication). Bootstraps from the primary on first run.
	$(COMPOSE) --profile replica up -d postgres_replica
	@echo ""
	@echo "  Replica host:  banka-postgres_replica-1:5432 (inside Docker)"
	@echo "  Replica port:  $${POSTGRES_REPLICA_PORT:-5433} (on the host)"
	@echo "  Validate:      docker exec banka-postgres_replica-1 psql -U $${POSTGRES_USER:-banka} -d $${POSTGRES_DB:-banka} -c 'SELECT pg_is_in_recovery();'"

.PHONY: replica-down
replica-down: ## Stop the read replica (keeps the volume)
	$(COMPOSE) --profile replica stop postgres_replica

.PHONY: influxdb
influxdb: ## Bring up the InfluxDB market-data side-channel (BonusInfluxDB). Trading service mirrors daily prices when INFLUX_* env vars are set.
	$(COMPOSE) --profile influxdb up -d influxdb
	@echo ""
	@echo "  InfluxDB UI:   http://localhost:$${INFLUX_PORT:-8087}"
	@echo "  Set INFLUX_URL=http://influxdb:8086, INFLUX_TOKEN=…, INFLUX_ORG=banka, INFLUX_BUCKET=market-data on trading to enable the mirror."

.PHONY: influxdb-down
influxdb-down: ## Stop the InfluxDB service (keeps the volume)
	$(COMPOSE) --profile influxdb stop influxdb

.PHONY: observability
observability: ## Bring up the Prometheus + Grafana + Alertmanager observability stack (in addition to whatever services are already running)
	$(COMPOSE) --profile observability up -d --build prometheus grafana alertmanager discord_notifier
	@echo ""
	@echo "  Grafana:       http://localhost:$${GRAFANA_PORT:-3001} (admin/admin)"
	@echo "  Prometheus:    http://localhost:$${PROMETHEUS_PORT:-9090}"
	@echo "  Alertmanager:  http://localhost:$${ALERTMANAGER_PORT:-9093}"

.PHONY: observability-down
observability-down: ## Tear down the observability stack (keeps app services running)
	$(COMPOSE) --profile observability stop prometheus grafana alertmanager discord_notifier
	$(COMPOSE) --profile observability rm -f prometheus grafana alertmanager discord_notifier

.PHONY: interbank-down
interbank-down: ## Tear down both stacks
	$(MAKE) down-partner
	$(MAKE) down

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
