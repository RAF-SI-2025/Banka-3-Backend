// Package store is the exchange service's persistence layer.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/postgres"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/exchange/internal/domain"
	"github.com/jackc/pgx/v5"
)

type Store struct {
	// DB routes writes/transactions to the primary and reads marked
	// postgres.WithRead(ctx) to the read replica. See pkg/postgres.
	DB *postgres.DB
}

func New(db *postgres.DB) *Store { return &Store{DB: db} }

func noRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// UpsertRate inserts or updates a single (from, to) row. Stamping
// updated_at via the SET clause keeps the column actively meaningful
// even on no-change updates.
func (s *Store) UpsertRate(ctx context.Context, r *domain.Rate) (*domain.Rate, error) {
	const q = `
        insert into "exchange".fx_rates ("from", "to", bid, ask, updated_at)
        values ($1,$2,$3,$4, now())
        on conflict ("from","to") do update
          set bid = excluded.bid, ask = excluded.ask, updated_at = now()
        returning "from", "to", bid::text, ask::text, updated_at`

	out, err := scanRate(s.DB.QueryRow(ctx, q, string(r.From), string(r.To), r.Bid, r.Ask))
	if err != nil {
		// numeric check-violation is the most likely failure here
		// (negative bid, ask < bid). Surface as Validation so the gateway
		// returns 400.
		var pe interface{ SQLState() string }
		if errors.As(err, &pe) && pe.SQLState() == "23514" {
			return nil, apperr.Validation("fx rate violates a check constraint (positive amounts and ask ≥ bid)")
		}
		return nil, apperr.Internal("upsert fx rate", err)
	}
	return out, nil
}

// GetRate returns one row or NotFound.
func (s *Store) GetRate(ctx context.Context, from, to domain.Currency) (*domain.Rate, error) {
	const q = `
        select "from", "to", bid::text, ask::text, updated_at
        from "exchange".fx_rates
        where "from" = $1 and "to" = $2`
	out, err := scanRate(s.DB.QueryRow(postgres.WithRead(ctx), q, string(from), string(to)))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("fx rate not found")
		}
		return nil, apperr.Internal("get fx rate", err)
	}
	return out, nil
}

// ListRates returns all rows, optionally filtered to a single base
// currency.
func (s *Store) ListRates(ctx context.Context, from domain.Currency) ([]*domain.Rate, error) {
	args := []any{}
	q := `select "from", "to", bid::text, ask::text, updated_at from "exchange".fx_rates`
	if from != "" {
		q += ` where "from" = $1`
		args = append(args, string(from))
	}
	q += ` order by "from", "to"`

	rows, err := s.DB.Query(postgres.WithRead(ctx), q, args...)
	if err != nil {
		return nil, apperr.Internal("list fx rates", err)
	}
	defer rows.Close()

	var out []*domain.Rate
	for rows.Next() {
		r, err := scanRate(rows)
		if err != nil {
			return nil, apperr.Internal("scan fx rate", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// InsertRateHistory appends one append-only observation of a pair. Called
// from the feed alongside the latest-only UpsertRate so history accrues
// going forward without disturbing the live rate.
func (s *Store) InsertRateHistory(ctx context.Context, r *domain.Rate) error {
	const q = `
        insert into "exchange".fx_rate_history ("from", "to", bid, ask)
        values ($1,$2,$3,$4)`
	if _, err := s.DB.Exec(ctx, q, string(r.From), string(r.To), r.Bid, r.Ask); err != nil {
		var pe interface{ SQLState() string }
		if errors.As(err, &pe) && pe.SQLState() == "23514" {
			return apperr.Validation("fx rate history violates a check constraint (positive amounts and ask ≥ bid)")
		}
		return apperr.Internal("insert fx rate history", err)
	}
	return nil
}

// ListRateHistory returns the recorded points for a pair on or after
// since, newest first.
func (s *Store) ListRateHistory(ctx context.Context, from, to domain.Currency, since time.Time) ([]*domain.RateHistoryPoint, error) {
	const q = `
        select bid::text, ask::text, recorded_at
        from "exchange".fx_rate_history
        where "from" = $1 and "to" = $2 and recorded_at >= $3
        order by recorded_at desc`
	rows, err := s.DB.Query(postgres.WithRead(ctx), q, string(from), string(to), since)
	if err != nil {
		return nil, apperr.Internal("list fx rate history", err)
	}
	defer rows.Close()

	var out []*domain.RateHistoryPoint
	for rows.Next() {
		var p domain.RateHistoryPoint
		if err := rows.Scan(&p.Bid, &p.Ask, &p.RecordedAt); err != nil {
			return nil, apperr.Internal("scan fx rate history", err)
		}
		out = append(out, &p)
	}
	return out, rows.Err()
}

func scanRate(row interface{ Scan(...any) error }) (*domain.Rate, error) {
	var r domain.Rate
	var from, to string
	if err := row.Scan(&from, &to, &r.Bid, &r.Ask, &r.UpdatedAt); err != nil {
		return nil, err
	}
	r.From = domain.Currency(from)
	r.To = domain.Currency(to)
	return &r, nil
}
