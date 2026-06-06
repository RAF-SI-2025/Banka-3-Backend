-- c5 — Inter-bank observability & control (spec p.77+).
--
-- Two tables backing the supervisor "Međubankarske transakcije" portal:
--
--   * interbank_blacklist — partner banks (by routing number) this bank
--     refuses to transact with. PreparePayment rejects an inbound /
--     outbound leg whose sender_routing_number is actively blacklisted.
--     Rows are inserted manually by a supervisor or automatically when
--     the consecutive-failure counter crosses the threshold. Unblocking
--     flips active=false + stamps unblocked_at; the row is retained for
--     audit rather than deleted.
--
--   * interbank_partner_failures — a per-routing-number counter of
--     *consecutive* failed partner interactions. Reset to 0 on any
--     success; when it reaches the threshold the partner is auto-blocked
--     and a supervisor is notified. One row per routing number, upserted.

create table "bank".interbank_blacklist (
    sender_routing_number integer     not null primary key,
    reason                text        not null default '',
    -- Supervisor user id for a manual block, or 'system' for an auto-block.
    blocked_by            text        not null default '',
    blocked_at            timestamptz not null default now(),
    -- Set when a supervisor unblocks; null while active.
    unblocked_at          timestamptz,
    active                boolean     not null default true
);

-- Fast "is this partner currently blocked?" lookup on the prepare path.
create index interbank_blacklist_active_idx
    on "bank".interbank_blacklist (sender_routing_number)
    where active;


create table "bank".interbank_partner_failures (
    sender_routing_number integer     not null primary key,
    -- Consecutive failures since the last success. Reset to 0 on success.
    consecutive_failures  integer     not null default 0 check (consecutive_failures >= 0),
    last_failure_at       timestamptz,
    updated_at            timestamptz not null default now()
);
