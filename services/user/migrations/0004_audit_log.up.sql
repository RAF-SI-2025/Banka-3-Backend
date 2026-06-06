-- Cross-cutting audit log (todoSpec "Audit log"). One row per recorded
-- administrative action: employee create/update, permission change,
-- agent limit change, order approve/decline, manual tax run, inter-bank
-- message, etc. The user service owns the table; other services append
-- via the RecordAuditEntry gRPC. Read access is admin/supervisor-only
-- (enforced in the service layer).
create table "user".audit_log (
    id           uuid primary key default gen_random_uuid(),
    action       text not null,
    -- actor_id is text, not uuid: cross-service callers may pass a
    -- non-UUID sentinel principal (e.g. an internal service identity).
    actor_id     text not null,
    actor_kind   text not null,
    actor_name   text not null default '',
    target_id    text not null default '',
    target_label text not null default '',
    old_value    text not null default '',
    new_value    text not null default '',
    note         text not null default '',
    created_at   timestamptz not null default now()
);

-- Default feed: newest-first.
create index audit_log_created_idx on "user".audit_log (created_at desc);
-- Filter-by-action-type (S44), still newest-first within an action.
create index audit_log_action_idx on "user".audit_log (action, created_at desc);
-- Filter-by-actor (S45).
create index audit_log_actor_idx on "user".audit_log (actor_id);
