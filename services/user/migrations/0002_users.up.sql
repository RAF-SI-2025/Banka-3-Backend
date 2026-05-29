-- Employees and clients are stored in separate tables. Both can log in,
-- both have a permission list, both have a session_version that's
-- bumped on actions that should immediately invalidate live tokens.

create extension if not exists "pgcrypto";

create table "user".employees (
    id              uuid primary key default gen_random_uuid(),
    email           text not null unique,
    username        text not null unique,
    password_hash   text,                       -- null until activated
    first_name      text not null,
    last_name       text not null,
    date_of_birth   date not null,
    gender          text not null check (gender in ('male', 'female', 'other')),
    phone           text not null,
    address         text not null,
    position        text not null,
    department      text not null,
    active          boolean not null default true,
    permissions     text[] not null default '{}',
    session_version bigint not null default 1,
    created_at      timestamptz not null default now(),
    updated_at      timestamptz not null default now()
);

create index employees_last_name_idx on "user".employees (lower(last_name));
create index employees_position_idx  on "user".employees (lower(position));

create table "user".clients (
    id              uuid primary key default gen_random_uuid(),
    email           text not null unique,
    password_hash   text,
    first_name      text not null,
    last_name       text not null,
    date_of_birth   date not null,
    gender          text not null check (gender in ('male', 'female', 'other')),
    phone           text not null,
    address         text not null,
    active          boolean not null default true,
    permissions     text[] not null default '{}',
    session_version bigint not null default 1,
    created_at      timestamptz not null default now(),
    updated_at      timestamptz not null default now()
);

create index clients_last_name_idx on "user".clients (lower(last_name));

-- Refresh tokens. We store the sha256 hash of the random plaintext, so
-- a database leak doesn't expose live tokens.
create table "user".refresh_tokens (
    id         uuid primary key default gen_random_uuid(),
    user_id    uuid not null,
    user_kind  text not null check (user_kind in ('employee', 'client')),
    token_hash text not null unique,
    expires_at timestamptz not null,
    revoked_at timestamptz,
    created_at timestamptz not null default now()
);

create index refresh_tokens_user_idx on "user".refresh_tokens (user_kind, user_id);

-- Activation tokens for employees. Sent in the welcome email after
-- admin creates the account; consumed when the employee sets their
-- initial password.
create table "user".activation_tokens (
    id          uuid primary key default gen_random_uuid(),
    employee_id uuid not null references "user".employees(id) on delete cascade,
    token_hash  text not null unique,
    expires_at  timestamptz not null,
    used_at     timestamptz,
    created_at  timestamptz not null default now()
);

create index activation_tokens_employee_idx on "user".activation_tokens (employee_id);

-- Password reset tokens for either user kind.
create table "user".password_reset_tokens (
    id         uuid primary key default gen_random_uuid(),
    user_id    uuid not null,
    user_kind  text not null check (user_kind in ('employee', 'client')),
    token_hash text not null unique,
    expires_at timestamptz not null,
    used_at    timestamptz,
    created_at timestamptz not null default now()
);

create index password_reset_tokens_user_idx on "user".password_reset_tokens (user_kind, user_id);

-- updated_at trigger
create or replace function "user".set_updated_at() returns trigger as $$
begin
    new.updated_at = now();
    return new;
end;
$$ language plpgsql;

create trigger employees_updated_at before update on "user".employees
    for each row execute procedure "user".set_updated_at();

create trigger clients_updated_at before update on "user".clients
    for each row execute procedure "user".set_updated_at();
