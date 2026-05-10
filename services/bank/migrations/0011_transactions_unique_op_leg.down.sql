alter table "bank".transactions
  drop constraint transactions_op_id_leg_index_key;
create index transactions_op_idx on "bank".transactions (op_id);
