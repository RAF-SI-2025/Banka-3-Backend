-- Spec p.35: when an installment debit fails (insufficient funds), the
-- system retries 72 hours later. If the retry also fails, the loan's
-- base rate is bumped by +0.05% as a late-payment penalty. Subsequent
-- failures keep retrying every 72 hours but don't re-apply the bump.
--
-- We track two new columns:
--   - loan_installments.overdue_since: timestamp of the first failed
--     debit attempt, used to schedule the 72h retry. NULL until the
--     first failure.
--   - loans.late_penalty_applied: idempotency flag for the +0.05% bump.

alter table "bank".loan_installments
    add column overdue_since timestamptz;

alter table "bank".loans
    add column late_penalty_applied boolean not null default false;

-- Partial index for the cron's "overdue rows whose retry window has
-- elapsed" scan; unpaid rows hit the existing expected_due_date path.
create index loan_installments_overdue_retry_idx
    on "bank".loan_installments (overdue_since)
    where status = 'overdue';
