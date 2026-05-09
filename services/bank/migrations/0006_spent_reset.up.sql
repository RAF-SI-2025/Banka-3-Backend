-- Spec p.12-13: every account carries a daily and monthly spending
-- counter ("Dnevna potrošnja" / "Mesečna potrošnja"). Without a reset,
-- both counters monotonically grow forever and lock the account out of
-- payments after the first day's traffic. We add two date columns
-- recording when each counter was last zeroed, and a daily cron walks
-- the table to apply the rollover.
--
-- The reset is calendar-based (CURRENT_DATE), not time-based — so it
-- doesn't need a fake clock to test. For new rows the default of
-- current_date means the next cron tick is a no-op until the calendar
-- day actually changes.

alter table "bank".accounts
    add column daily_spent_reset_on   date not null default current_date,
    add column monthly_spent_reset_on date not null default current_date;

-- Partial indexes so the cron's WHERE … < CURRENT_DATE scan stays cheap
-- even with many active rows; inactive accounts aren't reset (no traffic
-- to count) so they don't need the index.
create index accounts_daily_reset_idx
    on "bank".accounts (daily_spent_reset_on)
    where status = 'active';

create index accounts_monthly_reset_idx
    on "bank".accounts (monthly_spent_reset_on)
    where status = 'active';
