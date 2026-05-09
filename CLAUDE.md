# Banka-3-Backend

Go monorepo for the Banka 3 backend. Six gRPC services + one HTTP gateway,
shared library code in `pkg/`, proto contracts in `proto/`. Deployed on
Kubernetes; app code is k8s-ready (probes, graceful shutdown, structured
logs, env config).

The top-level memory at `/home/user/si/CLAUDE.md` has the architecture
overview and celina roadmap. This file is the backend-specific working
memory.

## Layout

```
.
├── go.work                    # workspace declaration
├── docker-compose.yml         # local dev: postgres + redis + all services
├── Taskfile.yml               # canonical commands (go-task)
├── flake.nix                  # nix dev shell (go, protoc, buf, go-task, …)
├── .env.example
├── docker/Dockerfile          # one image, $SERVICE arg picks the binary
├── buf.yaml / buf.gen.yaml    # proto codegen via buf
├── proto/<svc>/v1/*.proto     # service contracts (grpc-gateway annotated)
├── gen/proto/                 # generated stubs (gitignored, run `make proto`)
├── pkg/                       # shared library code (single Go module)
│   ├── logger/                # slog JSON logger
│   ├── probes/                # /healthz + /readyz HTTP server
│   ├── config/                # env var helpers, no flags
│   ├── postgres/              # pgx pool helper
│   ├── redis/                 # redis client helper
│   ├── grpcserver/            # gRPC server with interceptors
│   └── shutdown/              # signal handling + graceful shutdown
├── services/<svc>/            # one Go module per service
│   ├── cmd/<svc>/main.go      # entrypoint
│   ├── internal/
│   │   ├── domain/            # entities, value objects, no I/O
│   │   ├── service/           # business logic, depends on store interface
│   │   ├── store/             # postgres queries (pgx, hand-written)
│   │   ├── server/            # gRPC handlers, depend on service
│   │   └── config/            # service-specific env config
│   └── migrations/            # service-owned schema (golang-migrate format)
└── scripts/
    ├── proto/generate.sh      # invoked by `make proto`
    └── db/migrate.sh          # invoked by `make migrate`
```

Generated proto stubs go into `gen/proto/<svc>v1/`. Don't commit them —
`task proto` regenerates them and CI verifies they're up-to-date.

## Services

| Service | Owns | Talks to |
|---|---|---|
| `user` | employees, clients, roles, permissions, JWT, password reset, activation tokens | notification |
| `bank` | accounts (checking + foreign), companies, cards, payments, transfers, exchange, loans, FX rates, bank's own accounts | exchange, notification |
| `trading` | listings, orders, portfolio, OTC, investment funds, capital-gains tax, SAGA orchestrator | bank, exchange, notification |
| `exchange` | exchange catalog (NYSE etc), trading hours toggle, FX rate feed | — |
| `notification` | sends emails (activation, blocked card, late installment, OTC counter-offer, …) | — |
| `gateway` | only public surface; HTTP/JSON ↔ gRPC; authn middleware; inter-bank REST endpoints | all |

Inter-service traffic is gRPC over the cluster network. Services trust the
gateway: the gateway authenticates JWT and forwards `user_id` + permissions
in gRPC metadata. Services check permissions, not roles.

## Proto + grpc-gateway

Conventions:

- One package per service per version: `proto/user/v1/user.proto` →
  `package banka.user.v1;`.
- Use `google.api.http` annotations for REST mapping. Generated REST
  routes follow `/api/v1/<resource>/...`.
- Long-running ops use server-streaming RPCs, not polling.
- Field numbers are forever — never reuse, append-only.
- `validate.proto` (buf-validate) for field validation; the gateway
  rejects bad payloads before they hit gRPC.

Codegen happens via `buf` (see `buf.gen.yaml`). Generated artifacts:

- `*.pb.go` — Go structs + gRPC clients/servers
- `*.pb.gw.go` — REST gateway
- `*.swagger.json` — OpenAPI document, copied to the frontend repo

## Database

One Postgres instance, one schema per service:

| Schema | Service | Examples |
|---|---|---|
| `user` | user | `user.users`, `user.refresh_tokens`, `user.permissions` |
| `bank` | bank | `bank.accounts`, `bank.cards`, `bank.payments`, `bank.loans` |
| `trading` | trading | `trading.listings`, `trading.orders`, `trading.saga_executions` |
| `exchange` | exchange | `exchange.exchanges`, `exchange.fx_rates` |
| `notification` | notification | `notification.outbox` |

Migrations are owned per-service in `services/<svc>/migrations/` using
golang-migrate's `NNNN_name.up.sql` / `.down.sql` convention. `task migrate`
runs all pending migrations across all services in dependency order.

Cross-schema joins are forbidden by convention — services own their data.
If you need data from another service, call its gRPC API.

Queries are hand-written with `pgx` (no ORM). Each `internal/store/*.go`
file groups queries by aggregate. Use `pgx.Pool`, parameterize everything,
use `RETURNING` for inserted IDs.

## SAGA framework (`trading` service)

Each saga is a state machine persisted to `trading.saga_executions`:

```sql
create table trading.saga_executions (
  transaction_id uuid primary key,
  saga_type      text not null,           -- 'otc_settlement', 'cross_bank_payment', …
  current_step   text not null,
  state          jsonb not null,          -- per-saga payload
  status         text not null,           -- 'running' | 'compensating' | 'completed' | 'failed'
  attempts       int not null default 0,
  last_error     text,
  created_at     timestamptz not null default now(),
  updated_at     timestamptz not null default now()
);
```

A saga is defined as a sequence of `Step{Name, Forward, Compensate}`. The
orchestrator runs forward steps in order, persisting state after each. On
failure, it runs compensations in reverse for completed steps. A
background worker scans for sagas stuck in `running` past a timeout and
retries them with exponential backoff.

Step handlers must be **idempotent** keyed by `(transaction_id, step_name)`
— at-least-once delivery is assumed. The bank service's reservation /
debit / credit operations all need an idempotency key column.

## Auth

- Access token: JWT, 15min TTL, signed HS256 (key in env). Carries
  `sub` (user_id), `permissions` (string array), `iat`, `exp`.
- Refresh token: opaque random 32 bytes, 7d TTL, stored hashed in
  `user.refresh_tokens`. Delivered to FE in an `httpOnly; Secure;
  SameSite=Strict` cookie scoped to `/api/v1/auth/refresh`.
- The gateway validates access tokens and forwards
  `x-user-id` + `x-permissions` metadata to gRPC services.
- Activation + password-reset tokens are stored in Redis with TTL
  (15 min for reset per spec).

## K8s readiness

Every service main does, in this order:

1. Parse env config (fail fast on missing required vars).
2. Initialize `slog.JSONHandler` writing to stdout. Level from
   `LOG_LEVEL` env (debug|info|warn|error, default info).
3. Open Postgres pool + Redis client. Mark `readyz` not ready until both
   ping successfully.
4. Start probe HTTP server on `PROBE_PORT` (default 8081):
   - `GET /healthz` — liveness, returns 200 if process is alive.
   - `GET /readyz` — readiness, returns 200 only when DB+Redis are up
     and gRPC server is accepting.
5. Start gRPC server on `GRPC_PORT` (default 50051) with interceptors:
   logging, recovery, metadata→context propagation.
6. Block on signal (`SIGINT`, `SIGTERM`). On signal: stop accepting
   new requests, drain in-flight (60s default), then close DB/Redis.

Use `pkg/probes`, `pkg/shutdown`, `pkg/grpcserver` — don't reinvent.

## Conventions

- **Errors**: return typed errors from service layer
  (`apperr.NotFound`, `apperr.Conflict`, `apperr.Validation`,
  `apperr.Permission`). gRPC interceptor maps them to status codes.
  Don't leak `pgx.ErrNoRows` past `internal/store`.
- **Logging**: `slog` only. Use `log.With("user_id", id, …)` early and
  pass the logger through context. No `fmt.Println`, no `log` stdlib.
- **Context**: every gRPC handler takes ctx; every store function takes
  ctx; every service method takes ctx. Cancel propagates.
- **Testing**: `_test.go` next to source. Integration tests against a
  real Postgres in `_integration_test.go` files (build tag
  `integration`); `make test-integration` brings up a throwaway
  Postgres via testcontainers.
- **Dependencies**: prefer stdlib. External deps need a reason. Locked
  in: `pgx/v5`, `redis/go-redis/v9`, `grpc-go`, `protobuf`,
  `grpc-gateway/v2`, `golang-migrate`, `golang-jwt`, `bufbuild/protovalidate-go`,
  `robfig/cron/v3`.
- **No god packages**. `internal/util/` is a smell — name the package
  by what it does (`internal/luhn`, `internal/iban`).

## Task targets

`task --list` shows everything; common ones:

```
task proto                  # buf generate → gen/proto/
task tidy                   # go mod tidy each module (populates go.sum)
task build                  # compile all services to bin/
task up                     # postgres + redis + every service
task down                   # tear down
task migrate                # apply migrations across all services
task migrate:create SVC=user NAME=add_index
task seed                   # load dev fixtures
task nuke                   # down -v + up + migrate + seed
task test                   # unit tests with race detector
task test:integration
task lint                   # golangci-lint
task fmt                    # gofumpt
```

## C1 + C2 status

c1 and c2 are feature-complete and verified end-to-end. See top-level
`/home/user/si/CLAUDE.md` "Verification status" section for the full
breakdown.

## C3 status (in progress)

**Foundation + catalog landed (2026-05-09):**

- `services/trading/migrations/0002_c3.up.sql` — full c3 schema
  (`actuary_info`, `exchanges`, `securities`, `listings`,
  `listing_daily_price_info`, `orders`, `order_executions`,
  `portfolio_holdings`, `realized_gains`, `saga_executions`).
  Polymorphic `securities` table with type-discriminated columns +
  per-type required check constraints.
- Permissions extended: `actuary.supervisor`, `actuary.agent`,
  `trading.client`, `trading.margin`. Role bundles
  `RoleEmployeeActuarySupervisor`, `RoleEmployeeActuaryAgent`, and
  the existing `RoleClientTrading` updated.
- Proto contract `proto/trading/v1/trading.proto` defines the full c3
  surface (actuary, exchanges, securities, listings, option chain,
  orders, portfolio, tax). Generated stubs live in
  `gen/proto/trading/v1/`.
- Trading service wired into `app.go` with daily 23:59 (Belgrade)
  used-limit reset cron. Gateway dials trading and registers the
  REST handler.
- Implemented + smoke-tested via curl through the gateway:
  - actuary CRUD (`GET/PUT /api/v1/actuaries/{id}`, `PATCH .../limit`,
    `POST .../used-limit/reset`, `PATCH .../need-approval`,
    `POST /api/v1/actuaries/reset-job`),
  - exchanges (`GET/PUT /api/v1/exchanges`, `PATCH /api/v1/exchanges/{mic}/override`),
  - securities (`PUT/GET /api/v1/securities`, `GET /api/v1/securities`,
    `GET /api/v1/securities/{id}`, `GET /api/v1/securities/{stock}/option-chain`),
  - listings (`PUT /api/v1/listings`, `GET/PUT /api/v1/listings`,
    `GET /api/v1/listings/{id}/history`).
- Unit tests: actuary auth gating, market-state resolver (override,
  weekday/weekend, after-hours window), HH:MM parsing, maintenance-
  margin formula per security type, option-chain strike-window
  filter, security validation, daily-cron next-occurrence logic.

**Spec edge cases handled in this slice:**
- Spec p.38 "Admin -> supervizor" — `requireSupervisor` accepts both.
- Spec p.38 "Supervizor nema limit" — supervisors are forced to
  `daily_limit=0, need_approval=false` on upsert; updating the limit
  on a supervisor row is a `FailedPrecondition`.
- Spec p.39 "dugme koje uključuje/isključuje vreme berze" — exchange
  rows carry a tri-state `override_open` (NULL=schedule / true=forced
  open / false=forced closed); the resolver short-circuits to the
  override when set.
- Spec p.46-48 derived-data formulas (maintenance margin per type,
  initial margin cost = 1.1 × maintenance margin) computed in
  `service.computeMaintenanceMargin`.
- Spec p.56 after-hours window — within 4h of close on a weekday
  (weekend returns IsOpen=false, IsAfterHours=false).
- Spec p.58 client visibility — clients can only list stocks +
  futures; the listings/securities endpoints filter forex and option
  rows out for client principals.
- Spec p.59 option-chain strike window — `filterStrikeWindow` returns
  the N rows above + N below + the at-the-money row.

**Still to land for c3:**
- Orders (create, list, approve/decline, cancel) — schema exists,
  proto exists, service/server/store TBD. 4 types × buy/sell × AON ×
  margin; approval routing for agents (`need_approval` flag + RSD
  limit cap).
- Order execution worker (partial fills, random sub-quantity, random
  interval per spec p.56 formula; STOP/STOP_LIMIT trigger detection;
  after-hours slow-down).
- Portfolio holdings (weighted-avg cost basis on buy fills, decrement
  on sell fills, public_count for c4 OTC).
- Capital-gains tax (per-sell realized_gain row in security currency
  + RSD-converted via menjačnica without commission; end-of-month
  cron debits 15% from acquisition account to state account).
- Bank-side `TradeMove` RPC (or reuse of `executeMoneyMove` via a new
  internal entry point) for trade settlement.
- Seed for c3: a few exchanges + sample stock/future/forex/option
  rows with listings so the FE has data to render.

**c1**: user service — auth (login/refresh/logout), employee CRUD,
activation, password reset, session_version revocation, JWT middleware
in gateway.

**c2**: bank service — companies + authorized persons; accounts (RSD
+ FX, personal + business) with per-spec maintenance fees, default
limits, and `UpdateAccountName` for spec p.20 rename popup; cards
(lifecycle + per-account limit; CVV digest is HMAC-SHA256 with
`BANK_CVV_PEPPER`, never argon2id); payments (same-currency + FX
through bank house, ASK on every leg per spec p.26); transfers;
menjačnica (quote + execute); payment recipients; loans (request →
approve → installment cron → variable-rate refresh). Bank emits
Serbian notifications via a `Notifier` interface (email through
`pkg/email`; `UserResolver` dials user-service GetClient with
internal admin metadata to fetch the recipient address).

**Verification primitive** (`pkg/verification`, gateway middleware):
spec p.11 verifikacioni-kod gates payments / transfers / limit
changes / card issuance. Redis-keyed, 6-digit, 5-min TTL, 3 wrong
attempts retire the record. Mobile app is c5; until then the gateway
returns the code in the request response so the FE can render it.

**Tests**: bank service ~50 (`integration` build tag for ~33 of them),
user service ~30 integration, gateway middleware suites (auth +
idempotency + verification), pkg/* unit suites (account, auth, card,
cvv, idempotency, loans, money, passwords, permissions, verification)
all green.

Next steps:
- Begin celina 3 (`trading` service: listings, orders, portfolio, OTC,
  capital-gains tax, SAGA orchestrator). Read spec section "Trgovina
  hartijama sa berze" before starting.

## Locked decisions for c1

- **Password hashing**: argon2id (OWASP default), parameters in
  `pkg/passwords`.
- **Email in dev**: notification service uses real SMTP if `SMTP_HOST`
  is set; otherwise it logs full email content to stdout. One code
  path, env-driven.
- **Session revocation**: JWT carries a `sv` (session_version) claim.
  Each user has a `session_version` int column. Gateway middleware
  reads `usv:{kind}:{id}` from Redis on every request and rejects
  tokens with stale `sv`. On deactivation: increment user's
  `session_version`, write to Redis, revoke refresh tokens.
- **Permissions** are dot-namespaced strings, frozen in
  `pkg/permissions/permissions.go`. C1 set:
  `admin`, `employee.read`, `employee.write`, `client.read`,
  `client.write`, `permission.grant`. Subsequent celine append, never
  rename.
- **Activation token TTL**: 24h.
- **Reset token TTL**: 15min (per spec).
- **Lockout policy**: not implemented. Spec p.10 marks it
  "za nadogradnju" — explicitly deferred. Don't add a counter without
  a spec change.
- **Card CVV hashing**: HMAC-SHA256 keyed by `BANK_CVV_PEPPER`
  (`pkg/cvv`). Argon2id is wrong here — the search space is 1000 keys,
  so per-guess work factor is meaningless. The pepper makes a stolen
  database alone insufficient to recover any CVV.
- **FX rate direction**: spec p.26 ("uvek prodajni kurs") — always use
  the ASK column on every leg of a conversion, even when the bank is
  buying foreign. The bank's profit comes from the commission, not
  the bid/ask spread. `services/bank/.../exchange_quote.go` and
  `loans.go` (bracket-lookup) follow this; the BID column the
  exchange service stores is reserved for future use.
- **Notification service**: c1 + c2 emit emails directly via
  `pkg/email` through service-local `Notifier` adapters (user service
  for activation / reset / profile change; bank service for card
  status / loan decisions / missed installments). The standalone
  `notification` service is still a stub. Centralizing all email
  through it is deferred until c4 OTC counter-offers — when there are
  ~10+ event types the duplication of Serbian email templating across
  services becomes annoying enough to justify the migration. When
  that happens, both services swap their `notifierAdapter` for a
  notification gRPC client; service layers don't change.

Each celina's spec edge cases are documented in the top-level
`/home/user/si/CLAUDE.md`. Re-read before starting work in that area.
