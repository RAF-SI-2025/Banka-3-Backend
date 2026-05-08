drop table if exists "bank".loan_installments;
drop table if exists "bank".loans;
drop table if exists "bank".loan_requests;

alter table "bank".transactions
  drop constraint if exists transactions_op_kind_check,
  add constraint transactions_op_kind_check
    check (op_kind in ('payment','transfer','exchange','fee'));
