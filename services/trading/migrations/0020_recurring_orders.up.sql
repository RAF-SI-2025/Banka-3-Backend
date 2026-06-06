-- Recurring orders / "Trajni nalog" (DCA — todoSpec C3 S47-S53).
--
-- A user schedules a recurring Market BUY of a security on a cadence
-- (DAILY/WEEKLY/MONTHLY). On each next_run a cron creates a Market BUY
-- order for the configured amount-in-RSD or quantity and advances
-- next_run by the cadence. Insufficient funds skip the order, notify
-- the client, and still advance. Pausing flips active=false; cancelling
-- deactivates the row permanently.
--
-- The sweep walks every due+active row, so (active, next_run) is indexed.

create table "trading".recurring_orders (
    id           uuid primary key default gen_random_uuid(),
    user_id      text not null,
    user_kind    text not null,
    security_id  uuid not null,
    direction    text not null default 'buy',
    mode         text not null check (mode in ('BYAMOUNT', 'BYQUANTITY')),
    amount_rsd   numeric(20, 4),
    quantity     int,
    account_id   uuid not null,
    cadence      text not null check (cadence in ('DAILY', 'WEEKLY', 'MONTHLY')),
    next_run     timestamptz not null,
    active       boolean not null default true,
    created_at   timestamptz not null default now(),
    updated_at   timestamptz not null default now()
);

create index recurring_orders_active_next_run_idx
    on "trading".recurring_orders (active, next_run);
create index recurring_orders_user_id_idx
    on "trading".recurring_orders (user_id);
