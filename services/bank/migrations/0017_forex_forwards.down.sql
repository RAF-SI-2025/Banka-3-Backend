alter table "bank".transactions
  drop constraint transactions_op_kind_check,
  add constraint transactions_op_kind_check
    check (op_kind in (
      'payment','transfer','exchange','fee',
      'loan_disbursement','loan_installment',
      'trade','tax','forex_fill',
      'otc_premium','otc_exercise','fund_invest','fund_withdraw',
      'interbank_payment','dividend'
    ));

drop table if exists "bank".forex_forwards;
drop table if exists "bank".forex_forward_spreads;
