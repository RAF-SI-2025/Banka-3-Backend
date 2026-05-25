-- c5 — Inter-bank 2-phase commit primitive (spec p.77+).
--
-- Two tables:
--   * interbank_protocol_transactions — one row per cross-bank 2PC
--     transaction. NEW_TX (prepare) inserts; COMMIT_TX or ROLLBACK_TX
--     flips status and stamps the bank.transactions op_id (commit
--     path) or the rollback timestamp.
--   * interbank_protocol_messages — verbatim audit of inbound partner
--     messages keyed by (sender_routing_number, idempotence_key) so
--     replayed messages get the stored response back without re-running
--     the underlying Prepare/Commit/Rollback.
--
-- Reservations (`bank.reservations`, 0012) are the cash-side primitive
-- for outbound legs: PreparePayment with direction='outbound' debits
-- the source account into a held reservation, CommitPayment moves the
-- reservation to a real bank.transactions leg, RollbackPayment releases.
-- Inbound legs don't reserve — they go straight to a credit leg at
-- commit time (the partner's debit is the funds-availability guarantee).
--
-- Both transactions and messages keep their primary key on
-- (sender_routing_number, …) so when this service later hash-partitions
-- on sender (BONUS-Part), no rewrite is needed.

create table "bank".interbank_protocol_transactions (
    sender_routing_number integer        not null,
    transaction_id        text           not null,

    direction             text           not null check (direction in ('inbound','outbound')),

    -- 18-digit account numbers on each side. Spec p.107 mandates the
    -- shared sum(digits)%11 checksum; the gateway validates before
    -- this row gets inserted. The check below just pins the length.
    local_account_number  text           not null check (length(local_account_number) = 18),
    remote_account_number text           not null check (length(remote_account_number) = 18),
    currency              text           not null,
    amount                numeric(20,4)  not null check (amount > 0),
    purpose               text           not null default '',

    -- Verbatim partner request body. Audit + replay.
    transaction_body      text           not null default '',

    -- bank.reservations.id when direction='outbound' and prepare
    -- succeeded. Null for inbound. No FK so deleting / partitioning
    -- reservations later doesn't cascade here; the row is audit-grade.
    reservation_id        uuid,

    -- bank.transactions op_id assigned at commit. Null until commit.
    op_id                 uuid,

    status                text           not null check (status in (
        'prepared','committed','rolled_back'
    )),
    last_error            text           not null default '',

    created_at            timestamptz    not null default now(),
    updated_at            timestamptz    not null default now(),

    primary key (sender_routing_number, transaction_id)
);

-- Sweep stuck-prepared rows (for the timeout-rollback worker).
create index interbank_protocol_transactions_status_idx
    on "bank".interbank_protocol_transactions (status, updated_at);


create table "bank".interbank_protocol_messages (
    sender_routing_number integer     not null,
    idempotence_key       text        not null,

    message_type          text        not null check (message_type in (
        'NEW_TX','COMMIT_TX','ROLLBACK_TX'
    )),

    -- The transaction_id this message acted on. Empty for malformed
    -- requests we still stash to keep replay deterministic.
    transaction_id        text        not null default '',

    response_status       integer     not null check (response_status in (200, 202, 204)),
    response_body         text        not null default '',

    created_at            timestamptz not null default now(),
    updated_at            timestamptz not null default now(),

    primary key (sender_routing_number, idempotence_key)
);


-- Extend transactions.op_kind so cross-bank legs are auditable. Same
-- drop-and-recreate pattern as 0008/0009/0010/0012.
alter table "bank".transactions
  drop constraint transactions_op_kind_check,
  add constraint transactions_op_kind_check
    check (op_kind in (
      'payment','transfer','exchange','fee',
      'loan_disbursement','loan_installment',
      'trade','tax','forex_fill',
      'otc_premium','otc_exercise','fund_invest','fund_withdraw',
      'interbank_payment'
    ));
