-- c4 (spec p.64-76) reservation primitive — generalizes c3's
-- "debit-then-credit" SettleTrade pattern to a two-phase
-- ReserveFunds → CommitReservedFunds (or ReleaseFunds) shape used by
-- OTC premium settlement, OTC exercise, and fund invest/withdraw. The
-- SAGA orchestrator in trading drives these.
--
-- Why a separate table (rather than re-using a `pending` state on
-- transactions): the held-side debit moves `available_balance` only
-- (not `balance`) so the client still sees the money in the account
-- summary until the commit completes. The transactions ledger remains
-- a record of *settled* moves; reservations are pending state that
-- shouldn't appear in pregled plaćanja until they finalize.
--
-- Idempotency: `unique (op_id)` enforces a single reservation per SAGA
-- step. The orchestrator's deterministic op_id (uuid.NewSHA1 of
-- (transaction_id, step_name)) means a retry hits the unique constraint
-- and the service maps it to "already reserved, return the existing row".
create table "bank".reservations (
    id           uuid primary key default gen_random_uuid(),
    account_id   uuid not null references "bank".accounts(id) on delete restrict,
    op_id        uuid not null,
    amount       numeric(20,4) not null check (amount > 0),
    currency     text not null,
    state        text not null check (state in ('held','committed','released')),
    op_kind      text not null,
    held_at      timestamptz not null default now(),
    settled_at   timestamptz,
    unique (op_id)
);

-- Active-reservation lookups (release path scans by op_id directly; this
-- index covers the per-account summary the FE renders on the account
-- detail page for c4 — "Rezervisana sredstva" line on spec p.20 is
-- already populated by available_balance, but the breakdown table the
-- c4 flow needs lives off this index).
create index reservations_held_acct
    on "bank".reservations (account_id)
    where state = 'held';

-- Extend transactions.op_kind so committed reservation legs (and the
-- TransferBetweenClients wrapper) can tag their rows with c4
-- categories. Same shape as 0008/0009/0010 — drop the existing check,
-- recreate with the new union.
alter table "bank".transactions
  drop constraint transactions_op_kind_check,
  add constraint transactions_op_kind_check
    check (op_kind in (
      'payment','transfer','exchange','fee',
      'loan_disbursement','loan_installment',
      'trade','tax','forex_fill',
      'otc_premium','otc_exercise','fund_invest','fund_withdraw'
    ));
