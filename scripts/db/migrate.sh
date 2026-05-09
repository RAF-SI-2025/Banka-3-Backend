#!/usr/bin/env bash
# Apply golang-migrate migrations across every service in dependency order.
# Usage:
#   scripts/db/migrate.sh up
#   scripts/db/migrate.sh down 1
#   scripts/db/migrate.sh create <svc> <name>
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

# shellcheck disable=SC1091
[[ -f .env ]] && source .env

DB_BASE="postgres://${POSTGRES_USER:?}:${POSTGRES_PASSWORD:?}@localhost:${POSTGRES_PORT:-5432}/${POSTGRES_DB:?}?sslmode=disable"

# Order: user → exchange → bank → trading → notification → gateway
# Gateway has no migrations. Each service tracks version in its own
# schema_migrations table — without `x-migrations-table` they'd all
# share the default table and clobber each other's version state on
# the second `migrate up` of the day.
SERVICES=(user exchange bank trading notification)

# svc_db_url builds the connection URL with a per-service migration
# tracking table. golang-migrate honours this via the URL query string.
svc_db_url() {
  local svc="$1"
  echo "${DB_BASE}&x-migrations-table=${svc}_schema_migrations"
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
