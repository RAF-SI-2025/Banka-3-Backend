alter table "bank".interbank_protocol_transactions
  drop constraint interbank_protocol_transactions_remote_account_number_check;

alter table "bank".interbank_protocol_transactions
  add constraint interbank_protocol_transactions_remote_account_number_check
  check (length(remote_account_number) = 18);
