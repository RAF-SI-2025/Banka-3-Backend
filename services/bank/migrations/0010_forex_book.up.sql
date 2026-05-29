-- Spec p.42: forex pairs are not held — buying a pair means selling
-- one currency and buying the other. The trading service routes a
-- forex fill as two paired SettleTrade-shaped legs whose "user" side
-- is a bank-owned forex_book account (one per supported currency).
-- The forex_book represents the bank's open FX positions; its
-- counterparty in each leg is the existing per-currency house
-- (KindSystem) account.
--
-- 1. accounts.kind grows 'forex_book' so the seeded book accounts
--    stay distinct from menjačnica system accounts (which would
--    otherwise collide via GetSystemAccount).
-- 2. transactions.op_kind grows 'forex_fill' so the resulting ledger
--    legs are distinguishable from normal trade settlement.
alter table "bank".accounts
  drop constraint accounts_kind_check,
  add constraint accounts_kind_check
    check (kind in (
      'personal_checking_rsd', 'personal_fx',
      'business_checking_rsd', 'business_fx',
      'system', 'state_tax', 'forex_book'
    ));

alter table "bank".transactions
  drop constraint transactions_op_kind_check,
  add constraint transactions_op_kind_check
    check (op_kind in (
      'payment','transfer','exchange','fee',
      'loan_disbursement','loan_installment',
      'trade','tax','forex_fill'
    ));
