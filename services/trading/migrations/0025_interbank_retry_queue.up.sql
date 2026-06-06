-- Inter-bank retry queue (celina 5 — todoSpec "Retry queue").
--
-- When a partner bank is unavailable mid cross-bank payment, the saga
-- parks (status=running) and the originating request enqueues one row
-- here. A worker (scheduler job `trading-interbank-retry`, every 5s)
-- re-drives every pending entry whose next_retry_at <= now by resuming
-- the underlying saga:
--   * saga completed  → status='succeeded'
--   * still parked     → next_retry_at = now + 5s (retry again)
--   * now > deadline_at (created + 30s) → status='failed', the saga is
--     rolled back and the client is notified the transaction failed.
--
-- One row per saga (transaction_id) — re-enqueue is an idempotent
-- upsert that re-arms a pending entry instead of inserting a duplicate.

create table "trading".interbank_retry_queue (
    id              uuid primary key default gen_random_uuid(),
    -- saga transaction_id this entry drives (also the bank-side
    -- interbank tx id). Unique so re-enqueue is idempotent.
    transaction_id  uuid not null unique,
    -- partner routing/bank code, kept for observability + the failure
    -- notification copy.
    partner_bank_code text not null,
    -- operation being retried; currently always 'cross_bank_payment'.
    operation       text not null default 'cross_bank_payment',
    -- who to notify on terminal failure.
    user_id         text not null,
    user_kind       text not null,
    attempt_count   int  not null default 0,
    next_retry_at   timestamptz not null,
    -- hard cut-off: created_at + 30s. Past this the entry fails.
    deadline_at     timestamptz not null,
    status          text not null default 'pending'
                    check (status in ('pending', 'succeeded', 'failed')),
    last_error      text not null default '',
    created_at      timestamptz not null default now(),
    updated_at      timestamptz not null default now()
);

-- The worker's working set: pending entries ordered by when they're due.
create index interbank_retry_queue_due_idx
    on "trading".interbank_retry_queue (status, next_retry_at);
