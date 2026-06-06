-- c5 — Inter-bank real-time status tracking (spec p.77+).
--
-- Widen the interbank_protocol_transactions.status CHECK to admit the
-- two non-terminal/terminal-failure states the supervisor status view
-- surfaces:
--   * 'pending' — transient pre-prepare state (the cross-bank SAGA may
--     write a row before its prepare decision is reached).
--   * 'failed'  — prepare rejected (validation / insufficient funds /
--     blacklisted partner). Previously such an attempt left no row at
--     all, so the supervisor couldn't see it; PreparePayment now records
--     the failed attempt for audit + real-time tracking.
--
-- Drop-and-recreate the named constraint (same pattern as 0014's
-- op_kind widening).

alter table "bank".interbank_protocol_transactions
  drop constraint interbank_protocol_transactions_status_check,
  add constraint interbank_protocol_transactions_status_check
    check (status in (
      'pending','failed','prepared','committed','rolled_back'
    ));
