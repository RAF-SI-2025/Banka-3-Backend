package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
	"github.com/jackc/pgx/v5"
)

// =====================================================================
// Loan requests
// =====================================================================

const loanRequestColumns = `
    id, client_id, account_id, loan_type, interest_type,
    amount::text, currency, coalesce(purpose, ''),
    coalesce(monthly_salary::text, ''), employment_status,
    coalesce(employment_duration_months, 0), installments_total,
    coalesce(contact_phone, ''), status, coalesce(rejection_reason, ''),
    decided_at, coalesce(decided_by_employee_id::text, ''),
    created_at
`

func scanLoanRequest(row interface{ Scan(...any) error }) (*domain.LoanRequest, error) {
	var r domain.LoanRequest
	var loanType, interestType, currency, status, employmentStatus string
	var decidedAt *time.Time
	if err := row.Scan(
		&r.ID, &r.ClientID, &r.AccountID, &loanType, &interestType,
		&r.Amount, &currency, &r.Purpose,
		&r.MonthlySalary, &employmentStatus,
		&r.EmploymentDurationMonths, &r.InstallmentsTotal,
		&r.ContactPhone, &status, &r.RejectionReason,
		&decidedAt, &r.DecidedByEmployeeID,
		&r.CreatedAt,
	); err != nil {
		return nil, err
	}
	r.LoanType = domain.LoanType(loanType)
	r.InterestType = domain.InterestType(interestType)
	r.Currency = domain.Currency(currency)
	r.EmploymentStatus = domain.EmploymentStatus(employmentStatus)
	r.Status = domain.LoanRequestStatus(status)
	r.DecidedAt = decidedAt
	return &r, nil
}

func (s *Store) CreateLoanRequest(ctx context.Context, r *domain.LoanRequest) (*domain.LoanRequest, error) {
	const q = `
        insert into "bank".loan_requests (
            client_id, account_id, loan_type, interest_type,
            amount, currency, purpose, monthly_salary, employment_status,
            employment_duration_months, installments_total, contact_phone, status
        ) values (
            $1,$2,$3,$4,$5::numeric,$6,nullif($7,''),nullif($8,'')::numeric,$9,
            nullif($10, 0), $11, nullif($12,''), 'pending'
        )
        returning ` + loanRequestColumns
	out, err := scanLoanRequest(s.Pool.QueryRow(ctx, q,
		r.ClientID, r.AccountID, string(r.LoanType), string(r.InterestType),
		r.Amount, string(r.Currency), r.Purpose, r.MonthlySalary, string(r.EmploymentStatus),
		r.EmploymentDurationMonths, r.InstallmentsTotal, r.ContactPhone,
	))
	if err != nil {
		return nil, apperr.Internal("create loan request", err)
	}
	return out, nil
}

func (s *Store) GetLoanRequestByID(ctx context.Context, id string) (*domain.LoanRequest, error) {
	const q = `select ` + loanRequestColumns + ` from "bank".loan_requests where id = $1`
	out, err := scanLoanRequest(s.Pool.QueryRow(ctx, q, id))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("zahtev za kredit ne postoji")
		}
		return nil, apperr.Internal("get loan request", err)
	}
	return out, nil
}

func (s *Store) DecideLoanRequest(ctx context.Context, tx pgx.Tx, id, employeeID string, status domain.LoanRequestStatus, reason string) (*domain.LoanRequest, error) {
	const q = `
        update "bank".loan_requests set
            status = $2, rejection_reason = nullif($3,''),
            decided_at = now(), decided_by_employee_id = $4,
            updated_at = now()
        where id = $1 and status = 'pending'
        returning ` + loanRequestColumns
	out, err := scanLoanRequest(tx.QueryRow(ctx, q, id, string(status), reason, employeeID))
	if err != nil {
		if noRows(err) {
			return nil, apperr.FailedPrecondition("zahtev je već obrađen ili ne postoji")
		}
		return nil, apperr.Internal("decide loan request", err)
	}
	return out, nil
}

func (s *Store) ListLoanRequests(ctx context.Context, f domain.LoanRequestFilter, page, pageSize int) ([]*domain.LoanRequest, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 50
	}
	var conds []string
	var args []any
	if f.Status != "" {
		args = append(args, string(f.Status))
		conds = append(conds, fmt.Sprintf("status = $%d", len(args)))
	}
	if f.LoanType != "" {
		args = append(args, string(f.LoanType))
		conds = append(conds, fmt.Sprintf("loan_type = $%d", len(args)))
	}
	if f.AccountID != "" {
		args = append(args, f.AccountID)
		conds = append(conds, fmt.Sprintf("account_id = $%d", len(args)))
	}
	if f.ClientID != "" {
		args = append(args, f.ClientID)
		conds = append(conds, fmt.Sprintf("client_id = $%d", len(args)))
	}
	where := ""
	if len(conds) > 0 {
		where = " where " + strings.Join(conds, " and ")
	}
	var total int64
	if err := s.Pool.QueryRow(ctx, `select count(*) from "bank".loan_requests`+where, args...).Scan(&total); err != nil {
		return nil, 0, apperr.Internal("count loan requests", err)
	}
	listArgs := append([]any{}, args...)
	listArgs = append(listArgs, pageSize, (page-1)*pageSize)
	listQ := `select ` + loanRequestColumns + ` from "bank".loan_requests` + where +
		fmt.Sprintf(" order by created_at desc limit $%d offset $%d", len(args)+1, len(args)+2)
	rows, err := s.Pool.Query(ctx, listQ, listArgs...)
	if err != nil {
		return nil, 0, apperr.Internal("list loan requests", err)
	}
	defer rows.Close()
	var out []*domain.LoanRequest
	for rows.Next() {
		r, err := scanLoanRequest(rows)
		if err != nil {
			return nil, 0, apperr.Internal("scan loan request", err)
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// =====================================================================
// Loans
// =====================================================================

const loanColumns = `
    id, loan_number, coalesce(request_id::text, ''), client_id, account_id,
    loan_type, interest_type,
    principal::text, currency,
    base_rate::text, margin::text, current_offset::text,
    installments_total, installment_amount::text, remaining_principal::text,
    next_installment_date, coalesce(next_installment_amount::text, ''),
    status, contracted_at, matures_at
`

func scanLoan(row interface{ Scan(...any) error }) (*domain.Loan, error) {
	var l domain.Loan
	var loanType, interestType, currency, status string
	var nextDate, maturesAt *time.Time
	if err := row.Scan(
		&l.ID, &l.LoanNumber, &l.RequestID, &l.ClientID, &l.AccountID,
		&loanType, &interestType,
		&l.Principal, &currency,
		&l.BaseRate, &l.Margin, &l.CurrentOffset,
		&l.InstallmentsTotal, &l.InstallmentAmount, &l.RemainingPrincipal,
		&nextDate, &l.NextInstallmentAmount,
		&status, &l.ContractedAt, &maturesAt,
	); err != nil {
		return nil, err
	}
	l.LoanType = domain.LoanType(loanType)
	l.InterestType = domain.InterestType(interestType)
	l.Currency = domain.Currency(currency)
	l.Status = domain.LoanStatus(status)
	l.NextInstallmentDate = nextDate
	l.MaturesAt = maturesAt
	return &l, nil
}

func (s *Store) CreateLoan(ctx context.Context, tx pgx.Tx, l *domain.Loan) (*domain.Loan, error) {
	const q = `
        insert into "bank".loans (
            request_id, loan_number, client_id, account_id, loan_type, interest_type,
            principal, currency, base_rate, margin, current_offset,
            installments_total, installment_amount, remaining_principal,
            next_installment_date, next_installment_amount, status, matures_at
        ) values (
            nullif($1,'')::uuid, $2, $3, $4, $5, $6,
            $7::numeric, $8, $9::numeric, $10::numeric, $11::numeric,
            $12, $13::numeric, $14::numeric,
            $15, nullif($16,'')::numeric, $17, $18
        )
        returning ` + loanColumns
	out, err := scanLoan(tx.QueryRow(ctx, q,
		l.RequestID, l.LoanNumber, l.ClientID, l.AccountID,
		string(l.LoanType), string(l.InterestType),
		l.Principal, string(l.Currency),
		l.BaseRate, l.Margin, l.CurrentOffset,
		l.InstallmentsTotal, l.InstallmentAmount, l.RemainingPrincipal,
		l.NextInstallmentDate, l.NextInstallmentAmount, string(l.Status), l.MaturesAt,
	))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, apperr.Conflict("loan number collision")
		}
		return nil, apperr.Internal("create loan", err)
	}
	return out, nil
}

func (s *Store) GetLoanByID(ctx context.Context, id string) (*domain.Loan, error) {
	const q = `select ` + loanColumns + ` from "bank".loans where id = $1`
	out, err := scanLoan(s.Pool.QueryRow(ctx, q, id))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("kredit ne postoji")
		}
		return nil, apperr.Internal("get loan", err)
	}
	return out, nil
}

// UpdateLoanAfterInstallment is called when an installment is paid:
// remaining_principal decreases by the principal-portion of the
// installment, the next due date rolls forward by 1 month, and
// status flips to paid_off when balance reaches 0.
func (s *Store) UpdateLoanAfterInstallment(ctx context.Context, tx pgx.Tx, loanID, newRemaining, nextAmount string, nextDate *time.Time, status domain.LoanStatus) error {
	const q = `
        update "bank".loans set
            remaining_principal = $2::numeric,
            next_installment_date = $3,
            next_installment_amount = nullif($4,'')::numeric,
            status = $5,
            updated_at = now()
        where id = $1`
	if _, err := tx.Exec(ctx, q, loanID, newRemaining, nextDate, nextAmount, string(status)); err != nil {
		return apperr.Internal("update loan", err)
	}
	return nil
}

// UpdateVariableRate refreshes pomeraj + recomputes installment.
// Used by the monthly cron.
func (s *Store) UpdateVariableRate(ctx context.Context, tx pgx.Tx, loanID, newOffset, newInstallmentAmount string) error {
	const q = `
        update "bank".loans set
            current_offset = $2::numeric,
            installment_amount = $3::numeric,
            next_installment_amount = case
                when next_installment_amount is null then null
                else $3::numeric
            end,
            updated_at = now()
        where id = $1`
	if _, err := tx.Exec(ctx, q, loanID, newOffset, newInstallmentAmount); err != nil {
		return apperr.Internal("update variable rate", err)
	}
	return nil
}

func (s *Store) ListLoans(ctx context.Context, f domain.LoanFilter, page, pageSize int) ([]*domain.Loan, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 50
	}
	var conds []string
	var args []any
	if f.ClientID != "" {
		args = append(args, f.ClientID)
		conds = append(conds, fmt.Sprintf("client_id = $%d", len(args)))
	}
	if f.AccountID != "" {
		args = append(args, f.AccountID)
		conds = append(conds, fmt.Sprintf("account_id = $%d", len(args)))
	}
	if f.LoanType != "" {
		args = append(args, string(f.LoanType))
		conds = append(conds, fmt.Sprintf("loan_type = $%d", len(args)))
	}
	if f.Status != "" {
		args = append(args, string(f.Status))
		conds = append(conds, fmt.Sprintf("status = $%d", len(args)))
	}
	where := ""
	if len(conds) > 0 {
		where = " where " + strings.Join(conds, " and ")
	}
	var total int64
	if err := s.Pool.QueryRow(ctx, `select count(*) from "bank".loans`+where, args...).Scan(&total); err != nil {
		return nil, 0, apperr.Internal("count loans", err)
	}
	listArgs := append([]any{}, args...)
	listArgs = append(listArgs, pageSize, (page-1)*pageSize)
	listQ := `select ` + loanColumns + ` from "bank".loans` + where +
		fmt.Sprintf(" order by contracted_at desc limit $%d offset $%d", len(args)+1, len(args)+2)
	rows, err := s.Pool.Query(ctx, listQ, listArgs...)
	if err != nil {
		return nil, 0, apperr.Internal("list loans", err)
	}
	defer rows.Close()
	var out []*domain.Loan
	for rows.Next() {
		l, err := scanLoan(rows)
		if err != nil {
			return nil, 0, apperr.Internal("scan loan", err)
		}
		out = append(out, l)
	}
	return out, total, rows.Err()
}

func (s *Store) ListActiveVariableLoans(ctx context.Context) ([]*domain.Loan, error) {
	const q = `select ` + loanColumns + ` from "bank".loans
              where interest_type = 'variable' and status in ('approved','overdue')
              order by id`
	rows, err := s.Pool.Query(ctx, q)
	if err != nil {
		return nil, apperr.Internal("list variable loans", err)
	}
	defer rows.Close()
	var out []*domain.Loan
	for rows.Next() {
		l, err := scanLoan(rows)
		if err != nil {
			return nil, apperr.Internal("scan variable loan", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// =====================================================================
// Installments
// =====================================================================

const installmentColumns = `
    id, loan_id, sequence_number, amount::text, interest_rate_at_due::text, currency,
    expected_due_date, actual_paid_at, status
`

func scanInstallment(row interface{ Scan(...any) error }) (*domain.LoanInstallment, error) {
	var i domain.LoanInstallment
	var currency, status string
	var paid *time.Time
	if err := row.Scan(
		&i.ID, &i.LoanID, &i.SequenceNumber, &i.Amount, &i.InterestRateAtDue, &currency,
		&i.ExpectedDueDate, &paid, &status,
	); err != nil {
		return nil, err
	}
	i.Currency = domain.Currency(currency)
	i.Status = domain.InstallmentStatus(status)
	i.ActualPaidAt = paid
	return &i, nil
}

func (s *Store) CreateInstallment(ctx context.Context, tx pgx.Tx, i *domain.LoanInstallment) (*domain.LoanInstallment, error) {
	const q = `
        insert into "bank".loan_installments
            (loan_id, sequence_number, amount, interest_rate_at_due, currency,
             expected_due_date, status)
        values ($1,$2,$3::numeric,$4::numeric,$5,$6,$7)
        returning ` + installmentColumns
	out, err := scanInstallment(tx.QueryRow(ctx, q,
		i.LoanID, i.SequenceNumber, i.Amount, i.InterestRateAtDue, string(i.Currency),
		i.ExpectedDueDate, string(i.Status),
	))
	if err != nil {
		return nil, apperr.Internal("create installment", err)
	}
	return out, nil
}

func (s *Store) MarkInstallmentPaid(ctx context.Context, tx pgx.Tx, id string) error {
	const q = `update "bank".loan_installments set status = 'paid', actual_paid_at = now(), updated_at = now() where id = $1`
	if _, err := tx.Exec(ctx, q, id); err != nil {
		return apperr.Internal("mark installment paid", err)
	}
	return nil
}

func (s *Store) MarkInstallmentOverdue(ctx context.Context, tx pgx.Tx, id string) error {
	const q = `update "bank".loan_installments set status = 'overdue', updated_at = now() where id = $1`
	if _, err := tx.Exec(ctx, q, id); err != nil {
		return apperr.Internal("mark installment overdue", err)
	}
	return nil
}

// ListInstallmentsDueOn returns unpaid installments whose expected
// due date is on or before `dueOn`. Used by the daily cron.
func (s *Store) ListInstallmentsDueOn(ctx context.Context, dueOn time.Time) ([]*domain.LoanInstallment, error) {
	const q = `select ` + installmentColumns + ` from "bank".loan_installments
              where status in ('unpaid','overdue') and expected_due_date <= $1
              order by expected_due_date, sequence_number`
	rows, err := s.Pool.Query(ctx, q, dueOn)
	if err != nil {
		return nil, apperr.Internal("list due installments", err)
	}
	defer rows.Close()
	var out []*domain.LoanInstallment
	for rows.Next() {
		i, err := scanInstallment(rows)
		if err != nil {
			return nil, apperr.Internal("scan installment", err)
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (s *Store) ListInstallmentsByLoan(ctx context.Context, loanID string) ([]*domain.LoanInstallment, error) {
	const q = `select ` + installmentColumns + ` from "bank".loan_installments
              where loan_id = $1 order by sequence_number`
	rows, err := s.Pool.Query(ctx, q, loanID)
	if err != nil {
		return nil, apperr.Internal("list installments", err)
	}
	defer rows.Close()
	var out []*domain.LoanInstallment
	for rows.Next() {
		i, err := scanInstallment(rows)
		if err != nil {
			return nil, apperr.Internal("scan installment", err)
		}
		out = append(out, i)
	}
	return out, rows.Err()
}
