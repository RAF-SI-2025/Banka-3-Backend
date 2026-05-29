-- c4 PR3 — Investment funds (spec p.71-76).
--
-- Spec model
-- ==========
-- An investment fund is a pre-tax pooled vehicle that holds a basket of
-- securities. Clients (and the bank itself) buy units; the fund's
-- manager (a supervisor) places orders on the fund's behalf using the
-- fund's bank account as the settlement leg.
--
-- Unit-pricing model (FUND-7)
-- ---------------------------
-- Spec p.72 mentions "ProcenatFonda" (share of the fund) but is silent
-- on how to attribute it across investments at different times. We
-- adopt the standard mutual-fund unit-share mechanic: each invested RSD
-- buys `amount_rsd / unit_price` units, where unit_price =
-- total_value_rsd / total_units. First investment defines the unit at
-- 1 RSD (mints amount_rsd units). Existing holders aren't diluted; new
-- investors don't get a free ride on prior appreciation.
--
-- Taxation (EDGE-3)
-- -----------------
-- Fund-actor orders write portfolio_holdings but DO NOT write
-- realized_gains rows — funds aren't taxable entities. The tax bites
-- the client at withdrawal: a realized_gains row with proceeds =
-- withdrawn RSD and cost_basis = proportional total_invested_rsd. The
-- bank's own fund stake (client_id = BankAsClientOwnerID) is taxed the
-- same way at the bank's "withdrawal" boundary.

create table "trading".investment_funds (
    id                      uuid primary key default gen_random_uuid(),
    name                    text not null unique,
    description             text not null default '',
    manager_user_id         uuid not null,                           -- user.users.id (supervisor)
    bank_account_id         uuid not null unique,                    -- bank.accounts.id (kind='fund', currency='RSD')
    minimum_contribution    numeric(20,4) not null check (minimum_contribution >= 0),
    total_units             numeric(28,8) not null default 0 check (total_units >= 0),
    status                  text not null default 'active' check (status in ('active','closed')),
    created_at              timestamptz not null default now(),
    updated_at              timestamptz not null default now()
);

create index investment_funds_manager_idx on "trading".investment_funds (manager_user_id);
create index investment_funds_status_idx  on "trading".investment_funds (status);

-- Per-client position in a fund. client_id may be a real client UUID or
-- the BankAsClientOwnerID sentinel (spec p.75 Napomena 2). units carry
-- the mutual-fund unit count; total_invested_rsd is the running sum of
-- cash invested (cost basis for the EDGE-3 tax row at withdrawal time).
create table "trading".client_fund_positions (
    id                      uuid primary key default gen_random_uuid(),
    fund_id                 uuid not null references "trading".investment_funds(id) on delete restrict,
    client_id               uuid not null,                           -- user.users.id OR BankAsClientOwnerID
    units                   numeric(28,8) not null default 0 check (units >= 0),
    total_invested_rsd      numeric(20,4) not null default 0 check (total_invested_rsd >= 0),
    created_at              timestamptz not null default now(),
    updated_at              timestamptz not null default now(),
    unique (fund_id, client_id)
);

create index client_fund_positions_client_idx on "trading".client_fund_positions (client_id);

-- Audit log for invest/withdraw transactions. is_inflow=true on invest,
-- false on withdraw. status flips pending → completed when the SAGA
-- finishes (or failed on terminal compensation). The illiquid withdraw
-- path stays in 'pending' while auto-liquidation orders settle, so the
-- FE can show "Likvidacija u toku" on a separate filter.
create table "trading".client_fund_transactions (
    id                          uuid primary key default gen_random_uuid(),
    fund_id                     uuid not null references "trading".investment_funds(id) on delete restrict,
    client_id                   uuid not null,
    initiator_employee_id       uuid,                                -- non-null on supervisor-initiated
    amount_rsd                  numeric(20,4) not null check (amount_rsd > 0),
    units_delta                 numeric(28,8) not null,              -- positive on invest, negative on withdraw
    source_or_dest_account_id   uuid not null,                       -- bank.accounts.id
    is_inflow                   boolean not null,
    status                      text not null default 'pending'
                                check (status in ('pending','completed','failed')),
    saga_id                     uuid,                                -- saga_executions.transaction_id
    failure_reason              text,
    created_at                  timestamptz not null default now(),
    updated_at                  timestamptz not null default now()
);

create index client_fund_transactions_fund_idx   on "trading".client_fund_transactions (fund_id);
create index client_fund_transactions_client_idx on "trading".client_fund_transactions (client_id, created_at);
create index client_fund_transactions_pending_idx
    on "trading".client_fund_transactions (status, created_at)
    where status = 'pending';

-- Daily snapshot of fund liquidity + holdings value (FUND-6, spec p.74
-- "Performance"). Cron writes one row per active fund per day. FE chart
-- reads the time series.
create table "trading".fund_performance_snapshots (
    fund_id             uuid not null references "trading".investment_funds(id) on delete cascade,
    snapshot_at         timestamptz not null,
    liquid_rsd          numeric(20,4) not null,
    holdings_value_rsd  numeric(20,4) not null,
    primary key (fund_id, snapshot_at)
);

-- Funds-as-holding-owner: extend portfolio_holdings.user_kind to admit
-- 'fund'. Fund-actor orders write rows keyed by (user_id=fund.id,
-- user_kind='fund') so the fund's basket is queryable through the same
-- portfolio store as client/employee positions.
alter table "trading".portfolio_holdings
  drop constraint portfolio_holdings_user_kind_check;
alter table "trading".portfolio_holdings
  add constraint portfolio_holdings_user_kind_check
  check (user_kind in ('client','employee','fund'));
