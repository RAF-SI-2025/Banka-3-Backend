-- SAGA test-spec observability model (SAGA_test.pdf SG-01..SG-11).
--
-- The base saga_executions row carried only `current_step` (a step-name
-- string used as the resume pointer) + `last_error` + `attempts`. The
-- test spec asks for three more observable things:
--
--   - a terminal `compensated` status, distinct from `failed`. A saga
--     that rolled back cleanly (all Ci succeeded) is Compensated; only
--     a saga that could NOT finish its rollback is `failed`. Invariant
--     I5 leans on this distinction.
--   - `step_no`: the numeric ordinal (1..N) of the last *attempted*
--     phase. Frozen at the failed phase for the duration of the
--     compensation walk (so SG-05 reads 3 throughout C2/C1).
--   - `log`: an append-only array of per-attempt records,
--     `[{"step":"F1","result":"ok"}, {"step":"F3","result":"err"}, ...]`,
--     one per forward/compensation attempt, in order (invariant I4).
--
-- `current_step` (the step-name resume pointer) is kept as-is — the
-- orchestrator's resume machinery + the c5 interbank saga both key off
-- it; `step_no` is the spec-facing numeric view layered on top.

alter table "trading".saga_executions
    add column step_no int   not null default 0,
    add column log     jsonb not null default '[]'::jsonb;

-- Widen the status domain to admit the clean-rollback terminal.
alter table "trading".saga_executions
    drop constraint if exists saga_executions_status_check;
alter table "trading".saga_executions
    add constraint saga_executions_status_check
    check (status in ('running','compensating','completed','compensated','failed'));
