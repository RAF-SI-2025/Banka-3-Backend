#!/usr/bin/env bash
# Apply golang-migrate migrations across every service in dependency order.
# Usage:
#   scripts/db/migrate.sh up
#   scripts/db/migrate.sh down 1
#   scripts/db/migrate.sh create <svc> <name>
#
# DATABASE_URL takes precedence — that's what the docker-compose
# `migrate` one-shot service sets so it can reach the in-network
# postgres at `postgres:5432`. Without it, the URL is built from
# POSTGRES_* (sourced from .env) against localhost, matching the
# original host-driven `make migrate` behaviour.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

if [[ -z "${DATABASE_URL:-}" ]]; then
  # shellcheck disable=SC1091
  [[ -f .env ]] && source .env
  DATABASE_URL="postgres://${POSTGRES_USER:?}:${POSTGRES_PASSWORD:?}@localhost:${POSTGRES_PORT:-5432}/${POSTGRES_DB:?}?sslmode=disable"
fi

# Order: user → exchange → bank → trading → notification → gateway.
# Gateway has no migrations. Each service tracks version in its own
# schema_migrations table — without `x-migrations-table` they'd all
# share the default table and clobber each other's version state on
# the second `migrate up` of the day.
SERVICES=(user exchange bank trading notification)

# svc_db_url builds the connection URL with a per-service migration
# tracking table. golang-migrate honours this via the URL query string.
svc_db_url() {
  local svc="$1"
  echo "${DATABASE_URL}&x-migrations-table=${svc}_schema_migrations"
}

cmd="${1:-up}"
shift || true

case "$cmd" in
  up)
    for svc in "${SERVICES[@]}"; do
      echo "==> migrating $svc"
      migrate -path "services/$svc/migrations" -database "$(svc_db_url "$svc")" up
    done
    ;;
  down)
    n="${1:-1}"
    for svc in "${SERVICES[@]}"; do
      echo "==> rolling back $svc by $n"
      migrate -path "services/$svc/migrations" -database "$(svc_db_url "$svc")" down "$n"
    done
    ;;
  create)
    svc="${1:?service required}"
    name="${2:?name required}"
    migrate create -ext sql -dir "services/$svc/migrations" -seq "$name"
    ;;
  *)
    echo "unknown subcommand: $cmd" >&2
    exit 1
    ;;
esac
