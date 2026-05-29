#!/usr/bin/env bash
# Regenerate proto stubs into gen/. Equivalent to `make proto`; kept here
# so CI / pre-commit can call it without Make.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

buf generate
