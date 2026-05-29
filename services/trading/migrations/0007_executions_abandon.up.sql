-- Finding 2 (soak audit 2026-05-11). The recovery sweep used to retry a
-- permanently-failing bank settle every tick forever — a cancelled order
-- whose pending row was wedged by a bad source-account couldn't be cleared
-- without a raw DELETE (the status check constraint only allowed
-- 'pending'/'settled'). Same row also blocked clean log signal at scale.
--
-- This migration:
--   1. Adds a third terminal status 'abandoned' so the recovery query
--      (status='pending') drops these rows naturally and a human-readable
--      audit row remains.
--   2. Adds an `attempts` counter the service bumps on every failed
--      resume — used to escalate log level after N transient failures.
--   3. Adds `last_error` so an operator can see why a row was abandoned
--      without diving into service logs.

alter table "trading".order_executions
    drop constraint if exists order_executions_status_check;

alter table "trading".order_executions
    add constraint order_executions_status_check
    check (status in ('pending', 'settled', 'abandoned'));

alter table "trading".order_executions
    add column attempts   int  not null default 0,
    add column last_error text;
