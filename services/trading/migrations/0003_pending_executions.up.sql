-- BE-3 / BE-4 (c3 audit). executeFill is now a two-phase saga:
--
--   1. INSERT a pending row (status='pending'); its UUID becomes the
--      deterministic op_id used for the bank call.
--   2. Call bank.SettleTrade / SettleForexFill / SettleCapitalGainsTax
--      with that op_id; the bank's (op_id, leg_index) unique constraint
--      (migration bank.0011) makes the call idempotent on retry.
--   3. UPDATE the row to status='settled', advance order progress, and
--      apply the portfolio change atomically.
--
-- Worker crash between (2) and (3) leaves a pending row; the next tick
-- resumes from step 2 with the same op_id. Bank no-ops; trading books.
--
-- Existing rows are settled by definition — backfill default 'settled'.
-- Partial index keeps the resume lookup cheap regardless of history.

alter table "trading".order_executions
    add column status text not null default 'settled'
    check (status in ('pending', 'settled'));

create index order_executions_pending_idx
    on "trading".order_executions (order_id)
    where status = 'pending';
