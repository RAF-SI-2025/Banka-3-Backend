-- Watchlists (todoSpec C3 S35-S39).
--
-- A user keeps one or more named watchlists (S36, e.g. "Tech akcije"),
-- each holding a set of securities (S35). Items are removed individually
-- (S37). A security can appear at most once per list (unique constraint).
-- The per-user list view keys off (user_id), so it is indexed.

create table "trading".watchlists (
    id         uuid primary key default gen_random_uuid(),
    user_id    text not null,
    user_kind  text not null,
    name       text not null,
    created_at timestamptz not null default now()
);

create index watchlists_user_id_idx on "trading".watchlists (user_id);

create table "trading".watchlist_items (
    id           uuid primary key default gen_random_uuid(),
    watchlist_id uuid not null references "trading".watchlists (id) on delete cascade,
    security_id  uuid not null,
    created_at   timestamptz not null default now(),
    unique (watchlist_id, security_id)
);
