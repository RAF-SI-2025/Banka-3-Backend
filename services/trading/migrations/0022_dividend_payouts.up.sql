-- Dividend payouts (todoSpec C3 S54-S59).
--
-- The quarterly cron (last business day of each quarter) credits every
-- stock holder a dividend = shares × price × (dividend_yield / 4). Each
-- payout is recorded here so the portfolio detail view can render the
-- per-position dividend history (S59: dates + amounts).
--
-- gross_amount + currency are the dividend in the security's listing
-- currency; account_id is the bank account that was actually credited
-- (the purchase account, the holder's default-currency account, or an
-- RSD account when no same-currency account exists — S54/S55/S56).
-- tax_rsd is the 15% capital-gains tax owed on this payout for client
-- holders (S57); it is "0" for actuary "in the name of the bank"
-- holdings, which route to Profit Banke untaxed (S58).
--
-- The per-position history view keys off (user_id, user_kind,
-- security_id); the cron's idempotency keys off op_id (the deterministic
-- bank settle op).

create table "trading".dividend_payouts (
    id           uuid primary key default gen_random_uuid(),
    user_id      text        not null,
    user_kind    text        not null,
    security_id  uuid        not null,
    quantity     integer     not null,
    price        numeric(20, 4) not null,
    gross_amount numeric(20, 4) not null,
    currency     text        not null,
    account_id   text        not null,
    tax_rsd      numeric(20, 4) not null default 0,
    op_id        uuid        not null,
    status       text        not null default 'paid' check (status in ('paid', 'failed')),
    paid_at      timestamptz,
    created_at   timestamptz not null default now()
);

-- One payout per (holding, op) — the deterministic op_id makes a retry
-- after a partial cron failure a no-op rather than a double credit.
create unique index dividend_payouts_op_id_idx on "trading".dividend_payouts (op_id);

-- Per-position history view (S59) and per-user list.
create index dividend_payouts_position_idx
    on "trading".dividend_payouts (user_id, user_kind, security_id);
