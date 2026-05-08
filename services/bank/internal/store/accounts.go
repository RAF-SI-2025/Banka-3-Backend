package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
)

// accountColumns casts the numeric columns to text so pgx scans into
// strings, preserving precision across the API boundary.
const accountColumns = `
    id, number, name, owner_client_id, company_id, created_by_employee_id,
    kind, subtype, currency, status,
    balance::text, available_balance::text, maintenance_fee::text,
    daily_limit::text, monthly_limit::text,
    daily_spent::text, monthly_spent::text,
    created_at, expires_at, updated_at
`

func scanAccount(row interface{ Scan(...any) error }) (*domain.Account, error) {
	var a domain.Account
	var kind, subtype, currency, status string
	var companyID *string
	if err := row.Scan(
		&a.ID, &a.Number, &a.Name, &a.OwnerClientID, &companyID, &a.CreatedByEmployeeID,
		&kind, &subtype, &currency, &status,
		&a.Balance, &a.AvailableBalance, &a.MaintenanceFee,
		&a.DailyLimit, &a.MonthlyLimit,
		&a.DailySpent, &a.MonthlySpent,
		&a.CreatedAt, &a.ExpiresAt, &a.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if companyID != nil {
		a.CompanyID = *companyID
	}
	a.Kind = domain.AccountKind(kind)
	a.Subtype = domain.AccountSubtype(subtype)
	a.Currency = domain.Currency(currency)
	a.Status = domain.AccountStatus(status)
	return &a, nil
}

func (s *Store) CreateAccount(ctx context.Context, a *domain.Account) (*domain.Account, error) {
	const q = `
        insert into "bank".accounts (
            number, name, owner_client_id, company_id, created_by_employee_id,
            kind, subtype, currency, status,
            balance, available_balance, maintenance_fee,
            daily_limit, monthly_limit
        ) values (
            $1, $2, $3, nullif($4, '')::uuid, $5,
            $6, $7, $8, $9,
            $10::numeric, $10::numeric, $11::numeric,
            $12::numeric, $13::numeric
        )
        returning ` + accountColumns
	out, err := scanAccount(s.Pool.QueryRow(ctx, q,
		a.Number, a.Name, a.OwnerClientID, a.CompanyID, a.CreatedByEmployeeID,
		string(a.Kind), string(a.Subtype), string(a.Currency), string(a.Status),
		a.Balance, a.MaintenanceFee, a.DailyLimit, a.MonthlyLimit,
	))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, apperr.Conflict("account number collision")
		}
		if isCheckViolation(err) {
			return nil, apperr.Validation("account constraints violated (kind/company/subtype mismatch)")
		}
		return nil, apperr.Internal("create account", err)
	}
	return out, nil
}

func (s *Store) GetAccountByID(ctx context.Context, id string) (*domain.Account, error) {
	const q = `select ` + accountColumns + ` from "bank".accounts where id = $1`
	out, err := scanAccount(s.Pool.QueryRow(ctx, q, id))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("account not found")
		}
		return nil, apperr.Internal("get account", err)
	}
	return out, nil
}

func (s *Store) GetSystemAccount(ctx context.Context, currency domain.Currency) (*domain.Account, error) {
	const q = `select ` + accountColumns + ` from "bank".accounts
              where kind = 'system' and currency = $1`
	out, err := scanAccount(s.Pool.QueryRow(ctx, q, string(currency)))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("system account not found for currency " + string(currency))
		}
		return nil, apperr.Internal("get system account", err)
	}
	return out, nil
}

func (s *Store) UpdateAccountLimits(ctx context.Context, id, daily, monthly string) (*domain.Account, error) {
	// Only update fields that were provided; an empty string means "no change".
	const q = `
        update "bank".accounts set
            daily_limit   = case when $2 = '' then daily_limit   else $2::numeric end,
            monthly_limit = case when $3 = '' then monthly_limit else $3::numeric end,
            updated_at = now()
        where id = $1
        returning ` + accountColumns
	out, err := scanAccount(s.Pool.QueryRow(ctx, q, id, daily, monthly))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("account not found")
		}
		return nil, apperr.Internal("update limits", err)
	}
	return out, nil
}

func (s *Store) SetAccountStatus(ctx context.Context, id string, status domain.AccountStatus) (*domain.Account, error) {
	const q = `
        update "bank".accounts set status = $2, updated_at = now()
        where id = $1
        returning ` + accountColumns
	out, err := scanAccount(s.Pool.QueryRow(ctx, q, id, string(status)))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("account not found")
		}
		return nil, apperr.Internal("set status", err)
	}
	return out, nil
}

func (s *Store) ListAccounts(ctx context.Context, f domain.AccountFilter, page, pageSize int) ([]*domain.Account, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 50
	}

	var conds []string
	var args []any
	if f.OwnerClientID != "" {
		args = append(args, f.OwnerClientID)
		conds = append(conds, fmt.Sprintf("owner_client_id = $%d", len(args)))
	}
	if f.Kind != "" {
		args = append(args, string(f.Kind))
		conds = append(conds, fmt.Sprintf("kind = $%d", len(args)))
	}
	if f.Currency != "" {
		args = append(args, string(f.Currency))
		conds = append(conds, fmt.Sprintf("currency = $%d", len(args)))
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
	if err := s.Pool.QueryRow(ctx, `select count(*) from "bank".accounts`+where, args...).Scan(&total); err != nil {
		return nil, 0, apperr.Internal("count accounts", err)
	}

	listArgs := append([]any{}, args...)
	listArgs = append(listArgs, pageSize, (page-1)*pageSize)
	listQ := `select ` + accountColumns + ` from "bank".accounts` + where +
		fmt.Sprintf(" order by created_at desc limit $%d offset $%d", len(args)+1, len(args)+2)

	rows, err := s.Pool.Query(ctx, listQ, listArgs...)
	if err != nil {
		return nil, 0, apperr.Internal("list accounts", err)
	}
	defer rows.Close()
	var out []*domain.Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, 0, apperr.Internal("scan account", err)
		}
		out = append(out, a)
	}
	return out, total, rows.Err()
}
