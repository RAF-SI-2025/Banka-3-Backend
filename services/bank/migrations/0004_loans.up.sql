-- Slice 3 of celina 2: krediti (loans).
--
-- Three tables: loan_requests (client submits), loans (created on
-- employee approval), loan_installments (history of paid + future
-- scheduled installments per spec p.32 "potrebno je da se čuva
-- istorija rata + 1 rata u budućnosti").
--
-- Rate math is persisted on the loan row: base_rate (osnovica from
-- the amount-bracket table), margin (marža banke from the type
-- table), and current_offset (pomeraj — only meaningful for variable;
-- updated by the monthly cron). Effective monthly rate is
-- (base + offset + margin) / 12 — derived, not stored.
--
-- Loan currency = account currency (enforced in service); the brackets
-- are RSD-denominated, so non-RSD loans use the menjačnica BID to
-- compute their RSD-equivalent for bracket lookup (per spec p.33).

-- Extend transactions.op_kind to cover loan flows. We use the same
-- ledger so "Pregled plaćanja" automatically renders disbursement and
-- installment lines as transactions of the loan kind.
alter table "bank".transactions
  drop constraint transactions_op_kind_check,
  add constraint transactions_op_kind_check
    check (op_kind in ('payment','transfer','exchange','fee','loan_disbursement','loan_installment'));

create table "bank".loan_requests (
    id                       uuid primary key default gen_random_uuid(),
    client_id                uuid not null,
    account_id               uuid not null references "bank".accounts(id) on delete restrict,

    loan_type                text not null check (loan_type in (
        'cash','housing','auto','refinance','student'
    )),
    interest_type            text not null check (interest_type in ('fixed','variable')),

    amount                   numeric(20,4) not null check (amount > 0),
    currency                 text not null check (currency in (
        'RSD','EUR','CHF','USD','GBP','JPY','CAD','AUD'
    )),
    purpose                  text,
    monthly_salary           numeric(20,4),
    employment_status        text not null check (employment_status in (
        'permanent','temporary','unemployed'
    )),
    employment_duration_months int,
    installments_total       int not null check (installments_total > 0),
    contact_phone            text,

    status                   text not null default 'pending' check (status in (
        'pending','approved','rejected'
    )),
    rejection_reason         text,
    decided_at               timestamptz,
    decided_by_employee_id   uuid,

    created_at               timestamptz not null default now(),
    updated_at               timestamptz not null default now()
);

create index loan_requests_client_idx  on "bank".loan_requests (client_id);
create index loan_requests_status_idx  on "bank".loan_requests (status);
create index loan_requests_created_idx on "bank".loan_requests (created_at desc);

create table "bank".loans (
    id                        uuid primary key default gen_random_uuid(),
    -- request_id is set when the loan was created via approval (most
    -- common). Kept nullable for the rare admin-created direct loan,
    -- though we don't currently expose that path.
    request_id                uuid references "bank".loan_requests(id),

    loan_number               text not null unique,
    client_id                 uuid not null,
    account_id                uuid not null references "bank".accounts(id) on delete restrict,

    loan_type                 text not null,
    interest_type             text not null,

    principal                 numeric(20,4) not null check (principal > 0),
    currency                  text not null,

    -- Rate components (annual percentages stored as numeric, e.g.
    -- 6.2500). r_monthly = (base_rate + current_offset + margin) / 12 / 100.
    base_rate                 numeric(8,4) not null,
    margin                    numeric(8,4) not null,
    current_offset            numeric(8,4) not null default 0,

    installments_total        int  not null,
    installment_amount        numeric(20,4) not null,
    remaining_principal       numeric(20,4) not null,

    next_installment_date     date,
    next_installment_amount   numeric(20,4),

    status                    text not null default 'approved' check (status in (
        'approved','rejected','paid_off','overdue'
    )),

    contracted_at             timestamptz not null default now(),
    matures_at                date,
    created_at                timestamptz not null default now(),
    updated_at                timestamptz not null default now()
);

create index loans_client_idx  on "bank".loans (client_id);
create index loans_account_idx on "bank".loans (account_id);
create index loans_status_idx  on "bank".loans (status);
create index loans_next_due_idx on "bank".loans (next_installment_date)
    where status in ('approved','overdue');

create table "bank".loan_installments (
    id                   uuid primary key default gen_random_uuid(),
    loan_id              uuid not null references "bank".loans(id) on delete cascade,
    sequence_number      int  not null,
    amount               numeric(20,4) not null,
    -- The annual-percent rate that was in effect when this installment
    -- was generated. Stored for audit on variable-rate loans (the
    -- "Iznos kamatne stope" column on spec p.32).
    interest_rate_at_due numeric(8,4) not null,
    currency             text not null,

    expected_due_date    date not null,
    actual_paid_at       timestamptz,
    status               text not null default 'unpaid' check (status in (
        'paid','unpaid','overdue'
    )),

    created_at           timestamptz not null default now(),
    updated_at           timestamptz not null default now(),
    unique (loan_id, sequence_number)
);

create index loan_installments_loan_idx     on "bank".loan_installments (loan_id);
create index loan_installments_due_idx      on "bank".loan_installments (expected_due_date)
    where status in ('unpaid','overdue');
