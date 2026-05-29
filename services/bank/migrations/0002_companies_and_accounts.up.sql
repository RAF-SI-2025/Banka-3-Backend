-- Slice 1 of celina 2: companies, accounts, system (house) accounts.
-- Cards / payments / loans land in subsequent migrations.

create extension if not exists "pgcrypto";

-- =====================================================================
-- Companies (Firma) — needed for Poslovni accounts.
-- Spec p.14: matični broj + PIB are unique national identifiers; we
-- enforce uniqueness in the DB so two records can't claim the same
-- company.
-- =====================================================================
create table "bank".companies (
    id              uuid primary key default gen_random_uuid(),
    name            text not null,
    registry_id     text not null unique,         -- Matični broj (8 digits)
    tax_id          text not null unique,         -- PIB (9 digits)
    activity_code   text not null,                -- Šifra delatnosti (xx.xx)
    address         text not null,
    -- The owner is a Klijent in the user service. Cross-schema FK is
    -- forbidden by convention (services own their data); we store the
    -- UUID and rely on the user service for validity.
    owner_client_id uuid not null,
    created_at      timestamptz not null default now(),
    updated_at      timestamptz not null default now()
);

create index companies_owner_idx on "bank".companies (owner_client_id);
create index companies_name_idx  on "bank".companies (lower(name));

-- =====================================================================
-- Accounts.
-- =====================================================================
--
-- `kind` matches pkg/account.Type — keep these check-constraint values
-- in sync with the Go constants. `subtype` is only meaningful for
-- personal-checking-RSD; other kinds carry 'unspecified'.
--
-- balance vs available_balance: balance is the actual ledger value;
-- available_balance is balance minus currently-reserved holds. The
-- payment flow decrements available immediately and balance on
-- settlement.
--
-- Spec p.12: limits and spending counters are at the account level so
-- the FE can show "preostalo" without recomputing.
create table "bank".accounts (
    id                       uuid primary key default gen_random_uuid(),
    number                   text not null unique,
    name                     text not null,
    owner_client_id          uuid not null,
    company_id               uuid references "bank".companies(id) on delete restrict,
    created_by_employee_id   uuid not null,

    kind                     text not null check (kind in (
        'personal_checking_rsd', 'personal_fx',
        'business_checking_rsd', 'business_fx',
        'system'
    )),
    subtype                  text not null default 'unspecified' check (subtype in (
        'unspecified',
        'standard', 'savings', 'pensioner', 'youth', 'student', 'unemployed',
        'doo', 'ad', 'foundation'
    )),
    currency                 text not null check (currency in (
        'RSD','EUR','CHF','USD','GBP','JPY','CAD','AUD'
    )),
    status                   text not null default 'active' check (status in ('active','inactive')),

    balance                  numeric(20,4) not null default 0,
    available_balance        numeric(20,4) not null default 0,
    maintenance_fee          numeric(20,4) not null default 0,
    daily_limit              numeric(20,4) not null default 0,
    monthly_limit            numeric(20,4) not null default 0,
    daily_spent              numeric(20,4) not null default 0,
    monthly_spent            numeric(20,4) not null default 0,

    created_at               timestamptz not null default now(),
    expires_at               timestamptz,
    updated_at               timestamptz not null default now(),

    -- Business accounts must point at a company; personal must not.
    constraint accounts_company_consistency check (
        (kind in ('business_checking_rsd','business_fx') and company_id is not null) or
        (kind not in ('business_checking_rsd','business_fx') and company_id is null)
    ),
    -- Tekući RSD personal accounts must declare a non-unspecified subtype.
    constraint accounts_subtype_required check (
        kind <> 'personal_checking_rsd' or subtype <> 'unspecified'
    )
);

create index accounts_owner_idx     on "bank".accounts (owner_client_id);
create index accounts_company_idx   on "bank".accounts (company_id);
create index accounts_currency_idx  on "bank".accounts (currency);
create index accounts_kind_idx      on "bank".accounts (kind);

-- =====================================================================
-- System (house) accounts.
-- The bank holds one account per supported currency for FX flows
-- (every cross-currency conversion routes through the house RSD
-- account, then through the destination-currency house account). We
-- do NOT seed actual numbers here — the bank service's "ensure house
-- accounts" startup hook generates checksum-clean numbers via
-- pkg/account on first boot and writes them through the same accounts
-- table with kind='system' and a sentinel owner.
-- =====================================================================

-- Sentinel client UUID for the bank itself. Stored as a nil/zero UUID
-- so it doesn't collide with any real Klijent. Code that joins to
-- "user".clients should skip rows where owner_client_id is this.
-- (Documented here rather than enforced — cross-schema FKs are
-- forbidden by convention.)
comment on column "bank".accounts.owner_client_id is
    'UUID of the owning Klijent in the user service. The reserved value 00000000-0000-0000-0000-000000000000 marks bank-owned (system) accounts.';
