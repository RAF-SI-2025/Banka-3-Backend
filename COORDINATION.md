# Parallel-session coordination

Multiple Claude sessions work this repo concurrently. Each declares the
files/areas it owns. Re-read before starting work; keep this current.

## Session A — inter-bank / cross-bank OTC (celina 5) — ✅ DONE / MERGED
Landed on main: per-partner outbound key (#304), buyerAccountNumber (#307),
cross-bank OTC settlement (#313, trading migration 0021). No longer active.

## Session B — todoSpec feature batch
Delivered (merged): C1 lockout/DOB/audit, C2 in-app + scheduled payments,
C3 price-alerts/watchlist/order-notifs/order-history/DCA/dividends/audit-log,
C4 negotiation-history/fund-stats/partial-exercise/OTC-matching/pre-expiry-warn/
fund-dividends, mobile, + primitives. Migrations used: trading 0019,0020,0022,
0023; bank 0015 (scheduled-pay),0016 (dividend-settle); user 0004,0005.
**Currently building: C3 forex forwards** — claims trading migration **0024**,
bank migration **0017**, new files `services/{trading,bank}/internal/**/forex_forward*.go`,
`proto/{trading,bank}/v1/*.proto` (ForexForward* RPCs), a scheduler job.
Forward settlement is a DIRECT bank settle (NOT a saga) — Session B will NOT
touch the saga package.
Delivery: feature branches → PRs (never direct-push main).

## Session C — SAGA testing
Owns (Session B will NOT edit):
- `services/trading/internal/saga/**` (orchestrator + saga_*_test.go)
- `services/trading/internal/service/*_saga.go` (otc_exercise_saga, fund_invest_saga,
  external_otc_*_saga step handlers)
- `services/trading/internal/store/saga.go`
- any `saga_executions` migration / SAGA fault-injection

**Status (2026-06-06): ✅ MERGED — Session C done.** Force-fail integration
suite (`services/trading/internal/service/saga_exercise_faultinject_integration_test.go`)
landed on main via **PR #318**; the SAGA_test.pdf conformance work
(compensated terminal + step_no/log, trading migration **0017**,
CompensateOnTransient on otc_exercise) merged earlier. No new migration.
Cypress saga-spec ordering also hardened on the frontend via **PR #286**
(`after()` reseed in `c4-saga-scenarios` so it leaves a clean backend for
the persistent soak). Live-verified end to end (saga unit + integration +
both cypress saga suites green).

⚠️ **Cross-lane note (Session B):** PR #318 also touched the shared
`services/trading/internal/service/integration_test.go` — it added
`stubReservations.SettleDividend` + `.ListClientAccounts`. The merged
quarterly-dividend feature put those two methods on the `BankReservations`
interface (dividends.go) but never updated the test double, so
`make test-integration` for trading was failing to **compile** on main. The
stubs are minimal (SettleDividend credits the dest; ListClientAccounts
returns none). If the dividends owner wants a fuller stub, replace mine —
no behavioural dependency.

## Rules
- Pre-assign migration numbers. Next free: trading after **0024**, bank after
  **0017**, user after 0005. Coordinate SAGA-related migrations with Session C.
- ff-only / PR merges; never force-push `main`.
- gen/ rebase conflict: resolve the `.proto` (keep both), then `make proto` to
  regenerate gen/ — never hand-merge generated files.
- CI runs `gofmt -l` + `buf format` that local `make build` does NOT — run
  `gofmt -w` on changed Go + `buf format -w proto` before pushing.
