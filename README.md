# Banka-3-Backend

Go monorepo. Six services (`user`, `bank`, `trading`, `exchange`,
`notification`, `gateway`) talking gRPC, Postgres + Redis, deployed on
Kubernetes.

See `CLAUDE.md` for architecture, conventions, and roadmap. Quick start:

```bash
cp .env.example .env
task proto
task up
task migrate
task seed
```

`task --list` shows everything available.

## Seeded credentials

`task seed` is idempotent and writes a bootstrap admin. Pass
`SEED_CLIENT=true` to also plant a demo client (used by the live
Cypress suite).

| Kind   | Email                | Password     | Created when             |
|--------|----------------------|--------------|--------------------------|
| Admin  | `admin@banka.local`  | `Admin123!`  | always                   |
| Client | `klijent@banka.local`| `Klijent123!`| `SEED_CLIENT=true` only  |

Override via `SEED_ADMIN_EMAIL` / `SEED_ADMIN_PASSWORD` /
`SEED_CLIENT_EMAIL` / `SEED_CLIENT_PASSWORD`. Passwords must satisfy
the spec p.10 policy (≥8/≤32 chars, ≥2 digits, ≥1 upper, ≥1 lower).

The gateway listens on `GATEWAY_HTTP_PORT` (default `8080`); each service
exposes its gRPC port plus an HTTP probe port (`/healthz`, `/readyz`).
