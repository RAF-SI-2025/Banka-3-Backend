-- Celina 3: trading.
--
-- Single migration covering the full c3 surface so unrelated parts of
-- the service (catalog, orders, executions, portfolio, tax) can be
-- developed in parallel against a stable schema.
--
-- Design notes
-- ============
-- * `securities` is one polymorphic table indexed by `type`. Per spec
--   p.40-44 each instrument carries a different attribute bag — we
--   keep all type-discriminated columns nullable and lean on check
--   constraints to enforce per-type required fields. The alternative
--   (4 tables joined into a virtual one) blows up every query that
--   sorts/filters across types, which is the dominant access pattern
--   on the "Hartije od vrednosti" portal (spec p.58).
-- * `listings` carries the live price snapshot. Per spec p.45-48
--   options don't have listings (they read from the parent stock),
--   forex listings are optional (Idea 1). We allow both: securities
--   with no listing row use type-specific fallback fields (premium
--   for options) at the service layer.
-- * Cross-schema FKs to user.users / bank.accounts are forbidden by
--   the project convention; we store UUIDs and validate via gRPC
--   when needed.
-- * Money columns are numeric(20,4) to match the bank schema; rates
--   are numeric(20,8). pkg/money formats both consistently.

create extension if not exists "pgcrypto";

-- =====================================================================
-- Actuary info — extension of Employee (user.users).
-- Spec p.38 explicitly allows a separate ActuaryInfo table FK'd to
-- Employee; we put it in the trading schema so the trading service
-- owns its own data without cross-schema joins.
-- =====================================================================
create table "trading".actuary_info (
    employee_id      uuid primary key,                    -- user.users.id (employee)
    type             text not null check (type in ('supervisor', 'agent')),
    daily_limit      numeric(20,4) not null default 0     check (daily_limit >= 0),
    used_limit       numeric(20,4) not null default 0     check (used_limit  >= 0),
    need_approval    boolean       not null default false,
    created_at       timestamptz   not null default now(),
    updated_at       timestamptz   not null default now()
);

create index actuary_info_type_idx on "trading".actuary_info (type);

-- =====================================================================
-- Exchanges (catalog of stock/futures/forex venues).
-- Spec p.39: name, acronym, MIC, polity, currency, time-zone, hours.
-- Per spec, every exchange in the same country shares hours; we keep
-- the schedule per-row for simplicity (seeding can fan out same-zone
-- rows from a single source row trivially).
--
-- `override_open` is the spec p.39 admin toggle: NULL means "follow
-- the calendar"; TRUE/FALSE forces the venue open or closed regardless
-- of wall-clock. Used so dev / test can place orders outside trading
-- hours without a fake clock.
-- =====================================================================
create table "trading".exchanges (
    mic              text primary key,                    -- e.g. XNYS
    name             text not null,
    acronym          text not null,
    polity           text not null,
    currency         text not null check (currency in (
        'RSD','EUR','CHF','USD','GBP','JPY','CAD','AUD'
    )),
    timezone         text not null default 'UTC',         -- IANA tz id
    open_local       time not null default '09:30',
    close_local      time not null default '16:00',
    override_open    boolean,                              -- nullable: see comment above
    created_at       timestamptz not null default now(),
    updated_at       timestamptz not null default now()
);

-- =====================================================================
-- Securities — stocks, futures, forex pairs, options.
--
-- Polymorphic on `type`. Common columns first; per-type columns after.
-- The (ticker, type) unique pair lets us look up by either the bare
-- ticker (with a type filter from the FE) or by both for option chains.
-- =====================================================================
create table "trading".securities (
    id                  uuid primary key default gen_random_uuid(),
    ticker              text not null,
    name                text not null,
    type                text not null check (type in ('stock','future','forex','option')),
    exchange_mic        text references "trading".exchanges(mic) on delete restrict,
    currency            text not null check (currency in (
        'RSD','EUR','CHF','USD','GBP','JPY','CAD','AUD'
    )),

    -- Stock fields
    outstanding_shares  bigint        check (outstanding_shares >= 0),
    dividend_yield      numeric(10,6) check (dividend_yield >= 0),

    -- Future fields
    contract_size       numeric(20,4) check (contract_size > 0),    -- also forex (default 1000)
    contract_unit       text,                                       -- "Barrel", "Liter", ...
    settlement_date     date,                                       -- futures + options expiry

    -- Forex fields
    base_currency       text check (base_currency in (
        'RSD','EUR','CHF','USD','GBP','JPY','CAD','AUD'
    )),
    quote_currency      text check (quote_currency in (
        'RSD','EUR','CHF','USD','GBP','JPY','CAD','AUD'
    )),
    liquidity           text check (liquidity in ('high','medium','low')),

    -- Option fields
    underlying_security_id  uuid references "trading".securities(id) on delete restrict,
    option_type             text check (option_type in ('call','put')),
    strike_price            numeric(20,4) check (strike_price >= 0),
    implied_volatility      numeric(20,6),
    premium                 numeric(20,4) check (premium >= 0),
    open_interest           bigint        check (open_interest >= 0),

    created_at          timestamptz not null default now(),
    updated_at          timestamptz not null default now(),

    unique (ticker, type),

    -- Per-type required columns (lightweight; full validation lives in
    -- the service layer where the message can be in Serbian).
    constraint securities_stock_required check (
        type <> 'stock' or outstanding_shares is not null
    ),
    constraint securities_future_required check (
        type <> 'future' or (contract_size is not null and settlement_date is not null)
    ),
    constraint securities_forex_required check (
        type <> 'forex' or (base_currency is not null and quote_currency is not null
                            and base_currency <> quote_currency)
    ),
    constraint securities_option_required check (
        type <> 'option' or (underlying_security_id is not null and option_type is not null
                             and strike_price is not null and settlement_date is not null
                             and premium is not null)
    )
);

create index securities_type_ticker_idx on "trading".securities (type, ticker);
create index securities_exchange_idx    on "trading".securities (exchange_mic);
create index securities_underlying_idx  on "trading".securities (underlying_security_id);

-- =====================================================================
-- Listings — live price snapshot per (security, exchange).
-- For options we don't write listings (the parent stock's listing is
-- what's quoted). For forex pairs we use Idea 1: one listing per pair,
-- exchange_mic NULL. The application enforces the 1:1 (forex/option)
-- expectations; the schema only requires (security_id) to be unique
-- across listings.
-- =====================================================================
create table "trading".listings (
    id              uuid primary key default gen_random_uuid(),
    security_id     uuid not null unique references "trading".securities(id) on delete cascade,
    exchange_mic    text references "trading".exchanges(mic) on delete restrict,
    price           numeric(20,4) not null check (price >= 0),
    ask             numeric(20,4) not null check (ask >= 0),
    bid             numeric(20,4) not null check (bid >= 0),
    volume          bigint        not null default 0,
    change_amt      numeric(20,4) not null default 0,
    contract_size   numeric(20,4) not null default 1 check (contract_size > 0),
    last_refresh    timestamptz   not null default now(),
    created_at      timestamptz   not null default now(),
    constraint listings_spread check (ask >= bid)
);

create index listings_exchange_idx on "trading".listings (exchange_mic);

-- =====================================================================
-- Daily price history per listing.
-- =====================================================================
create table "trading".listing_daily_price_info (
    listing_id     uuid not null references "trading".listings(id) on delete cascade,
    date           date not null,
    price          numeric(20,4) not null,
    ask            numeric(20,4) not null,
    bid            numeric(20,4) not null,
    change_amt     numeric(20,4) not null default 0,
    volume         bigint        not null default 0,
    primary key (listing_id, date)
);

-- =====================================================================
-- Orders.
-- Spec p.49 entity. user_kind discriminates client vs employee
-- because the rest of the trading flow (margin permission, approval
-- routing, FX commission) branches on it.
--
-- Pricing columns:
--   price_per_unit — display/reference price (Quote/Mid at submit).
--   limit_price    — for LIMIT and STOP_LIMIT (after trigger).
--   stop_price     — for STOP and STOP_LIMIT (trigger threshold).
-- One column per concept keeps the activation/comparison logic in the
-- execution worker readable.
-- =====================================================================
create table "trading".orders (
    id                  uuid primary key default gen_random_uuid(),
    user_id             uuid not null,
    user_kind           text not null check (user_kind in ('client','employee')),
    security_id         uuid not null references "trading".securities(id) on delete restrict,
    order_type          text not null check (order_type in ('market','limit','stop','stop_limit')),
    direction           text not null check (direction in ('buy','sell')),
    quantity            integer        not null check (quantity > 0),
    contract_size       numeric(20,4)  not null check (contract_size > 0),
    price_per_unit      numeric(20,4)  not null check (price_per_unit >= 0),
    limit_price         numeric(20,4)  check (limit_price >= 0),
    stop_price          numeric(20,4)  check (stop_price >= 0),
    all_or_none         boolean        not null default false,
    margin              boolean        not null default false,
    account_id          uuid           not null,           -- bank.accounts.id (settlement account)
    status              text           not null default 'pending'
                            check (status in ('pending','approved','declined')),
    approved_by         uuid,
    approval_required   boolean        not null default false,
    approved_at         timestamptz,
    is_done             boolean        not null default false,
    cancelled           boolean        not null default false,
    triggered           boolean        not null default false,    -- STOP/STOP_LIMIT activation flag
    after_hours         boolean        not null default false,
    remaining_quantity  integer        not null,
    last_modification   timestamptz    not null default now(),
    created_at          timestamptz    not null default now(),

    constraint orders_limit_price_required check (
        order_type not in ('limit','stop_limit') or limit_price is not null
    ),
    constraint orders_stop_price_required check (
        order_type not in ('stop','stop_limit') or stop_price is not null
    ),
    constraint orders_remaining_bounds check (
        remaining_quantity >= 0 and remaining_quantity <= quantity
    )
);

create index orders_user_idx       on "trading".orders (user_id, user_kind);
create index orders_status_idx     on "trading".orders (status);
create index orders_security_idx   on "trading".orders (security_id);
create index orders_active_idx     on "trading".orders (is_done, cancelled, status)
    where is_done = false and cancelled = false;

-- =====================================================================
-- Order executions — one row per partial fill.
-- Spec p.55-56 partial-fill simulation: each execution carries the
-- realized quantity, the per-unit fill price, and the bank op_id of
-- the money-move so the audit trail crosses services cleanly.
-- =====================================================================
create table "trading".order_executions (
    id              uuid primary key default gen_random_uuid(),
    order_id        uuid not null references "trading".orders(id) on delete cascade,
    quantity        integer       not null check (quantity > 0),
    price_per_unit  numeric(20,4) not null check (price_per_unit >= 0),
    total_amount    numeric(20,4) not null check (total_amount >= 0),
    commission_amt  numeric(20,4) not null default 0 check (commission_amt >= 0),
    bank_op_id      uuid,
    executed_at     timestamptz not null default now()
);

create index order_executions_order_idx on "trading".order_executions (order_id);

-- =====================================================================
-- Portfolio holdings.
-- We key by (user_id, security_id, account_id) so the same security
-- bought from different accounts (different currency, different tax
-- bucket) stays distinct. Weighted-average cost basis is the simplest
-- model that supports the realized-gain calculation; FIFO can be
-- layered on later without a schema change.
--
-- public_count is the spec p.61 OTC public-share count; not used until
-- c4 but the column lands now to avoid a destructive migration later.
-- =====================================================================
create table "trading".portfolio_holdings (
    id                       uuid primary key default gen_random_uuid(),
    user_id                  uuid not null,
    user_kind                text not null check (user_kind in ('client','employee')),
    security_id              uuid not null references "trading".securities(id) on delete restrict,
    account_id               uuid not null,
    quantity                 integer       not null check (quantity >= 0),
    weighted_avg_price       numeric(20,4) not null check (weighted_avg_price >= 0),
    public_count             integer       not null default 0 check (public_count >= 0),
    acquired_at              timestamptz   not null default now(),
    updated_at               timestamptz   not null default now(),
    unique (user_id, security_id, account_id)
);

create index portfolio_holdings_user_idx on "trading".portfolio_holdings (user_id, user_kind);

-- =====================================================================
-- Realized gains — one row per closing sell-execution.
-- Spec p.62: capital-gains tax is 15% of profit, computed in RSD,
-- charged at the end of every month from the same account that paid
-- for the security. The state has a single RSD account.
-- =====================================================================
create table "trading".realized_gains (
    id              uuid primary key default gen_random_uuid(),
    user_id         uuid not null,
    user_kind       text not null check (user_kind in ('client','employee')),
    security_id     uuid not null references "trading".securities(id) on delete restrict,
    account_id      uuid not null,
    quantity        integer       not null check (quantity > 0),
    cost_basis_amt  numeric(20,4) not null,
    proceeds_amt    numeric(20,4) not null,
    currency        text not null check (currency in (
        'RSD','EUR','CHF','USD','GBP','JPY','CAD','AUD'
    )),
    gain_native     numeric(20,4) not null,                -- proceeds - cost_basis (in `currency`)
    gain_rsd        numeric(20,4) not null,                -- gain_native converted to RSD (no commission)
    realized_at     timestamptz   not null default now(),
    taxed           boolean       not null default false,
    taxed_at        timestamptz,
    tax_op_id       uuid                                   -- bank.transactions.op_id of the 15% transfer
);

create index realized_gains_user_taxed_idx on "trading".realized_gains (user_id, taxed);
create index realized_gains_realized_idx   on "trading".realized_gains (realized_at);

-- =====================================================================
-- SAGA executions — orchestrator state machine.
-- Lands now (c3) so c4 OTC settlement and c5 inter-bank 2PC can build
-- on a stable table. Step handlers are keyed by (transaction_id,
-- step_name) for at-least-once idempotency at the executor layer
-- (handlers themselves still must be safe to replay).
-- =====================================================================
create table "trading".saga_executions (
    transaction_id  uuid primary key,
    saga_type       text not null,
    current_step    text not null,
    state           jsonb not null,
    status          text not null check (status in ('running','compensating','completed','failed')),
    attempts        int not null default 0,
    last_error      text,
    created_at      timestamptz not null default now(),
    updated_at      timestamptz not null default now()
);

create index saga_executions_status_idx on "trading".saga_executions (status);
