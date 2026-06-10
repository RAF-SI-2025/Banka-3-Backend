package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/postgres"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
)

// =====================================================================
// Forex forward spreads (per-pair SpreadFactor, supervisor-set)
// =====================================================================

const forexSpreadCols = `base_currency, quote_currency, spread_factor::text,
    coalesce(updated_by::text, ''), updated_at`

func scanForexSpread(row interface{ Scan(...any) error }) (*domain.ForexForwardSpread, error) {
	var sp domain.ForexForwardSpread
	var base, quote string
	if err := row.Scan(&base, &quote, &sp.SpreadFactor, &sp.UpdatedBy, &sp.UpdatedAt); err != nil {
		return nil, err
	}
	sp.BaseCurrency = domain.Currency(base)
	sp.QuoteCurrency = domain.Currency(quote)
	return &sp, nil
}

// GetForexForwardSpread returns the SpreadFactor row for a pair, or
// NotFound when the supervisor hasn't configured the pair yet.
func (s *Store) GetForexForwardSpread(ctx context.Context, base, quote domain.Currency) (*domain.ForexForwardSpread, error) {
	const q = `select ` + forexSpreadCols + `
        from "bank".forex_forward_spreads where base_currency = $1 and quote_currency = $2`
	sp, err := scanForexSpread(s.DB.QueryRow(postgres.WithRead(ctx), q, string(base), string(quote)))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("spread za valutni par nije podešen")
		}
		logger.From(ctx).ErrorContext(ctx, "get forex spread failed", "err", err, "base", string(base), "quote", string(quote))
		return nil, apperr.Internal("get forex spread", err)
	}
	return sp, nil
}

// ListForexForwardSpreads returns every configured pair, ordered by pair.
func (s *Store) ListForexForwardSpreads(ctx context.Context) ([]*domain.ForexForwardSpread, error) {
	const q = `select ` + forexSpreadCols + `
        from "bank".forex_forward_spreads order by base_currency, quote_currency`
	rows, err := s.DB.Query(postgres.WithRead(ctx), q)
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "list forex spreads failed", "err", err)
		return nil, apperr.Internal("list forex spreads", err)
	}
	defer rows.Close()
	var out []*domain.ForexForwardSpread
	for rows.Next() {
		sp, err := scanForexSpread(rows)
		if err != nil {
			logger.From(ctx).ErrorContext(ctx, "scan forex spread failed", "err", err)
			return nil, apperr.Internal("scan forex spread", err)
		}
		out = append(out, sp)
	}
	if err := rows.Err(); err != nil {
		logger.From(ctx).ErrorContext(ctx, "iterate forex spreads failed", "err", err)
		return out, err
	}
	return out, nil
}

// UpsertForexForwardSpread inserts or updates a pair's SpreadFactor,
// stamping the supervisor who set it.
func (s *Store) UpsertForexForwardSpread(ctx context.Context, sp *domain.ForexForwardSpread) (*domain.ForexForwardSpread, error) {
	const q = `
        insert into "bank".forex_forward_spreads (base_currency, quote_currency, spread_factor, updated_by, updated_at)
        values ($1, $2, $3::numeric, nullif($4, '')::uuid, now())
        on conflict (base_currency, quote_currency) do update
            set spread_factor = excluded.spread_factor,
                updated_by    = excluded.updated_by,
                updated_at    = now()
        returning ` + forexSpreadCols
	out, err := scanForexSpread(s.DB.QueryRow(ctx, q,
		string(sp.BaseCurrency), string(sp.QuoteCurrency), sp.SpreadFactor, sp.UpdatedBy))
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "upsert forex spread failed", "err", err, "base", string(sp.BaseCurrency), "quote", string(sp.QuoteCurrency))
		return nil, apperr.Internal("upsert forex spread", err)
	}
	return out, nil
}

// =====================================================================
// Forex forwards (concluded contracts)
// =====================================================================

const forexForwardCols = `id, client_id, base_currency, quote_currency,
    notional::text, forward_rate::text, spot_ask_rate::text, spread_factor::text,
    days_to_settlement, commission::text, reservation_id, from_account_id, to_account_id,
    settlement_date, status, coalesce(failure_reason, ''), created_at, settled_at`

func scanForexForward(row interface{ Scan(...any) error }) (*domain.ForexForward, error) {
	var f domain.ForexForward
	var base, quote, status string
	var settledAt *time.Time
	if err := row.Scan(
		&f.ID, &f.ClientID, &base, &quote,
		&f.Notional, &f.ForwardRate, &f.SpotAskRate, &f.SpreadFactor,
		&f.DaysToSettlement, &f.Commission, &f.ReservationID, &f.FromAccountID, &f.ToAccountID,
		&f.SettlementDate, &status, &f.FailureReason, &f.CreatedAt, &settledAt,
	); err != nil {
		return nil, err
	}
	f.BaseCurrency = domain.Currency(base)
	f.QuoteCurrency = domain.Currency(quote)
	f.Status = domain.ForexForwardStatus(status)
	f.SettledAt = settledAt
	return &f, nil
}

// InsertForexForward persists a new 'active' contract inside the caller's
// tx (the reservation + commission charge + insert run atomically).
func (s *Store) InsertForexForward(ctx context.Context, tx pgx.Tx, f *domain.ForexForward) (*domain.ForexForward, error) {
	const q = `
        insert into "bank".forex_forwards
            (client_id, base_currency, quote_currency, notional, forward_rate, spot_ask_rate,
             spread_factor, days_to_settlement, commission, reservation_id,
             from_account_id, to_account_id, settlement_date)
        values ($1, $2, $3, $4::numeric, $5::numeric, $6::numeric, $7::numeric, $8, $9::numeric,
                $10, $11, $12, $13)
        returning ` + forexForwardCols
	out, err := scanForexForward(tx.QueryRow(ctx, q,
		f.ClientID, string(f.BaseCurrency), string(f.QuoteCurrency), f.Notional, f.ForwardRate,
		f.SpotAskRate, f.SpreadFactor, f.DaysToSettlement, f.Commission, f.ReservationID,
		f.FromAccountID, f.ToAccountID, f.SettlementDate,
	))
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "insert forex forward failed", "err", err, "client_id", f.ClientID, "from_account_id", f.FromAccountID)
		return nil, apperr.Internal("insert forex forward", err)
	}
	return out, nil
}

// GetForexForward loads a single contract by id.
func (s *Store) GetForexForward(ctx context.Context, id string) (*domain.ForexForward, error) {
	const q = `select ` + forexForwardCols + ` from "bank".forex_forwards where id = $1`
	f, err := scanForexForward(s.DB.QueryRow(ctx, q, id))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("terminski ugovor ne postoji")
		}
		logger.From(ctx).ErrorContext(ctx, "get forex forward failed", "err", err, "forward_id", id)
		return nil, apperr.Internal("get forex forward", err)
	}
	return f, nil
}

// ListForexForwardsByClient returns the client's contracts, newest first.
func (s *Store) ListForexForwardsByClient(ctx context.Context, clientID string) ([]*domain.ForexForward, error) {
	const q = `select ` + forexForwardCols + `
        from "bank".forex_forwards where client_id = $1 order by created_at desc`
	return s.queryForexForwards(ctx, q, clientID)
}

// ListDueForexForwards returns every 'active' contract whose settlement
// date is at or before now, oldest first. Read against the primary so a
// just-concluded contract isn't missed by replica lag.
func (s *Store) ListDueForexForwards(ctx context.Context, now time.Time) ([]*domain.ForexForward, error) {
	const q = `select ` + forexForwardCols + `
        from "bank".forex_forwards
       where status = 'active' and settlement_date <= $1
       order by settlement_date asc`
	return s.queryForexForwards(ctx, q, now)
}

func (s *Store) queryForexForwards(ctx context.Context, q string, args ...any) ([]*domain.ForexForward, error) {
	rows, err := s.DB.Query(ctx, q, args...)
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "list forex forwards failed", "err", err)
		return nil, apperr.Internal("list forex forwards", err)
	}
	defer rows.Close()
	var out []*domain.ForexForward
	for rows.Next() {
		f, err := scanForexForward(rows)
		if err != nil {
			logger.From(ctx).ErrorContext(ctx, "scan forex forward failed", "err", err)
			return nil, apperr.Internal("scan forex forward", err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		logger.From(ctx).ErrorContext(ctx, "iterate forex forwards failed", "err", err)
		return out, err
	}
	return out, nil
}

// MarkForexForwardSettled flips an 'active' row to 'settled' and stamps
// settled_at. Status-guarded so a double-sweep is a no-op.
func (s *Store) MarkForexForwardSettled(ctx context.Context, id string, at time.Time) error {
	const q = `
        update "bank".forex_forwards
           set status = 'settled', settled_at = $2, failure_reason = null
         where id = $1 and status = 'active'`
	if _, err := s.DB.Exec(ctx, q, id, at); err != nil {
		logger.From(ctx).ErrorContext(ctx, "mark forex forward settled failed", "err", err, "forward_id", id)
		return apperr.Internal("mark forex forward settled", err)
	}
	return nil
}

// MarkForexForwardFailed flips an 'active' row to 'failed' with a reason
// and stamps settled_at. Status-guarded so a double-sweep is a no-op.
func (s *Store) MarkForexForwardFailed(ctx context.Context, id, reason string, at time.Time) error {
	const q = `
        update "bank".forex_forwards
           set status = 'failed', settled_at = $2, failure_reason = $3
         where id = $1 and status = 'active'`
	if _, err := s.DB.Exec(ctx, q, id, at, reason); err != nil {
		logger.From(ctx).ErrorContext(ctx, "mark forex forward failed failed", "err", err, "forward_id", id)
		return apperr.Internal("mark forex forward failed", err)
	}
	return nil
}

// CancelForexForward flips an owner's still-'active' row to 'cancelled'.
// Owner-scoped + status-guarded in one statement so a concurrent
// settlement can't be cancelled mid-flight.
func (s *Store) CancelForexForward(ctx context.Context, id, clientID string) (*domain.ForexForward, error) {
	const q = `
        update "bank".forex_forwards
           set status = 'cancelled'
         where id = $1 and client_id = $2 and status = 'active'
        returning ` + forexForwardCols
	f, err := scanForexForward(s.DB.QueryRow(ctx, q, id, clientID))
	if err != nil {
		if noRows(err) {
			existing, gerr := s.GetForexForward(ctx, id)
			if gerr != nil {
				return nil, gerr
			}
			if existing.ClientID != clientID {
				return nil, apperr.NotFound("terminski ugovor ne postoji")
			}
			return nil, apperr.FailedPrecondition("terminski ugovor se više ne može otkazati")
		}
		logger.From(ctx).ErrorContext(ctx, "cancel forex forward failed", "err", err, "forward_id", id, "client_id", clientID)
		return nil, apperr.Internal("cancel forex forward", err)
	}
	return f, nil
}
