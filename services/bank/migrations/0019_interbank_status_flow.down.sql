-- Revert to the original three-state CHECK. Rows still in 'pending' or
-- 'failed' would violate it, so clear them first (audit-grade rows; safe
-- to drop on a down-migration).
delete from "bank".interbank_protocol_transactions
  where status in ('pending','failed');

alter table "bank".interbank_protocol_transactions
  drop constraint interbank_protocol_transactions_status_check,
  add constraint interbank_protocol_transactions_status_check
    check (status in (
      'prepared','committed','rolled_back'
    ));
