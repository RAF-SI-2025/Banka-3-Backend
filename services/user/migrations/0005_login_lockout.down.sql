alter table "user".employees
    drop column if exists failed_login_attempts,
    drop column if exists locked_until;

alter table "user".clients
    drop column if exists failed_login_attempts,
    drop column if exists locked_until;
