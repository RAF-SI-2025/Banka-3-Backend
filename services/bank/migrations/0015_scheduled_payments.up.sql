-- C2 — Zakazivanje plaćanja (scheduled payments, todoSpec C2).
--
-- A one-time future-dated intra-bank payment. The client schedules it
-- via POST /api/v1/scheduled-payments (verification-gated at the
-- gateway); the row sits in status 'scheduled' until its scheduled_date.
-- The central scheduler's bank-scheduled-payments sweep picks up due
-- rows and attempts execution: enough funds → 'completed' + notify,
-- insufficient funds → 'failed' + notify. The client can cancel a
-- 'scheduled' row before it executes.
--
-- The payment-shaped columns mirror bank.CreatePayment's input
-- (to_account_number / amount / currency / recipient_name /
-- payment_code / purpose / model / reference_number) so the sweep can
-- replay the exact same intra-bank money-move at execution time.
create table "bank".scheduled_payments (
    id                uuid primary key default gen_random_uuid(),
    client_id         uuid           not null,
    from_account_id   uuid           not null,
    to_account_number text           not null,
    amount            numeric(20, 4) not null,
    currency          text           not null,
    recipient_name    text           not null,
    payment_code      text,
    purpose           text,
    model             text,
    reference_number  text,
    scheduled_date    timestamptz    not null,
    status            text           not null default 'scheduled'
        check (status in ('scheduled', 'completed', 'failed', 'cancelled')),
    failure_reason    text,
    created_at        timestamptz    not null default now(),
    executed_at       timestamptz
);

-- The due-sweep selects on (status, scheduled_date).
create index scheduled_payments_due_idx
    on "bank".scheduled_payments (status, scheduled_date);

-- The client's list view selects on client_id.
create index scheduled_payments_client_idx
    on "bank".scheduled_payments (client_id);
