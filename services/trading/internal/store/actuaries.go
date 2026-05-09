package store

import (
	"context"
	"strings"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/jackc/pgx/v5"
)

// UpsertActuaryInfo inserts or updates the actuary row.
func (s *Store) UpsertActuaryInfo(ctx context.Context, in *domain.ActuaryInfo) (*domain.ActuaryInfo, error) {
	const q = `
        insert into "trading".actuary_info (employee_id, type, daily_limit, used_limit, need_approval, created_at, updated_at)
        values ($1, $2, $3, 0, $4, now(), now())
        on conflict (employee_id) do update
          set type          = excluded.type,
              daily_limit   = excluded.daily_limit,
              need_approval = excluded.need_approval,
              updated_at    = now()
        returning employee_id, type, daily_limit::text, used_limit::text, need_approval, created_at, updated_at`

	row := s.Pool.QueryRow(ctx, q, in.EmployeeID, string(in.Type), in.DailyLimit, in.NeedApproval)
	out, err := scanActuary(row)
	if err != nil {
		return nil, apperr.Internal("upsert actuary", err)
	}
	return out, nil
}

// GetActuaryInfo returns one row or NotFound.
func (s *Store) GetActuaryInfo(ctx context.Context, employeeID string) (*domain.ActuaryInfo, error) {
	const q = `
        select employee_id, type, daily_limit::text, used_limit::text, need_approval, created_at, updated_at
        from "trading".actuary_info
        where employee_id = $1`
	out, err := scanActuary(s.Pool.QueryRow(ctx, q, employeeID))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("actuary not found")
		}
		return nil, apperr.Internal("get actuary", err)
	}
	return out, nil
}

// UpdateActuaryLimit sets daily_limit; bumps updated_at.
func (s *Store) UpdateActuaryLimit(ctx context.Context, employeeID, dailyLimit string) (*domain.ActuaryInfo, error) {
	const q = `
        update "trading".actuary_info
        set daily_limit = $2, updated_at = now()
        where employee_id = $1
        returning employee_id, type, daily_limit::text, used_limit::text, need_approval, created_at, updated_at`
	out, err := scanActuary(s.Pool.QueryRow(ctx, q, employeeID, dailyLimit))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("actuary not found")
		}
		return nil, apperr.Internal("update actuary limit", err)
	}
	return out, nil
}

// ResetActuaryUsedLimit zeroes used_limit for a single actuary.
func (s *Store) ResetActuaryUsedLimit(ctx context.Context, employeeID string) (*domain.ActuaryInfo, error) {
	const q = `
        update "trading".actuary_info
        set used_limit = 0, updated_at = now()
        where employee_id = $1
        returning employee_id, type, daily_limit::text, used_limit::text, need_approval, created_at, updated_at`
	out, err := scanActuary(s.Pool.QueryRow(ctx, q, employeeID))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("actuary not found")
		}
		return nil, apperr.Internal("reset used limit", err)
	}
	return out, nil
}

// ResetAllUsedLimits zeroes used_limit across every actuary; returns
// number affected. Used by the daily 23:59 cron.
func (s *Store) ResetAllUsedLimits(ctx context.Context) (int64, error) {
	const q = `update "trading".actuary_info set used_limit = 0, updated_at = now() where used_limit <> 0`
	tag, err := s.Pool.Exec(ctx, q)
	if err != nil {
		return 0, apperr.Internal("reset all used limits", err)
	}
	return tag.RowsAffected(), nil
}

// SetActuaryNeedApproval flips the per-actuary approval requirement.
func (s *Store) SetActuaryNeedApproval(ctx context.Context, employeeID string, need bool) (*domain.ActuaryInfo, error) {
	const q = `
        update "trading".actuary_info
        set need_approval = $2, updated_at = now()
        where employee_id = $1
        returning employee_id, type, daily_limit::text, used_limit::text, need_approval, created_at, updated_at`
	out, err := scanActuary(s.Pool.QueryRow(ctx, q, employeeID, need))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("actuary not found")
		}
		return nil, apperr.Internal("set need approval", err)
	}
	return out, nil
}

// AddUsedLimit increments used_limit by a positive RSD amount inside
// the caller's transaction. Used when an order is approved/executed.
func (s *Store) AddUsedLimit(ctx context.Context, tx pgx.Tx, employeeID, deltaRSD string) error {
	const q = `update "trading".actuary_info set used_limit = used_limit + $2, updated_at = now() where employee_id = $1`
	_, err := tx.Exec(ctx, q, employeeID, deltaRSD)
	if err != nil {
		return apperr.Internal("add used limit", err)
	}
	return nil
}

// ActuaryFilter narrows ListActuaries.
type ActuaryFilter struct {
	Type        domain.ActuaryType
	EmailQuery  string
	NameQuery   string
}

// ListActuaries returns matching rows. Email/name filtering happens in
// the user service (this store has no access to user.users) — the
// filter struct here narrows on type only; the service layer fans out
// to the user service for name/email.
func (s *Store) ListActuaries(ctx context.Context, t domain.ActuaryType, page, pageSize int) ([]*domain.ActuaryInfo, int64, error) {
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 200 {
		pageSize = 200
	}
	if page <= 0 {
		page = 1
	}

	var (
		args      []any
		whereParts []string
	)
	if t != "" {
		args = append(args, string(t))
		whereParts = append(whereParts, "type = $1")
	}
	where := ""
	if len(whereParts) > 0 {
		where = " where " + strings.Join(whereParts, " and ")
	}

	countQ := "select count(*) from \"trading\".actuary_info" + where
	var total int64
	if err := s.Pool.QueryRow(ctx, countQ, args...).Scan(&total); err != nil {
		return nil, 0, apperr.Internal("count actuaries", err)
	}

	q := `select employee_id, type, daily_limit::text, used_limit::text, need_approval, created_at, updated_at
          from "trading".actuary_info` + where + `
          order by updated_at desc
          limit ` + intArg(len(args)+1) + ` offset ` + intArg(len(args)+2)
	args = append(args, pageSize, (page-1)*pageSize)

	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, apperr.Internal("list actuaries", err)
	}
	defer rows.Close()

	var out []*domain.ActuaryInfo
	for rows.Next() {
		a, err := scanActuary(rows)
		if err != nil {
			return nil, 0, apperr.Internal("scan actuary", err)
		}
		out = append(out, a)
	}
	return out, total, rows.Err()
}

// scanActuary reads one ActuaryInfo row.
func scanActuary(row pgx.Row) (*domain.ActuaryInfo, error) {
	var a domain.ActuaryInfo
	var t string
	if err := row.Scan(&a.EmployeeID, &t, &a.DailyLimit, &a.UsedLimit, &a.NeedApproval, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, err
	}
	a.Type = domain.ActuaryType(t)
	return &a, nil
}

