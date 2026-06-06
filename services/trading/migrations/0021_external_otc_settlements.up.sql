-- Cross-bank OTC option settlement 2PC tracking (Banka-4 protocol-notes
-- §2/§3). When a partner POSTs a NEW_TX that settles an accepted option
-- (premium MONAS + option-right OPTION asset) or an exercise (strike
-- MONAS + shares STOCK through an OPTION escrow account), the gateway
-- routes it here so trading can perform the seller-side effects, and so
-- the following COMMIT_TX / ROLLBACK_TX can be routed back to trading.
-- (Plain cash transfers stay in bank.interbank_protocol_transactions.)
--
-- Keyed by the partner's idempotency tuple (sender_routing_number,
-- transaction_id) so a re-delivered NEW_TX returns the existing row.

create table "trading".external_otc_settlements (
    sender_routing_number int            not null,
    transaction_id        text           not null,
    kind                  text           not null check (kind in ('accept', 'exercise')),
    status                text           not null check (status in ('prepared', 'committed', 'rolled_back')),
    option_ref            text           not null,        -- negotiationId (accept) / contractId (exercise)
    contract_id           uuid,                           -- our local external_otc_contracts row, once formed
    quantity              bigint         not null default 0,
    cash_amount           numeric(30,10) not null default 0,
    cash_currency         text           not null default '',
    op_id                 uuid,                           -- bank cash-leg op id, stamped on commit
    created_at            timestamptz    not null default now(),
    updated_at            timestamptz    not null default now(),
    primary key (sender_routing_number, transaction_id)
);
