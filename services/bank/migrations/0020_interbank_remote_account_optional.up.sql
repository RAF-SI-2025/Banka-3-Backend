-- remote_account_number is audit-only (the money moves between
-- local_account_number and the bank's per-currency system account). It is
-- 18 digits for account-addressed cash payments, but EMPTY for PERSON-
-- addressed OTC settlements (si-tx-proto / Banka-2), where the counterparty
-- is identified by foreign-bank id, not an account number. Relax the CHECK
-- from "exactly 18" to "18 or empty".
alter table "bank".interbank_protocol_transactions
  drop constraint interbank_protocol_transactions_remote_account_number_check;

alter table "bank".interbank_protocol_transactions
  add constraint interbank_protocol_transactions_remote_account_number_check
  check (remote_account_number = '' or length(remote_account_number) = 18);
