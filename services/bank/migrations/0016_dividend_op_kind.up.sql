-- Extend transactions.op_kind to cover the quarterly dividend payout
-- (todoSpec C3 S54-S59). The trading service's dividend cron credits
-- each holder's account from the bank's house account via the
-- SettleDividend RPC; tagging the resulting ledger leg 'dividend' keeps
-- it distinguishable from regular trade settlement and FX conversions.
-- Same drop-and-recreate pattern as 0008/0009/0010/0012/0014.
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
