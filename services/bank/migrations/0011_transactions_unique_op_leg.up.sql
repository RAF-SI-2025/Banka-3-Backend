-- Spec-correctness: idempotency on op_id was previously enforced by a
-- read-then-write check (`SELECT … WHERE op_id = $1` then INSERT) inside
-- service.SettleTrade / SettleForexFill / SettleCapitalGainsTax. That's
-- a TOCTOU window — two concurrent retries with the same op_id can both
-- pass the read and double-insert.
--
-- Adding a unique constraint on (op_id, leg_index) lets the database
-- arbitrate. Service code can then `ON CONFLICT DO NOTHING` (or treat a
-- conflict as "already settled") and retries become safe by construction.
--
-- Replaces the existing non-unique transactions_op_idx which served only
-- as a lookup index — the unique index covers the same lookups.
drop index "bank".transactions_op_idx;
alter table "bank".transactions
  add constraint transactions_op_id_leg_index_key unique (op_id, leg_index);
