drop index if exists "trading".order_executions_pending_idx;
alter table "trading".order_executions drop column status;
