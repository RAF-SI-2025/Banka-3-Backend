drop index if exists "bank".loan_installments_overdue_retry_idx;
alter table "bank".loans
    drop column if exists late_penalty_applied;
alter table "bank".loan_installments
    drop column if exists overdue_since;
