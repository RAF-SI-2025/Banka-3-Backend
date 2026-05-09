drop index if exists "bank".accounts_monthly_reset_idx;
drop index if exists "bank".accounts_daily_reset_idx;
alter table "bank".accounts
    drop column if exists monthly_spent_reset_on,
    drop column if exists daily_spent_reset_on;
