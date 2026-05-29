#!/usr/bin/env bash
# Load development fixtures into the local Postgres.
#
# Currently plants a bootstrap admin so the system can be brought up
# from zero (the spec says only an admin can create employees). Override
# credentials via SEED_ADMIN_{EMAIL,USERNAME,PASSWORD} env vars.
#
# Idempotent: a second run no-ops if any admin already exists.
set -euo pipefail

# Resolve repo root from this script's location so it works regardless
# of the caller's cwd.
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"

if [[ -z "${DATABASE_URL:-}" ]]; then
    : "${POSTGRES_USER:=banka}"
    : "${POSTGRES_PASSWORD:=banka}"
    : "${POSTGRES_DB:=banka}"
    : "${POSTGRES_PORT:=5432}"
    export DATABASE_URL="postgres://${POSTGRES_USER}:${POSTGRES_PASSWORD}@localhost:${POSTGRES_PORT}/${POSTGRES_DB}?sslmode=disable"
fi

cd "${ROOT}/services/user"
# Run inside the per-service module rather than the workspace so we
# don't touch go.work.sum (which may be root-owned after a docker build).
exec env GOWORK=off go run ./cmd/seed
