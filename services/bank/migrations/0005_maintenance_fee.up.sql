-- Per spec p.12, every checking account carries a monthly maintenance
-- fee ("održavanje računa"). The example shows 255.00 RSD for a
-- standard RSD account; FX accounts have no fee row, and student /
-- youth / unemployed accounts are typically free in Serbian banking.
--
-- We persist the fee per account (set at creation) and track when it
-- was last debited so the monthly cron can be run any day without
-- double-charging if it slips.

alter table "bank".accounts
    add column last_maintenance_debit timestamptz;

-- Backfill: treat existing accounts as freshly opened so the first cron
-- run a month from now charges them once.
update "bank".accounts set last_maintenance_debit = now();
