# Parallel-session coordination

Two Claude sessions work this repo concurrently. To avoid collisions, each
declares the files/areas it owns. Re-read before starting work; keep this
current.

## Session A — inter-bank / cross-bank OTC (celina 5)
Owns (do not edit from Session B):
- `services/trading/internal/external/interbank/**`
- `services/trading/internal/service/external_otc*.go`
- `services/trading/internal/app/app.go` (inter-bank client wiring)
- `.env.example` (inter-bank vars)
- `proto/bank/v1/interbank_2pc.proto` + bank inter-bank 2PC files
Branches: `feat/interbank-*`.

## Session B — todoSpec feature batch (this session)
Owns / currently building:
- C2 scheduled payments: `services/bank/internal/{store,service,server}/scheduled_payments*.go`,
  `proto/bank/v1/bank.proto` (SchedulePayment* RPCs), bank migration **0015**,
  `services/scheduler/internal/app/jobs.go` (a `bank-scheduled-payments` job).
- Frontend: `Banka-3-Frontend` scheduling UI under `placanja`.
Branches: `feat/...` per feature, delivered as PRs (never direct-push `main`).

## Rules
- Pre-assign migration numbers (bank next free: after 0015; trading after 0020).
- ff-only / PR merges; never force-push `main`.
- On a `gen/` rebase conflict: resolve the `.proto` (keep both additions), then
  `make proto` to regenerate `gen/` — do NOT hand-merge generated files.
- CI runs `gofmt -l` + `buf format` checks that local `make build` does not —
  run `gofmt -w` on changed Go + `buf format -w proto` before pushing.
