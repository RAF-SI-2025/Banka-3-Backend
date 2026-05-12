-- c4 PR2 — OTC offers + contracts (spec p.64-69, 79).
--
-- Spec model
-- ==========
--   * Two parties (clients ↔ clients OR supervisors ↔ supervisors —
--     spec p.79 forbids the mixed case) negotiate a single thread of
--     iterations against one seller-holding row. Each iteration
--     supersedes the previous; only one row in a thread is "open" at
--     a time, and `modified_by` carries the user_id of whichever side
--     proposed the latest iteration so the FE can render an unread
--     badge (spec p.69 optional indicator) for the other party.
--   * Accepting the open iteration freezes the thread (status flips
--     from `open` → `accepted` on the live row, prior `superseded`
--     rows stay as audit) and creates a single `otc_contracts` row,
--     which is the actual call/put-style option contract per spec
--     p.67.b — strike = `price_per_unit` at time of accept, premium
--     paid up-front, exercise window until `settlement_date`.
--   * The seller's holding row gets `reserved_count` bumped by the
--     offer's quantity at offer-create time (spec p.68 "12 = 3 + 7 + 2"
--     worked example) and the contract inherits the same reservation
--     until expiry or exercise. Withdraw releases; supersede may
--     adjust if qty changed.
--
-- Schema notes
-- ============
--   * `thread_id` groups iterations. By convention the first iteration's
--     `id` is reused as the `thread_id`, but we keep them as separate
--     columns so a thread can be referenced by name (`/otc/offers/{thread_id}/…`)
--     without forcing callers to know which row is "first".
--   * `buyer_kind` / `seller_kind` are `client` or `employee`. They must
--     match (spec p.79). No DB constraint to enforce equality — the
--     mixed case is a service-layer reject (we want a Serbian error
--     message, not a 23514).
--   * `seller_holding_id` is a soft reference; the trading-side store
--     also owns portfolio_holdings, so the FK is safe.
--   * Money columns are numeric(20,4) (matches bank.transactions +
--     trading.orders); rates would be numeric(20,8) but premium and
--     price-per-unit are amounts, not rates.
--   * One contract per thread (`unique (thread_id)`): accepting a
--     thread is terminal; subsequent counter-offers against the
--     same thread are rejected at the service layer.

create table "trading".otc_offers (
    id                  uuid primary key default gen_random_uuid(),
    thread_id           uuid not null,

    security_id         uuid not null references "trading".securities(id) on delete restrict,
    seller_holding_id   uuid not null references "trading".portfolio_holdings(id) on delete restrict,

    buyer_id            uuid not null,
    buyer_kind          text not null check (buyer_kind  in ('client','employee')),
    buyer_account_id    uuid not null,

    seller_id           uuid not null,
    seller_kind         text not null check (seller_kind in ('client','employee')),
    seller_account_id   uuid not null,

    quantity            integer       not null check (quantity > 0),
    price_per_unit      numeric(20,4) not null check (price_per_unit >= 0),
    premium             numeric(20,4) not null check (premium       >= 0),
    currency            text          not null,
    settlement_date     date          not null,

    -- Who proposed this iteration. Used by the FE unread badge: a
    -- thread row whose modified_by != currentUserId AND status='open'
    -- is "waiting on the current user".
    modified_by         uuid not null,

    status              text not null check (status in (
        'open','superseded','accepted','withdrawn','expired'
    )),

    created_at          timestamptz not null default now(),
    updated_at          timestamptz not null default now()
);

-- All iterations in a thread, oldest first — drives the thread-detail
-- modal on spec p.69.
create index otc_offers_thread_idx     on "trading".otc_offers (thread_id, created_at);

-- Active negotiations per party — drives the "Aktivne ponude" pages
-- (spec p.69) where both buyer- and seller-side users see their open
-- threads. Partial indexes so the hot path doesn't scan superseded
-- audit rows.
create index otc_offers_open_seller    on "trading".otc_offers (seller_id) where status = 'open';
create index otc_offers_open_buyer     on "trading".otc_offers (buyer_id)  where status = 'open';

-- Reservation overlap check (spec p.68) uses this to sum across all
-- open offers on a holding.
create index otc_offers_open_holding   on "trading".otc_offers (seller_holding_id) where status = 'open';


create table "trading".otc_contracts (
    id                  uuid primary key default gen_random_uuid(),
    -- One contract per thread. Accepting a thread is terminal.
    thread_id           uuid not null unique,

    security_id         uuid not null references "trading".securities(id) on delete restrict,
    seller_holding_id   uuid not null references "trading".portfolio_holdings(id) on delete restrict,

    buyer_id            uuid not null,
    buyer_kind          text not null check (buyer_kind  in ('client','employee')),
    buyer_account_id    uuid not null,

    seller_id           uuid not null,
    seller_kind         text not null check (seller_kind in ('client','employee')),
    seller_account_id   uuid not null,

    quantity            integer       not null check (quantity > 0),
    strike_price        numeric(20,4) not null check (strike_price >= 0),
    premium_paid        numeric(20,4) not null check (premium_paid >= 0),
    currency            text          not null,
    settlement_date     date          not null,

    -- Bank op_id of the premium-leg saga step. Stamped at create_contract
    -- step time; persisted so the cross-service audit (transactions
    -- ledger ↔ contract row) can be reconstructed without joining on
    -- (op_kind='otc_premium', purpose LIKE ...).
    premium_op_id       uuid not null,

    status              text not null check (status in (
        'active','exercised','expired','settling'
    )),

    -- Populated when the buyer exercises (spec p.61.d).
    exercised_op_id     uuid,
    exercise_saga_id    uuid,
    exercised_at        timestamptz,

    created_at          timestamptz not null default now(),
    updated_at          timestamptz not null default now()
);

-- Buyer view ("Sklopljeni ugovori", spec p.69): all contracts the
-- buyer holds. Active filter is the hot path for the exercise board.
create index otc_contracts_active_buyer   on "trading".otc_contracts (buyer_id, status);
create index otc_contracts_active_seller  on "trading".otc_contracts (seller_id, status);

-- Expiry sweep cron — picks up contracts past settlement_date that
-- are still active.
create index otc_contracts_expiry_sweep   on "trading".otc_contracts (settlement_date) where status = 'active';
