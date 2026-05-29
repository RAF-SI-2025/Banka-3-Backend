-- c4 SAGA orchestrator extras (spec p.64-76 OTC + funds).
--
-- The base table from 0002_c3 carries `attempts` but no scheduling
-- column. The orchestrator + recovery worker need three more things:
--
--   - `next_attempt_at` so the recovery worker picks up rows in
--     priority order without scanning every running saga every tick;
--   - `attempts_max` so we can bound the retry budget per saga type
--     (OTC accept's 8 retries is fine; an OTC exercise that just keeps
--     bouncing off a market-closed condition might want 16);
--   - a partial index supporting the recovery sweep's hot path.
alter table "trading".saga_executions
    add column next_attempt_at timestamptz not null default now(),
    add column attempts_max    int         not null default 8;

create index saga_executions_pending_due_idx
    on "trading".saga_executions (next_attempt_at)
    where status in ('running', 'compensating');
