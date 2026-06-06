-- C3 — Forex Forwards (terminski valutni ugovori, todoSpec).
--
-- A forex forward is a contract between a client and the bank that fixes
-- TODAY a rate for a FUTURE currency conversion. On the settlement date
-- the bank debits the client's RSD account by ForwardRate × Notional and
-- credits the Notional in the quote currency to the client's quote
-- account — a DIRECT fixed-rate move, not a menjačnica RSD round-trip
-- (the spread risk is already priced into ForwardRate).
--
-- Two tables:
--   * forex_forward_spreads — per-pair SpreadFactor, set by supervisors.
--   * forex_forwards        — the concluded contracts.

-- Per-currency-pair annualised spread factor used in the forward-rate
-- formula. base_currency is the currency the client buys forward (the
-- Notional currency); quote_currency is RSD (the leg debited at
-- settlement). Editable by supervisors only (service-layer gated).
create table "bank".forex_forward_spreads (
    base_currency  text           not null,
    quote_currency text           not null,
    spread_factor  numeric(10, 6) not null,
    updated_by     uuid,
    updated_at     timestamptz    not null default now(),
    primary key (base_currency, quote_currency)
);

-- A concluded forward contract.
--
-- forward_rate is locked at conclusion via the spec formula:
--   ForwardRate = SpotAskRate × (1 + (DaysToSettlement / 365) × SpreadFactor)
-- The quote-currency obligation (forward_rate × notional) is reserved on
-- the client's quote (RSD) account through the existing reservation
-- primitive; reservation_id stores that reservation's op_id so the
-- settlement / cancel paths can commit / release it. A commission is
-- charged at conclusion.
create table "bank".forex_forwards (
    id                  uuid primary key default gen_random_uuid(),
    client_id           uuid           not null,
    base_currency       text           not null,
    quote_currency      text           not null,
    notional            numeric(20, 4) not null,
    forward_rate        numeric(20, 8) not null,
    spot_ask_rate       numeric(20, 8) not null,
    spread_factor       numeric(10, 6) not null,
    days_to_settlement  int            not null,
    commission          numeric(20, 4) not null default 0,
    reservation_id      text           not null,
    from_account_id     uuid           not null, -- client's quote (RSD) account, debited at settlement
    to_account_id       uuid           not null, -- client's base-currency account, credited at settlement
    settlement_date     timestamptz    not null,
    status              text           not null default 'active'
        check (status in ('active', 'settled', 'cancelled', 'failed')),
    failure_reason      text,
    created_at          timestamptz    not null default now(),
    settled_at          timestamptz
);

-- The settlement sweep selects on (status, settlement_date).
create index forex_forwards_due_idx
    on "bank".forex_forwards (status, settlement_date);

-- The client's list view selects on client_id.
create index forex_forwards_client_idx
    on "bank".forex_forwards (client_id);

-- Extend transactions.op_kind so the forward-settlement ledger leg is
-- auditable. Same drop-and-recreate pattern as 0008/0009/0010/0012/0014/0016.
alter table "bank".transactions
  drop constraint transactions_op_kind_check,
  add constraint transactions_op_kind_check
    check (op_kind in (
      'payment','transfer','exchange','fee',
      'loan_disbursement','loan_installment',
      'trade','tax','forex_fill',
      'otc_premium','otc_exercise','fund_invest','fund_withdraw',
      'interbank_payment','dividend','forex_forward'
    ));
