alter table "trading".order_executions
    drop column if exists last_error,
    drop column if exists attempts;

alter table "trading".order_executions
    drop constraint if exists order_executions_status_check;

alter table "trading".order_executions
    add constraint order_executions_status_check
    check (status in ('pending', 'settled'));
