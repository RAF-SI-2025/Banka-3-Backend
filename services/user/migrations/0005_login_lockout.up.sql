-- Brute-force login protection (todoSpec "Brute-force zaštita", S7–S11).
-- Both employees and clients can log in, so both get the counter +
-- lockout window. failed_login_attempts resets to 0 on a successful
-- login or a password reset; locked_until holds the instant the lock
-- lifts (null = not locked).
alter table "user".employees
    add column failed_login_attempts int not null default 0,
    add column locked_until timestamptz;

alter table "user".clients
    add column failed_login_attempts int not null default 0,
    add column locked_until timestamptz;
