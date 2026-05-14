# Banka-3-Backend

Go monorepo. Six services (`user`, `bank`, `trading`, `exchange`,
`notification`, `gateway`) talking gRPC, Postgres + Redis, deployed on
Kubernetes.

See `CLAUDE.md` for architecture, conventions, and roadmap. Quick start:

```bash
cp .env.example .env
make proto
make up
make migrate
make seed
```

`make help` shows everything available.

## Seeded credentials

`make seed` is idempotent and unconditionally plants the bootstrap
admin, the demo client, and (when the bank schema is migrated) a
small c2 dataset hung off the client: one company, three accounts
(RSD personal / EUR personal / RSD business), an active card, and an
approved cash loan with one paid + one upcoming installment. On a
c1-only stack the c2 layer is skipped silently.

| Kind                | Email                     | Password         |
|---------------------|---------------------------|------------------|
| Admin               | `admin@banka.local`       | `Admin123!`      |
| Banking employee    | `zaposleni@banka.local`   | `Zaposleni123!`  |
| Actuary agent       | `aktuar@banka.local`      | `Aktuar123!`     |
| Actuary supervisor  | `supervizor@banka.local`  | `Supervizor123!` |
| Client              | `klijent@banka.local`     | `Klijent123!`    |
| Second client       | `klijent2@banka.local`    | `Klijent123!`    |

Override via `SEED_<ROLE>_EMAIL` / `SEED_<ROLE>_PASSWORD`
(`ADMIN`, `EMPLOYEE`, `ACTUARY`, `SUPERVISOR`, `CLIENT`, `CLIENT2`).
Passwords must satisfy the spec p.10 policy (≥8/≤32 chars, ≥2 digits,
≥1 upper, ≥1 lower).

Roles in brief: `zaposleni` is the c2 banking agent (no trading
permissions); `aktuar` carries `actuary` + `actuary.agent` and a
trading `actuary_info` row with a 200 000 RSD daily limit;
`supervizor` carries `actuary` + `actuary.supervisor` + `trading.margin`
and approves agent-side orders.

The gateway listens on `GATEWAY_HTTP_PORT` (default `8080`); each service
exposes its gRPC port plus an HTTP probe port (`/healthz`, `/readyz`).
