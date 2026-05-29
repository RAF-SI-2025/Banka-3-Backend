alter table "trading".portfolio_holdings
  drop constraint portfolio_holdings_user_kind_check;
alter table "trading".portfolio_holdings
  add constraint portfolio_holdings_user_kind_check
  check (user_kind in ('client','employee'));

drop table if exists "trading".fund_performance_snapshots;
drop table if exists "trading".client_fund_transactions;
drop table if exists "trading".client_fund_positions;
drop table if exists "trading".investment_funds;
