-- Revert the dividend op_kind back to the 0014 set. Rows tagged
-- 'dividend' would violate the narrower constraint, so drop them first.
delete from "bank".transactions where op_kind = 'dividend';

alter table "bank".transactions
  drop constraint transactions_op_kind_check,
  add constraint transactions_op_kind_check
    check (op_kind in (
      'payment','transfer','exchange','fee',
      'loan_disbursement','loan_installment',
      'trade','tax','forex_fill',
      'otc_premium','otc_exercise','fund_invest','fund_withdraw',
      'interbank_payment'
    ));
