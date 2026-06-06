-- Price alerts (todoSpec C3 S26-S29).
--
-- A user sets a threshold on a security; when the security's current
-- price crosses it (ABOVE: price >= threshold, BELOW: price <= threshold)
-- the sweep fires one notification and deactivates the alert. The sweep
-- walks every active row, so (is_active) is indexed; the per-user list
-- view keys off (user_id).

create table "trading".price_alerts (
    id           uuid primary key default gen_random_uuid(),
    user_id      text not null,
    user_kind    text not null,
    security_id  uuid not null,
    threshold    numeric(20, 4) not null,
    condition    text not null check (condition in ('ABOVE', 'BELOW')),
    is_active    boolean not null default true,
    created_at   timestamptz not null default now(),
    triggered_at timestamptz
);

create index price_alerts_is_active_idx on "trading".price_alerts (is_active);
create index price_alerts_user_id_idx on "trading".price_alerts (user_id);
