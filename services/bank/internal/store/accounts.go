package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/postgres"
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
    created_at, expires_at, updated_at, last_maintenance_debit
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
		&a.CreatedAt, &a.ExpiresAt, &a.UpdatedAt, &a.LastMaintenanceDebit,
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
	out, err := scanAccount(s.DB.QueryRow(
		ctx, q,
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
		logger.From(ctx).ErrorContext(ctx, "create account failed", "err", err, "number", a.Number, "owner_client_id", a.OwnerClientID)
		return nil, apperr.Internal("create account", err)
	}
	return out, nil
}

func (s *Store) GetAccountByID(ctx context.Context, id string) (*domain.Account, error) {
	const q = `select ` + accountColumns + ` from "bank".accounts where id = $1`
	out, err := scanAccount(s.DB.QueryRow(postgres.WithRead(ctx), q, id))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("account not found")
		}
		logger.From(ctx).ErrorContext(ctx, "get account failed", "err", err, "account_id", id)
		return nil, apperr.Internal("get account", err)
	}
	return out, nil
}

func (s *Store) GetSystemAccount(ctx context.Context, currency domain.Currency) (*domain.Account, error) {
	const q = `select ` + accountColumns + ` from "bank".accounts
              where kind = 'system' and currency = $1`
	out, err := scanAccount(s.DB.QueryRow(ctx, q, string(currency)))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("system account not found for currency " + string(currency))
		}
		logger.From(ctx).ErrorContext(ctx, "get system account failed", "err", err, "currency", string(currency))
		return nil, apperr.Internal("get system account", err)
	}
	return out, nil
}

// GetForexBookAccount returns the bank's per-currency forex-book
// account used as the "market" counterparty in spec p.42 forex
// settlement. Seeded by EnsureSystemAccounts.
func (s *Store) GetForexBookAccount(ctx context.Context, currency domain.Currency) (*domain.Account, error) {
	const q = `select ` + accountColumns + ` from "bank".accounts
              where kind = 'forex_book' and currency = $1`
	out, err := scanAccount(s.DB.QueryRow(ctx, q, string(currency)))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("forex book account not found for currency " + string(currency))
		}
		logger.From(ctx).ErrorContext(ctx, "get forex book account failed", "err", err, "currency", string(currency))
		return nil, apperr.Internal("get forex book account", err)
	}
	return out, nil
}

// GetStateTaxAccount returns the state's RSD capital-gains tax account
// (spec p.62). Seeded by EnsureSystemAccounts at boot.
func (s *Store) GetStateTaxAccount(ctx context.Context) (*domain.Account, error) {
	const q = `select ` + accountColumns + ` from "bank".accounts
              where kind = 'state_tax' and currency = 'RSD'`
	out, err := scanAccount(s.DB.QueryRow(ctx, q))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("state tax account not found")
		}
		logger.From(ctx).ErrorContext(ctx, "get state tax account failed", "err", err)
		return nil, apperr.Internal("get state tax account", err)
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
	out, err := scanAccount(s.DB.QueryRow(ctx, q, id, daily, monthly))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("account not found")
		}
		logger.From(ctx).ErrorContext(ctx, "update limits failed", "err", err, "account_id", id)
		return nil, apperr.Internal("update limits", err)
	}
	return out, nil
}

// UpdateAccountName overwrites the display name. Caller is responsible
// for the spec p.20 invariants (owner check, distinct from current,
// distinct from sibling accounts).
func (s *Store) UpdateAccountName(ctx context.Context, id, name string) (*domain.Account, error) {
	const q = `
        update "bank".accounts set name = $2, updated_at = now()
        where id = $1
        returning ` + accountColumns
	out, err := scanAccount(s.DB.QueryRow(ctx, q, id, name))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("account not found")
		}
		logger.From(ctx).ErrorContext(ctx, "update name failed", "err", err, "account_id", id)
		return nil, apperr.Internal("update name", err)
	}
	return out, nil
}

// AccountNameTakenByOwner checks whether ownerClientID already has an
// active account with the given name (case-insensitive), excluding
// excludeID so a no-op rename doesn't trip the constraint. Spec p.20
// validation: "novo ime se ne poklapa s imenom nekog drugog računa
// iste mušterije".
func (s *Store) AccountNameTakenByOwner(ctx context.Context, ownerClientID, name, excludeID string) (bool, error) {
	const q = `
        select exists(
            select 1 from "bank".accounts
            where owner_client_id = $1
              and lower(name) = lower($2)
              and id <> $3
              and status <> 'closed'
        )`
	var taken bool
	if err := s.DB.QueryRow(ctx, q, ownerClientID, name, excludeID).Scan(&taken); err != nil {
		logger.From(ctx).ErrorContext(ctx, "check name uniqueness failed", "err", err, "owner_client_id", ownerClientID)
		return false, apperr.Internal("check name uniqueness", err)
	}
	return taken, nil
}

func (s *Store) SetAccountStatus(ctx context.Context, id string, status domain.AccountStatus) (*domain.Account, error) {
	const q = `
        update "bank".accounts set status = $2, updated_at = now()
        where id = $1
        returning ` + accountColumns
	out, err := scanAccount(s.DB.QueryRow(ctx, q, id, string(status)))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("account not found")
		}
		logger.From(ctx).ErrorContext(ctx, "set status failed", "err", err, "account_id", id, "status", string(status))
		return nil, apperr.Internal("set status", err)
	}
	return out, nil
}

// ListAccountsDueForMaintenance returns active accounts whose monthly
// maintenance fee should be debited today: fee > 0 and either never
// debited or last_maintenance_debit is older than the cutoff.
//
// Excluded: system accounts (no fee), inactive accounts.
func (s *Store) ListAccountsDueForMaintenance(ctx context.Context, cutoff time.Time) ([]*domain.Account, error) {
	const q = `select ` + accountColumns + ` from "bank".accounts
              where status = 'active' and kind <> 'system'
                and maintenance_fee > 0
                and (last_maintenance_debit is null or last_maintenance_debit <= $1)
              order by created_at`
	rows, err := s.DB.Query(ctx, q, cutoff)
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "list maintenance-due failed", "err", err)
		return nil, apperr.Internal("list maintenance-due", err)
	}
	defer rows.Close()
	var out []*domain.Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			logger.From(ctx).ErrorContext(ctx, "scan maintenance-due account failed", "err", err)
			return nil, apperr.Internal("scan", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		logger.From(ctx).ErrorContext(ctx, "iterate maintenance-due accounts failed", "err", err)
		return out, err
	}
	return out, nil
}

// MarkMaintenanceDebited stamps last_maintenance_debit = now() on the
// account inside the supplied tx. Caller is responsible for the
// AdjustBalance + InsertTransaction legs.
func (s *Store) MarkMaintenanceDebited(ctx context.Context, tx pgx.Tx, accountID string) error {
	_, err := tx.Exec(ctx, `update "bank".accounts set last_maintenance_debit = now() where id = $1`, accountID)
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "mark maintenance debited failed", "err", err, "account_id", accountID)
		return apperr.Internal("mark maintenance debited", err)
	}
	return nil
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
	if len(f.ExcludeKinds) > 0 {
		placeholders := make([]string, 0, len(f.ExcludeKinds))
		for _, k := range f.ExcludeKinds {
			args = append(args, string(k))
			placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
		}
		conds = append(conds, "kind not in ("+strings.Join(placeholders, ", ")+")")
	}
	where := ""
	if len(conds) > 0 {
		where = " where " + strings.Join(conds, " and ")
	}

	var total int64
	if err := s.DB.QueryRow(postgres.WithRead(ctx), `select count(*) from "bank".accounts`+where, args...).Scan(&total); err != nil {
		logger.From(ctx).ErrorContext(ctx, "count accounts failed", "err", err)
		return nil, 0, apperr.Internal("count accounts", err)
	}

	listArgs := append([]any{}, args...)
	listArgs = append(listArgs, pageSize, (page-1)*pageSize)
	listQ := `select ` + accountColumns + ` from "bank".accounts` + where +
		fmt.Sprintf(" order by created_at desc limit $%d offset $%d", len(args)+1, len(args)+2)

	rows, err := s.DB.Query(postgres.WithRead(ctx), listQ, listArgs...)
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "list accounts failed", "err", err)
		return nil, 0, apperr.Internal("list accounts", err)
	}
	defer rows.Close()
	var out []*domain.Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			logger.From(ctx).ErrorContext(ctx, "scan account failed", "err", err)
			return nil, 0, apperr.Internal("scan account", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		logger.From(ctx).ErrorContext(ctx, "iterate accounts failed", "err", err)
		return out, total, err
	}
	return out, total, nil
}

// ResetSpentCounters zeroes daily_spent for every account whose recorded
// daily-reset date is older than today, and monthly_spent for every
// account whose recorded monthly-reset date is in a prior calendar
// month. Returns the row counts touched in each pass.
//
// Idempotent: running the cron twice on the same day does no work the
// second time (the WHERE clauses match nothing once the reset columns
// have been stamped).
//
// Both updates run inside a single transaction so a payment landing
// between the two passes can't have its daily_spent increment overwritten
// by the daily reset and then have the monthly reset fire against a row
// that's already moved on. CURRENT_DATE is evaluated server-side, so the
// result depends on the Postgres clock — tests that need to fake the
// rollover backdate the reset columns directly rather than inject a
// clock here.
func (s *Store) ResetSpentCounters(ctx context.Context) (daily, monthly int64, err error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "begin spent-reset tx failed", "err", err)
		return 0, 0, apperr.Internal("begin spent-reset tx", err)
	}
	defer func() {
		if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			logger.From(ctx).WarnContext(ctx, "rollback spent-reset tx failed", "err", rbErr)
		}
	}()

	const dailyQ = `
        update "bank".accounts
           set daily_spent          = 0,
               daily_spent_reset_on = current_date,
               updated_at           = now()
         where daily_spent_reset_on < current_date`
	tag, err := tx.Exec(ctx, dailyQ)
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "reset daily spent failed", "err", err)
		return 0, 0, apperr.Internal("reset daily spent", err)
	}
	daily = tag.RowsAffected()

	const monthlyQ = `
        update "bank".accounts
           set monthly_spent          = 0,
               monthly_spent_reset_on = current_date,
               updated_at             = now()
         where date_trunc('month', monthly_spent_reset_on) < date_trunc('month', current_date)`
	tag, err = tx.Exec(ctx, monthlyQ)
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "reset monthly spent failed", "err", err)
		return daily, 0, apperr.Internal("reset monthly spent", err)
	}
	monthly = tag.RowsAffected()

	if err := tx.Commit(ctx); err != nil {
		logger.From(ctx).ErrorContext(ctx, "commit spent-reset tx failed", "err", err)
		return 0, 0, apperr.Internal("commit spent-reset tx", err)
	}
	return daily, monthly, nil
}
