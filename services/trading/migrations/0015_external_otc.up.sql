-- c5 — Cross-bank OTC (spec p.77+).
--
-- Mirrors the c4 otc_offers / otc_contracts shape (0010_otc_offers_contracts.up.sql)
-- but for threads where one party is at a remote bank. The negotiation
-- protocol is the same (offer → counter → withdraw / accept → contract
-- → exercise), it's just transported across banks via the gateway's
-- partner-REST surface.
--
-- Locality
-- ========
-- A thread is owned by the bank whose user is involved; the other bank
-- holds a mirror row. `direction` discriminates which side initiated:
--   * outgoing — a local user opened the thread against a partner-
--     advertised holding (we are the buyer).
--   * incoming — a partner user opened a thread against a holding we
--     advertise (we are the seller).
--
-- Remote-side fields (`remote_*`) capture the partner's identifiers
-- verbatim — bank code, their thread id, their user_ref string,
-- display name, account number. They're TEXT (not uuid) because partner
-- banks don't all use UUIDs.
--
-- Local-side fields use the rewrite's UUID convention. `local_user_id`,
-- `local_account_id` reference the rows in user.users / bank.accounts
-- without a cross-schema FK (project convention; see 0002_c3.up.sql).
--
-- `seller_holding_ref` is a single TEXT for both directions:
--   * incoming — set to the uuid (as text) of the local portfolio_holdings
--     row the partner-side buyer offered against;
--   * outgoing — set to the partner's holding identifier verbatim
--     (whatever the partner advertised in /otc/public).
-- Validation of the local case happens at the service layer.

create table "trading".external_otc_threads (
    id                    uuid          primary key default gen_random_uuid(),

    -- outgoing — local user is buyer; incoming — local user is seller.
    direction             text          not null check (direction in ('outgoing','incoming')),

    -- Partner-side identity. remote_thread_id stays '' until the
    -- partner POSTs back the id they assigned to their mirror row.
    remote_bank_code      text          not null,
    remote_thread_id      text          not null default '',
    remote_user_ref       text          not null,
    remote_display_name   text          not null default '',
    remote_account_ref    text          not null default '',

    -- Local-side identity. Account number is the spec 18-digit form so
    -- partner-facing endpoints can echo it back without re-fetching.
    local_user_id         uuid          not null,
    local_user_kind       text          not null check (local_user_kind in ('client','employee')),
    local_account_id      uuid          not null,
    local_account_number  text          not null check (length(local_account_number) = 18),
    local_role            text          not null check (local_role in ('buyer','seller')),

    -- security_id is nullable: outgoing threads may reference a partner
    -- security we haven't mirrored locally. Service layer fills it
    -- lazily once a contract gets minted.
    security_id           uuid,
    security_ticker       text          not null,
    seller_holding_ref    text          not null default '',

    quantity              integer       not null check (quantity > 0),
    price_per_unit        numeric(20,4) not null check (price_per_unit >= 0),
    premium               numeric(20,4) not null check (premium       >= 0),
    currency              text          not null,
    settlement_date       date          not null,

    -- Tracks whose side moved last (counter/withdraw/accept). Used by
    -- the FE "waiting on the other side" badge.
    modified_by_side      text          not null check (modified_by_side in ('local','remote')),

    status                text          not null check (status in (
        'open','superseded','accepted','withdrawn','expired','rejected'
    )),

    created_at            timestamptz   not null default now(),
    updated_at            timestamptz   not null default now()
);

-- A (remote_bank_code, remote_thread_id) tuple must map to at most one
-- of our mirror rows. The partial predicate excludes rows where we
-- haven't yet learned the partner's thread id (outgoing pre-ack).
create unique index external_otc_threads_remote_key
    on "trading".external_otc_threads (remote_bank_code, remote_thread_id)
    where remote_thread_id <> '';

-- User-board hot path: "Aktivne ponude (eksterno)" — list by user,
-- newest first, with optional status filter.
create index external_otc_threads_local_user_idx
    on "trading".external_otc_threads (local_user_id, updated_at desc);

create index external_otc_threads_local_user_status_idx
    on "trading".external_otc_threads (local_user_id, status, updated_at desc);


-- One row per iteration (oldest first). The thread carries the
-- "live" terms; this is the negotiation audit log.
create table "trading".external_otc_iterations (
    id                  uuid          primary key default gen_random_uuid(),
    thread_id           uuid          not null references "trading".external_otc_threads(id) on delete cascade,

    proposed_by_side    text          not null check (proposed_by_side in ('local','remote')),
    quantity            integer       not null check (quantity > 0),
    price_per_unit      numeric(20,4) not null check (price_per_unit >= 0),
    premium             numeric(20,4) not null check (premium       >= 0),
    settlement_date     date          not null,
    created_at          timestamptz   not null default now()
);

create index external_otc_iterations_thread_idx
    on "trading".external_otc_iterations (thread_id, created_at);


-- Contract — same shape as otc_contracts but cross-bank. premium_op_id
-- and exercise_op_id reference bank.transactions op_id of the legs the
-- bank's 2PC primitive settled.
create table "trading".external_otc_contracts (
    id                   uuid          primary key default gen_random_uuid(),
    -- One contract per thread (terminal).
    thread_id            uuid          not null unique references "trading".external_otc_threads(id) on delete restrict,

    direction            text          not null check (direction in ('outgoing','incoming')),

    remote_bank_code     text          not null,
    remote_thread_id     text          not null,
    remote_user_ref      text          not null,
    remote_display_name  text          not null default '',
    remote_account_ref   text          not null default '',

    local_user_id        uuid          not null,
    local_user_kind      text          not null check (local_user_kind in ('client','employee')),
    local_account_id     uuid          not null,
    local_account_number text          not null check (length(local_account_number) = 18),
    local_role           text          not null check (local_role in ('buyer','seller')),

    security_id          uuid,
    security_ticker      text          not null,
    seller_holding_ref   text          not null default '',

    quantity             integer       not null check (quantity > 0),
    strike_price         numeric(20,4) not null check (strike_price >= 0),
    premium_paid         numeric(20,4) not null check (premium_paid >= 0),
    currency             text          not null,
    settlement_date      date          not null,

    accepted_by_side     text          not null check (accepted_by_side in ('local','remote')),
    status               text          not null check (status in (
        'active','exercised','expired','settling'
    )),

    -- bank.transactions op_id of the premium-leg saga. Stamped at
    -- accept time; persisted so the cross-service audit can be
    -- reconstructed without join hints.
    premium_op_id        uuid          not null,
    -- Populated when the buyer (whichever side that is) exercises.
    exercise_op_id       uuid,
    exercised_at         timestamptz,

    created_at           timestamptz   not null default now(),
    updated_at           timestamptz   not null default now()
);

create index external_otc_contracts_local_user_idx
    on "trading".external_otc_contracts (local_user_id, updated_at desc);

create index external_otc_contracts_local_user_status_idx
    on "trading".external_otc_contracts (local_user_id, status, updated_at desc);

-- Expiry sweep — same shape as otc_contracts_expiry_sweep but cross-bank.
create index external_otc_contracts_expiry_sweep
    on "trading".external_otc_contracts (settlement_date) where status = 'active';
