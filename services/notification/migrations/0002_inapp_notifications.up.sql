-- In-app notification feed (todoSpec "Notifikacije" S19). One row per
-- delivered notification; the notification service writes rows from
-- CreateNotification and the gateway-exposed List/MarkRead RPCs read
-- them back scoped to the authenticated user.
create table "notification".notifications (
    id         uuid primary key default gen_random_uuid(),
    user_id    uuid not null,
    user_kind  text not null check (user_kind in ('client', 'employee')),
    kind       text not null default 'generic',
    title      text not null,
    body       text not null,
    read_at    timestamptz,
    created_at timestamptz not null default now()
);

-- Feed read path: newest-first per user.
create index notifications_user_created_idx
    on "notification".notifications (user_id, created_at desc);

-- Unread-count + unread-only filter: partial index keeps the bell-badge
-- query cheap as read rows accumulate.
create index notifications_user_unread_idx
    on "notification".notifications (user_id)
    where read_at is null;
