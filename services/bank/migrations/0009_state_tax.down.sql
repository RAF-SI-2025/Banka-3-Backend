alter table "bank".transactions
  drop constraint transactions_op_kind_check,
  add constraint transactions_op_kind_check
    check (op_kind in (
      'payment','transfer','exchange','fee',
      'loan_disbursement','loan_installment',
      'trade'
    ));

alter table "bank".accounts
  drop constraint accounts_kind_check,
  add constraint accounts_kind_check
    check (kind in (
      'personal_checking_rsd', 'personal_fx',
      'business_checking_rsd', 'business_fx',
      'system'
    ));
