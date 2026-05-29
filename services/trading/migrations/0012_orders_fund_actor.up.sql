-- c4 PR3 — fund-as-actor on orders (FUND-5, spec p.74-75).
--
-- Supervisor places orders on a fund's behalf:
--   * actor_kind='fund' identifies the row as a fund-side order so the
--     execution worker can skip realized_gains writes (EDGE-3 — fund
--     orders are pre-tax, the client pays at withdrawal).
--   * on_behalf_of_fund_id is the fund whose basket the order applies
--     to. The settlement account on the order row is the fund's bank
--     account; the order's user_id is the fund.id (matches the
--     user_kind='fund' rows we just admitted into portfolio_holdings).
--
-- The default 'client' keeps every existing row valid without backfill.
alter table "trading".orders
  add column actor_kind text not null default 'client'
    check (actor_kind in ('client','employee','fund')),
  add column on_behalf_of_fund_id uuid
    references "trading".investment_funds(id) on delete restrict,
  add constraint orders_fund_actor_requires_fund check (
    actor_kind <> 'fund' or on_behalf_of_fund_id is not null
  );

-- Extend user_kind to admit 'fund' so a fund-actor order's owner
-- (user_id = fund.id) parents holdings keyed by user_kind='fund' via
-- the same ApplyBuyFill / ApplySellFill helpers. The actor_kind column
-- is then strictly an audit/UX field; the load-bearing identity is
-- user_kind.
alter table "trading".orders
  drop constraint orders_user_kind_check;
alter table "trading".orders
  add constraint orders_user_kind_check
  check (user_kind in ('client','employee','fund'));

create index orders_fund_actor_idx
    on "trading".orders (on_behalf_of_fund_id)
    where on_behalf_of_fund_id is not null;
