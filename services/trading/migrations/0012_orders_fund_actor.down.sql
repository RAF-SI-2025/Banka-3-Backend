drop index if exists "trading".orders_fund_actor_idx;
alter table "trading".orders
  drop constraint if exists orders_user_kind_check;
alter table "trading".orders
  add constraint orders_user_kind_check
  check (user_kind in ('client','employee'));
alter table "trading".orders
  drop constraint if exists orders_fund_actor_requires_fund,
  drop column if exists on_behalf_of_fund_id,
  drop column if exists actor_kind;
