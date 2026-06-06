-- Slice: FX rate HISTORY (todoSpec mobile "kursna lista u zadnjih mesec
-- dana"). The fx_rates table is latest-only (one row per (from,to),
-- overwritten on every feed refresh — see 0002), so there is no record
-- of how a pair moved over time. This append-only table accrues one row
-- per (from,to) on every feed pass going forward, which the mobile app
-- reads back for its last-month rate view.
--
-- Append-only: never updated, never upserted. Rows are queried by pair
-- over a recent time window (newest first), hence the composite index.

create table "exchange".fx_rate_history (
    id          bigint generated always as identity primary key,
    "from"      text not null check ("from" in ('RSD','EUR','CHF','USD','GBP','JPY','CAD','AUD')),
    "to"        text not null check ("to"   in ('RSD','EUR','CHF','USD','GBP','JPY','CAD','AUD')),
    bid         numeric(20,8) not null,
    ask         numeric(20,8) not null,
    recorded_at timestamptz not null default now(),
    constraint fx_rate_history_distinct check ("from" <> "to"),
    constraint fx_rate_history_nonneg   check (bid > 0 and ask > 0),
    constraint fx_rate_history_spread   check (ask >= bid)
);

create index fx_rate_history_pair_recorded_idx
    on "exchange".fx_rate_history ("from", "to", recorded_at desc);
