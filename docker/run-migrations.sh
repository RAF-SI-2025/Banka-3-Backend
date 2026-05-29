#!/usr/bin/env bash
# Apply every service's migrations against DATABASE_URL in dependency
# order. Each service owns its own schema; golang-migrate tracks
# applied versions per-schema via `x-migrations-table` so the five
# version tables don't clash inside the one database.
set -euo pipefail

: "${DATABASE_URL:?DATABASE_URL must be set}"

services=(user exchange bank notification trading)

for svc in "${services[@]}"; do
  echo "==> migrating $svc"
  # x-migrations-table keeps each service's version pointer in its own
  # schema's migrations table (e.g. user.schema_migrations).
  migrate \
    -path "/migrations/$svc" \
    -database "${DATABASE_URL}&x-migrations-table=${svc}.schema_migrations" \
    up
done

echo "==> all migrations applied"
