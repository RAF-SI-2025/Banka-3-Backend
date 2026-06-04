alter table "trading".saga_executions
    drop constraint if exists saga_executions_status_check;
alter table "trading".saga_executions
    add constraint saga_executions_status_check
    check (status in ('running','compensating','completed','failed'));

alter table "trading".saga_executions
    drop column if exists log,
    drop column if exists step_no;
