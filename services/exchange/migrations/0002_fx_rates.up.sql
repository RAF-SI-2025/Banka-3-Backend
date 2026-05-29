-- Slice 1 of celina 2: FX rate feed.
--
-- Rates are stored directional (from → to) so the bid/ask convention
-- doesn't need re-interpretation on read. The bank service uses the
-- ask side when selling foreign currency to a client (menjačnica buy)
-- and the bid side when buying it back (menjačnica sell). Spec p.* on
-- "menjačnica" — every conversion goes RSD↔X with the sell-side rate.
--
-- We keep one row per (from, to) and overwrite on upsert; if/when the
-- spec demands historical rates, a separate fx_rate_history table
-- with the same shape would be the place to grow.

create table "exchange".fx_rates (
    "from"     text not null check ("from" in ('RSD','EUR','CHF','USD','GBP','JPY','CAD','AUD')),
    "to"       text not null check ("to"   in ('RSD','EUR','CHF','USD','GBP','JPY','CAD','AUD')),
    bid        numeric(20,8) not null,
    ask        numeric(20,8) not null,
    updated_at timestamptz not null default now(),
    primary key ("from", "to"),
    constraint fx_rates_distinct check ("from" <> "to"),
    constraint fx_rates_nonneg   check (bid > 0 and ask > 0),
    constraint fx_rates_spread   check (ask >= bid)
);

create index fx_rates_from_idx on "exchange".fx_rates ("from");
