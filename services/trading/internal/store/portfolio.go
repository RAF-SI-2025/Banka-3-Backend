package store

import (
	"context"
	"strings"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/postgres"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/jackc/pgx/v5"
)

const holdingCols = `
    id, user_id, user_kind, security_id, account_id,
    quantity, weighted_avg_price::text, public_count, reserved_count,
    acquired_at, updated_at`

// GetHolding returns the (user, security, account) holding row or
// NotFound. Used by the execution worker to recompute weighted-avg
// cost basis on each fill.
func (s *Store) GetHolding(ctx context.Context, tx pgx.Tx, userID, userKind, securityID, accountID string) (*domain.Holding, error) {
	q := `select ` + holdingCols + `
	      from "trading".portfolio_holdings
	      where user_id = $1 and user_kind = $2 and security_id = $3 and account_id = $4`
	row := tx.QueryRow(ctx, q, userID, userKind, securityID, accountID)
	out, err := scanHolding(row)
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("holding not found")
		}
		logger.From(ctx).ErrorContext(ctx, "get holding failed", "err", err, "user_id", userID, "security_id", securityID)
		return nil, apperr.Internal("get holding", err)
	}
	return out, nil
}

// ApplyBuyFill upserts a holding row by adding `qty` units at
// `pricePerUnit`. Cost basis becomes a weighted average of existing
// + new. Runs inside the caller's tx.
func (s *Store) ApplyBuyFill(
	ctx context.Context, tx pgx.Tx,
	userID, userKind, securityID, accountID string,
	qty int32, pricePerUnit string,
) (*domain.Holding, error) {
	const q = `
        insert into "trading".portfolio_holdings
            (user_id, user_kind, security_id, account_id, quantity, weighted_avg_price)
        values ($1, $2, $3, $4, $5, $6::numeric)
        on conflict (user_id, security_id, account_id) do update set
            weighted_avg_price = (
                "trading".portfolio_holdings.weighted_avg_price * "trading".portfolio_holdings.quantity
                + excluded.weighted_avg_price * excluded.quantity
            ) / ("trading".portfolio_holdings.quantity + excluded.quantity),
            quantity   = "trading".portfolio_holdings.quantity + excluded.quantity,
            updated_at = now()
        returning ` + holdingCols
	row := tx.QueryRow(ctx, q, userID, userKind, securityID, accountID, qty, pricePerUnit)
	out, err := scanHolding(row)
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "apply buy fill failed", "err", err, "user_id", userID, "security_id", securityID)
		return nil, apperr.Internal("apply buy fill", err)
	}
	return out, nil
}

// ApplySellFill decrements the holding's quantity by `qty`. Returns
// the updated row plus a snapshot of the row's pre-decrement
// weighted_avg_price so callers can compute realized gains.
//
// `public_count` is clamped down to the new quantity atomically:
// without this, a seller who exercised OTC delivery (or sold on the
// market) would keep an inflated public_count, and the discovery
// board would over-report `public_count - reserved_count`. The schema
// CHECK `public_count <= quantity` (migration 0014) is the backstop.
//
// Errors with FailedPrecondition when the user doesn't own enough.
func (s *Store) ApplySellFill(
	ctx context.Context, tx pgx.Tx,
	userID, userKind, securityID, accountID string,
	qty int32,
) (avgPrice string, updated *domain.Holding, err error) {
	const q = `
        update "trading".portfolio_holdings
        set quantity     = quantity - $5,
            public_count = least(public_count, quantity - $5),
            updated_at   = now()
        where user_id = $1 and user_kind = $2 and security_id = $3 and account_id = $4
          and quantity >= $5
        returning weighted_avg_price::text, ` + holdingCols
	var avg string
	row := tx.QueryRow(ctx, q, userID, userKind, securityID, accountID, qty)
	var h domain.Holding
	var t string
	if err := row.Scan(
		&avg,
		&h.ID, &h.UserID, &t, &h.SecurityID, &h.AccountID,
		&h.Quantity, &h.WeightedAvgPrice, &h.PublicCount, &h.ReservedCount,
		&h.AcquiredAt, &h.UpdatedAt,
	); err != nil {
		if noRows(err) {
			return "", nil, apperr.FailedPrecondition("nedovoljna količina za prodaju")
		}
		logger.From(ctx).ErrorContext(ctx, "apply sell fill failed", "err", err, "user_id", userID, "security_id", securityID)
		return "", nil, apperr.Internal("apply sell fill", err)
	}
	h.UserKind = domain.UserKind(t)
	return avg, &h, nil
}

// HoldingFilter narrows ListHoldings.
type HoldingFilter struct {
	UserID     string
	UserKind   domain.UserKind
	SecurityID string
}

// ListHoldings returns matching rows with quantity > 0. Empty filter
// returns every holding (supervisor view); narrowed filters scope.
func (s *Store) ListHoldings(ctx context.Context, f HoldingFilter) ([]*domain.Holding, error) {
	var conds []string
	var args []any
	add := func(cond string, a any) {
		args = append(args, a)
		conds = append(conds, strings.ReplaceAll(cond, "?", intArg(len(args))))
	}
	add("quantity > ?", 0)
	if f.UserID != "" {
		add("user_id = ?", f.UserID)
	}
	if f.UserKind != "" {
		add("user_kind = ?", string(f.UserKind))
	}
	if f.SecurityID != "" {
		add("security_id = ?", f.SecurityID)
	}
	where := " where " + strings.Join(conds, " and ")
	q := `select ` + holdingCols + ` from "trading".portfolio_holdings` + where + ` order by updated_at desc`
	rows, err := s.DB.Query(postgres.WithRead(ctx), q, args...)
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "list holdings failed", "err", err)
		return nil, apperr.Internal("list holdings", err)
	}
	defer rows.Close()
	var out []*domain.Holding
	for rows.Next() {
		h, err := scanHolding(rows)
		if err != nil {
			logger.From(ctx).ErrorContext(ctx, "scan holding failed", "err", err)
			return nil, apperr.Internal("scan holding", err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		logger.From(ctx).ErrorContext(ctx, "list holdings rows failed", "err", err)
		return out, err
	}
	return out, nil
}

// SetPublicCount updates the spec p.61 OTC public-share count for c4.
// Lands now so the column doesn't break later schema migrations.
func (s *Store) SetPublicCount(ctx context.Context, holdingID string, count int32) (*domain.Holding, error) {
	const q = `
        update "trading".portfolio_holdings
        set public_count = $2, updated_at = now()
        where id = $1
        returning ` + holdingCols
	row := s.DB.QueryRow(ctx, q, holdingID, count)
	out, err := scanHolding(row)
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("holding ne postoji")
		}
		logger.From(ctx).ErrorContext(ctx, "set public count failed", "err", err, "holding_id", holdingID)
		return nil, apperr.Internal("set public count", err)
	}
	return out, nil
}

// GetHoldingByID for the SetPublicCount auth check.
func (s *Store) GetHoldingByID(ctx context.Context, id string) (*domain.Holding, error) {
	q := `select ` + holdingCols + ` from "trading".portfolio_holdings where id = $1`
	out, err := scanHolding(s.DB.QueryRow(ctx, q, id))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("holding ne postoji")
		}
		logger.From(ctx).ErrorContext(ctx, "get holding failed", "err", err, "id", id)
		return nil, apperr.Internal("get holding", err)
	}
	return out, nil
}

// GetHoldingForUpdate reads a holding row with `for update` inside the
// caller's tx. OTC create needs this: the pre-flight availability check
// (`public_count - reserved_count >= qty`) is otherwise a stale read,
// and two concurrent CreateOTCOffer calls on the same holding can both
// pass and race the reservation increment. The DB CHECK catches the
// second one as FailedPrecondition, but the lock lets us serialize
// cleanly so the second caller sees the post-increment state.
func (s *Store) GetHoldingForUpdate(ctx context.Context, tx pgx.Tx, id string) (*domain.Holding, error) {
	const q = `select ` + holdingCols + ` from "trading".portfolio_holdings
	           where id = $1 for update`
	out, err := scanHolding(tx.QueryRow(ctx, q, id))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("holding ne postoji")
		}
		logger.From(ctx).ErrorContext(ctx, "lock holding failed", "err", err, "id", id)
		return nil, apperr.Internal("lock holding", err)
	}
	return out, nil
}

func scanHolding(row pgx.Row) (*domain.Holding, error) {
	var h domain.Holding
	var t string
	if err := row.Scan(
		&h.ID, &h.UserID, &t, &h.SecurityID, &h.AccountID,
		&h.Quantity, &h.WeightedAvgPrice, &h.PublicCount, &h.ReservedCount,
		&h.AcquiredAt, &h.UpdatedAt,
	); err != nil {
		return nil, err
	}
	h.UserKind = domain.UserKind(t)
	return &h, nil
}

// IncrementReservedHolding bumps a holding's reserved_count by n inside
// the caller's tx. Spec p.68 — used when an OTC offer is created or
// counter-offered upward (the seller commits the corresponding shares)
// and when a signed contract activates. The database CHECK guarantees
// reserved_count ≤ quantity; a violation surfaces as FailedPrecondition
// so the SAGA forward step fails cleanly and rolls back.
func (s *Store) IncrementReservedHolding(ctx context.Context, tx pgx.Tx, holdingID string, n int32) (*domain.Holding, error) {
	if n <= 0 {
		return nil, apperr.Validation("delta mora biti pozitivan")
	}
	const q = `
        update "trading".portfolio_holdings
        set reserved_count = reserved_count + $2,
            updated_at     = now()
        where id = $1
        returning ` + holdingCols
	row := tx.QueryRow(ctx, q, holdingID, n)
	out, err := scanHolding(row)
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("holding ne postoji")
		}
		if isCheckViolation(err) {
			return nil, apperr.FailedPrecondition("nedovoljno raspoloživih akcija za rezervaciju")
		}
		logger.From(ctx).ErrorContext(ctx, "increment reserved failed", "err", err, "holding_id", holdingID)
		return nil, apperr.Internal("increment reserved", err)
	}
	return out, nil
}

// DecrementReservedHolding releases n units of reservation on a holding
// (offer withdrawn, contract expired/exercised). The CHECK guarantees
// reserved_count ≥ 0; a violation surfaces as Internal so a buggy SAGA
// compensation can't quietly poison the column.
func (s *Store) DecrementReservedHolding(ctx context.Context, tx pgx.Tx, holdingID string, n int32) (*domain.Holding, error) {
	if n <= 0 {
		return nil, apperr.Validation("delta mora biti pozitivan")
	}
	const q = `
        update "trading".portfolio_holdings
        set reserved_count = reserved_count - $2,
            updated_at     = now()
        where id = $1
        returning ` + holdingCols
	row := tx.QueryRow(ctx, q, holdingID, n)
	out, err := scanHolding(row)
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("holding ne postoji")
		}
		if isCheckViolation(err) {
			logger.From(ctx).ErrorContext(ctx, "rezervacija je negativna failed", "err", err, "holding_id", holdingID)
			return nil, apperr.Internal("rezervacija je negativna", err)
		}
		logger.From(ctx).ErrorContext(ctx, "decrement reserved failed", "err", err, "holding_id", holdingID)
		return nil, apperr.Internal("decrement reserved", err)
	}
	return out, nil
}
