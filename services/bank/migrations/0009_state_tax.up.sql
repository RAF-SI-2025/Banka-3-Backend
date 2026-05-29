-- Spec p.62: capital-gains tax cron debits 15% of profit (in RSD) from
-- the user's account into the state's RSD account. Wire this through
-- two extensions:
--   1. accounts.kind grows a new 'state_tax' value so the seeded state
--      account stays out of the menjačnica's bank-house lookups.
--   2. transactions.op_kind grows 'tax' so the resulting ledger leg is
--      distinguishable from regular trade settlement and from FX
--      conversions.
alter table "bank".accounts
  drop constraint accounts_kind_check,
  add constraint accounts_kind_check
    check (kind in (
      'personal_checking_rsd', 'personal_fx',
      'business_checking_rsd', 'business_fx',
      'system', 'state_tax'
    ));

alter table "bank".transactions
  drop constraint transactions_op_kind_check,
  add constraint transactions_op_kind_check
    check (op_kind in (
      'payment','transfer','exchange','fee',
      'loan_disbursement','loan_installment',
      'trade','tax'
    ));
