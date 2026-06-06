package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
)

const reservationCols = `
    id, account_id, op_id, amount::text, currency,
    state, op_kind, held_at, settled_at`

func scanReservation(row interface{ Scan(...any) error }) (*domain.Reservation, error) {
	var r domain.Reservation
	var state, currency string
	var settledAt *time.Time
	if err := row.Scan(
		&r.ID, &r.AccountID, &r.OpID, &r.Amount, &currency,
		&state, &r.OpKind, &r.HeldAt, &settledAt,
	); err != nil {
		return nil, err
	}
	r.Currency = domain.Currency(currency)
	r.State = domain.ReservationState(state)
	r.SettledAt = settledAt
	return &r, nil
}

// InsertReservation writes a fresh 'held' row inside the caller's tx.
// On unique-violation (same op_id retried), returns ErrReservationExists
// so the service layer can read the winner.
func (s *Store) InsertReservation(ctx context.Context, tx pgx.Tx, r *domain.Reservation) (*domain.Reservation, error) {
	const q = `
        insert into "bank".reservations (account_id, op_id, amount, currency, state, op_kind)
        values ($1, $2, $3::numeric, $4, 'held', $5)
        returning ` + reservationCols
	out, err := scanReservation(tx.QueryRow(ctx, q, r.AccountID, r.OpID, r.Amount, string(r.Currency), r.OpKind))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrReservationExists
		}
		return nil, apperr.Internal("insert reservation", err)
	}
	return out, nil
}

// GetReservationByOpID returns the reservation row keyed by SAGA op_id.
// NotFound when no reservation has been created for that op_id yet —
// callers that need a no-op response on a stray release should treat
// this as the empty case.
func (s *Store) GetReservationByOpID(ctx context.Context, opID string) (*domain.Reservation, error) {
	const q = `select ` + reservationCols + ` from "bank".reservations where op_id = $1`
	out, err := scanReservation(s.DB.QueryRow(ctx, q, opID))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("reservation not found")
		}
		return nil, apperr.Internal("get reservation", err)
	}
	return out, nil
}

// GetReservationByOpIDTx variant runs inside a transaction (FOR UPDATE
// so a concurrent commit/release blocks rather than races).
func (s *Store) GetReservationByOpIDTx(ctx context.Context, tx pgx.Tx, opID string) (*domain.Reservation, error) {
	const q = `select ` + reservationCols + ` from "bank".reservations where op_id = $1 for update`
	out, err := scanReservation(tx.QueryRow(ctx, q, opID))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("reservation not found")
		}
		return nil, apperr.Internal("get reservation tx", err)
	}
	return out, nil
}

// MarkReservationState flips the state column and stamps settled_at on
// terminal transitions. Inside the caller's tx; the caller verifies
// state precondition (e.g. only `held` may transition).
func (s *Store) MarkReservationState(ctx context.Context, tx pgx.Tx, opID string, state domain.ReservationState) error {
	const q = `
        update "bank".reservations
        set state      = $2,
            settled_at = case when $2 in ('committed','released') then now() else settled_at end
        where op_id = $1
        returning id`
	var got string
	if err := tx.QueryRow(ctx, q, opID, string(state)).Scan(&got); err != nil {
		if noRows(err) {
			return apperr.NotFound("reservation not found")
		}
		return apperr.Internal("update reservation state", err)
	}
	return nil
}

// ErrReservationExists signals that an InsertReservation lost the
// unique-violation race against a concurrent retry. The service layer
// reads the winner's row and returns it as the idempotent response.
var ErrReservationExists = errors.New("reservation already exists for op_id")
