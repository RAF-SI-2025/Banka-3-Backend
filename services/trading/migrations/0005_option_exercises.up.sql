-- FE-4 (c3 audit). Spec p.61.d: actuaries may exercise an in-the-money
-- option before settlementDate. Mirrors the executeFill saga (BE-3):
--
--   1. INSERT a pending row (status='pending'); its UUID is the
--      deterministic op_id used for the bank settle.
--   2. Call bank.SettleTrade with that op_id; bank-side
--      (op_id, leg_index) unique constraint (bank.0011) makes a retry
--      idempotent.
--   3. UPDATE to status='settled', mutate the option holding + the
--      underlying holding (+ realized_gain on PUT) atomically.
--
-- A crash between (2) and (3) leaves a pending row; the FE retry that
-- carries the gateway's Idempotency-Key replays the cached response.
-- Without that key, the next call mints a fresh UUID and would
-- double-settle — option exercise is actuary-driven, low-frequency, so
-- we keep the exposure narrow and rely on the gateway dedup. The
-- pending row is the source of truth for ops who need to reconcile.

create table "trading".option_exercises (
    id                     uuid primary key default gen_random_uuid(),
    option_holding_id      uuid not null references "trading".portfolio_holdings(id),
    user_id                uuid not null,
    user_kind              text not null check (user_kind in ('client','employee')),
    option_security_id     uuid not null,
    underlying_security_id uuid not null,
    account_id             uuid not null,
    option_type            text not null check (option_type in ('call','put')),
    quantity               int  not null check (quantity > 0),
    contract_size          numeric(38,8) not null,
    strike_price           numeric(38,8) not null,
    notional_amt           numeric(38,8) not null,
    currency               text not null,
    bank_op_id             text,
    realized_gain_id       uuid,
    status                 text not null default 'pending'
        check (status in ('pending','settled')),
    created_at             timestamptz not null default now(),
    updated_at             timestamptz not null default now()
);

create index option_exercises_pending_idx
    on "trading".option_exercises (option_holding_id)
    where status = 'pending';
