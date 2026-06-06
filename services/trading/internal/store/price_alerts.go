package store

import (
	"context"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/postgres"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/jackc/pgx/v5"
)

const priceAlertCols = `id, user_id, user_kind, security_id,
    threshold::text, condition, is_active, created_at, triggered_at`

// InsertPriceAlert creates one alert row and returns it.
func (s *Store) InsertPriceAlert(ctx context.Context, a *domain.PriceAlert) (*domain.PriceAlert, error) {
	const q = `
        insert into "trading".price_alerts
            (user_id, user_kind, security_id, threshold, condition)
        values ($1, $2, $3, $4::numeric, $5)
        returning ` + priceAlertCols
	row := s.DB.QueryRow(ctx, q,
		a.UserID, string(a.UserKind), a.SecurityID, a.Threshold, string(a.Condition))
	out, err := scanPriceAlert(row)
	if err != nil {
		return nil, apperr.Internal("insert price alert", err)
	}
	return out, nil
}

// ListPriceAlertsByUser returns every alert (active + past) for one user,
// newest first.
func (s *Store) ListPriceAlertsByUser(ctx context.Context, userID string) ([]*domain.PriceAlert, error) {
	q := `select ` + priceAlertCols + ` from "trading".price_alerts
	      where user_id = $1 order by created_at desc`
	rows, err := s.DB.Query(postgres.WithRead(ctx), q, userID)
	if err != nil {
		return nil, apperr.Internal("list price alerts", err)
	}
	defer rows.Close()
	return scanPriceAlerts(rows)
}

// ListActivePriceAlerts returns every active alert across all users —
// the sweep's working set. Stays on the primary (worker read).
func (s *Store) ListActivePriceAlerts(ctx context.Context) ([]*domain.PriceAlert, error) {
	q := `select ` + priceAlertCols + ` from "trading".price_alerts
	      where is_active = true order by created_at asc`
	rows, err := s.DB.Query(ctx, q)
	if err != nil {
		return nil, apperr.Internal("list active price alerts", err)
	}
	defer rows.Close()
	return scanPriceAlerts(rows)
}

// GetPriceAlert returns one alert by id. NotFound on miss.
func (s *Store) GetPriceAlert(ctx context.Context, id string) (*domain.PriceAlert, error) {
	q := `select ` + priceAlertCols + ` from "trading".price_alerts where id = $1`
	out, err := scanPriceAlert(s.DB.QueryRow(postgres.WithRead(ctx), q, id))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("price alert not found")
		}
		return nil, apperr.Internal("get price alert", err)
	}
	return out, nil
}

// DeactivatePriceAlert flips is_active to false and stamps triggered_at.
// Idempotent: a no-op on an already-inactive row. Used both by the sweep
// (on a crossing) and by the owner-scoped delete.
func (s *Store) DeactivatePriceAlert(ctx context.Context, id string, triggeredAt time.Time) error {
	const q = `update "trading".price_alerts
	           set is_active = false, triggered_at = $2
	           where id = $1`
	if _, err := s.DB.Exec(ctx, q, id, triggeredAt); err != nil {
		return apperr.Internal("deactivate price alert", err)
	}
	return nil
}

func scanPriceAlerts(rows pgx.Rows) ([]*domain.PriceAlert, error) {
	var out []*domain.PriceAlert
	for rows.Next() {
		a, err := scanPriceAlert(rows)
		if err != nil {
			return nil, apperr.Internal("scan price alert", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func scanPriceAlert(row pgx.Row) (*domain.PriceAlert, error) {
	var (
		a         domain.PriceAlert
		kind      string
		condition string
		triggered *time.Time
	)
	if err := row.Scan(
		&a.ID, &a.UserID, &kind, &a.SecurityID,
		&a.Threshold, &condition, &a.IsActive, &a.CreatedAt, &triggered,
	); err != nil {
		return nil, err
	}
	a.UserKind = domain.UserKind(kind)
	a.Condition = domain.PriceAlertCondition(condition)
	a.TriggeredAt = triggered
	return &a, nil
}
