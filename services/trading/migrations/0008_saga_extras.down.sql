drop index if exists "trading".saga_executions_pending_due_idx;
alter table "trading".saga_executions
    drop column if exists attempts_max,
    drop column if exists next_attempt_at;
