drop trigger if exists clients_updated_at on "user".clients;
drop trigger if exists employees_updated_at on "user".employees;
drop function if exists "user".set_updated_at();

drop table if exists "user".password_reset_tokens;
drop table if exists "user".activation_tokens;
drop table if exists "user".refresh_tokens;
drop table if exists "user".clients;
drop table if exists "user".employees;
