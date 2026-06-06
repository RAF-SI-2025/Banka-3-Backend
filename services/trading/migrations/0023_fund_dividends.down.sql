drop table if exists "trading".fund_dividend_distributions;

alter table "trading".investment_funds
  drop column if exists reinvest_dividends;
