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

`task seed` is idempotent and unconditionally plants the bootstrap
admin, the demo client, and (when the bank schema is migrated) a
small c2 dataset hung off the client: one company, three accounts
(RSD personal / EUR personal / RSD business), an active card, and an
approved cash loan with one paid + one upcoming installment. On a
c1-only stack the c2 layer is skipped silently.

| Kind   | Email                | Password     |
|--------|----------------------|--------------|
| Admin  | `admin@banka.local`  | `Admin123!`  |
| Client | `klijent@banka.local`| `Klijent123!`|

Override via `SEED_ADMIN_EMAIL` / `SEED_ADMIN_PASSWORD` /
`SEED_CLIENT_EMAIL` / `SEED_CLIENT_PASSWORD`. Passwords must satisfy
the spec p.10 policy (≥8/≤32 chars, ≥2 digits, ≥1 upper, ≥1 lower).

The gateway listens on `GATEWAY_HTTP_PORT` (default `8080`); each service
exposes its gRPC port plus an HTTP probe port (`/healthz`, `/readyz`).
