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
task proto              # buf generate → gen/proto/
task build              # compile all services to bin/
task up                 # docker compose up -d
task down               # docker compose down
task migrate            # apply migrations across all services
task migrate:create SVC=user NAME=add_index
task seed               # load dev fixtures
task nuke               # down -v + up + migrate + seed
task test               # unit tests with race detector
task test:integration
task lint               # golangci-lint
task fmt                # gofumpt
task tidy               # go mod tidy across every module
```

## What's not done yet

This branch is a scaffold. Working out from here, in order:

1. Wire `pkg/postgres`, `pkg/redis`, `pkg/probes`, `pkg/shutdown`,
   `pkg/grpcserver` so a service main is ~50 lines.
2. Bring up `user` service with auth (celina 1).
3. Migrate frontend onto generated OpenAPI client; smoke-test the
   login flow end-to-end.
4. `bank` service for accounts + payments (celina 2 starts).

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
- **Lockout policy**: out of scope for c1 (spec marks it
  "za nadogradnju"). Track failed attempts in Redis if added later.

Each celina's spec edge cases are documented in the top-level
`/home/user/si/CLAUDE.md`. Re-read before starting work in that area.
