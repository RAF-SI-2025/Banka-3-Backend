drop index if exists "trading".realized_gains_fund_idx;
alter table "trading".realized_gains drop column if exists fund_id;
alter table "trading".realized_gains alter column security_id set not null;
