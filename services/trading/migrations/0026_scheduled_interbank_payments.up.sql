-- Scheduled / periodic inter-bank payments (celina 5 — todoSpec
-- "Scheduled/periodic inter-bank payments").
--
-- A client schedules a cross-bank cash payment to run once on a future
-- date or to repeat on a cadence (DAILY/WEEKLY/MONTHLY). Spec example:
-- "Svakog prvog u mesecu poslati 400 EUR na dati račun."
--
-- On each next_run the scheduler job `trading-scheduled-interbank` (a
-- daily sweep) drives the EXISTING SubmitCrossBankPayment path under the
-- row's owner principal, then advances next_run via schedule.AfterRun
-- (ONCE → active=false; recurring → next future slot). Pausing flips
-- active=false; cancelling deactivates the row permanently.
--
-- The sweep walks every due+active row, so (active, next_run) is indexed.

create table "trading".scheduled_interbank_payments (
    id                  uuid primary key default gen_random_uuid(),
    user_id             text not null,
    user_kind           text not null,
    source_account_id   uuid not null,
    dest_bank_code      text not null,
    dest_account_number text not null,
    currency            text not null,
    amount              numeric(20, 4) not null check (amount > 0),
    purpose             text not null default '',
    cadence             text not null
                        check (cadence in ('ONCE', 'DAILY', 'WEEKLY', 'MONTHLY')),
    next_run            timestamptz not null,
    active              boolean not null default true,
    -- last_status / last_error surface the most recent run's outcome to
    -- the FE list (running | completed | failed). Empty until first run.
    last_status         text not null default '',
    last_error          text not null default '',
    last_run_at         timestamptz,
    created_at          timestamptz not null default now(),
    updated_at          timestamptz not null default now()
);

create index scheduled_interbank_payments_active_next_run_idx
    on "trading".scheduled_interbank_payments (active, next_run);
create index scheduled_interbank_payments_user_id_idx
    on "trading".scheduled_interbank_payments (user_id);
