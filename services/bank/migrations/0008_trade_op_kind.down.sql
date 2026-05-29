alter table "bank".transactions
  drop constraint transactions_op_kind_check,
  add constraint transactions_op_kind_check
    check (op_kind in (
      'payment','transfer','exchange','fee',
      'loan_disbursement','loan_installment'
    ));
