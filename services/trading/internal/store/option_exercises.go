package store

import (
	"context"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/jackc/pgx/v5"
)

const optionExerciseCols = `
    id, option_holding_id, user_id, user_kind,
    option_security_id, underlying_security_id, account_id, option_type,
    quantity, contract_size::text, strike_price::text,
    notional_amt::text, currency,
    coalesce(bank_op_id::text, ''),
    coalesce(realized_gain_id::text, ''),
    status, created_at, updated_at`

// InsertPendingOptionExercise writes a status='pending' row inside the
// caller's tx. The returned UUID is the deterministic op_id passed to
// bank.SettleTrade — idempotent on retry per bank migration 0011.
func (s *Store) InsertPendingOptionExercise(ctx context.Context, tx pgx.Tx, e *domain.OptionExercise) (*domain.OptionExercise, error) {
	const q = `
        insert into "trading".option_exercises
            (option_holding_id, user_id, user_kind,
             option_security_id, underlying_security_id, account_id, option_type,
             quantity, contract_size, strike_price, notional_amt, currency)
        values ($1,$2,$3,
                $4,$5,$6,$7,
                $8,$9::numeric,$10::numeric,$11::numeric,$12)
        returning ` + optionExerciseCols
	row := tx.QueryRow(ctx, q,
		e.OptionHoldingID, e.UserID, string(e.UserKind),
		e.OptionSecurityID, e.UnderlyingSecurityID, e.AccountID, string(e.OptionType),
		e.Quantity, e.ContractSize, e.StrikePrice, e.NotionalAmt, string(e.Currency),
	)
	out, err := scanOptionExercise(row)
	if err != nil {
		return nil, apperr.Internal("insert pending option exercise", err)
	}
	return out, nil
}

// MarkOptionExerciseSettled flips a pending row to status='settled' and
// stamps the bank op_id (and the realized_gain row id when one was
// inserted). Idempotent.
func (s *Store) MarkOptionExerciseSettled(ctx context.Context, tx pgx.Tx, exerciseID, bankOpID, realizedGainID string) error {
	const q = `
        update "trading".option_exercises
        set status           = 'settled',
            bank_op_id       = nullif($2,'')::uuid,
            realized_gain_id = nullif($3,'')::uuid,
            updated_at       = now()
        where id = $1`
	if _, err := tx.Exec(ctx, q, exerciseID, bankOpID, realizedGainID); err != nil {
		return apperr.Internal("mark option exercise settled", err)
	}
	return nil
}

// AdjustHoldingQuantity decrements (negative delta) or increments
// (positive delta) a holding's quantity inside the caller's tx. Used by
// option exercise to consume the option contract count without touching
// the weighted_avg_price (option's own basis is irrelevant after
// exercise — the cash + underlying effect is the only book-keeping).
//
// Errors with FailedPrecondition when a negative delta would push the
// row below zero.
func (s *Store) AdjustHoldingQuantity(ctx context.Context, tx pgx.Tx, holdingID string, delta int32) (*domain.Holding, error) {
	const q = `
        update "trading".portfolio_holdings
        set quantity   = quantity + $2,
            updated_at = now()
        where id = $1
          and quantity + $2 >= 0
        returning ` + holdingCols
	row := tx.QueryRow(ctx, q, holdingID, delta)
	out, err := scanHolding(row)
	if err != nil {
		if noRows(err) {
			return nil, apperr.FailedPrecondition("nedovoljno količine za promenu")
		}
		return nil, apperr.Internal("adjust holding quantity", err)
	}
	return out, nil
}

func scanOptionExercise(row pgx.Row) (*domain.OptionExercise, error) {
	var e domain.OptionExercise
	var k, ot string
	var c string
	if err := row.Scan(
		&e.ID, &e.OptionHoldingID, &e.UserID, &k,
		&e.OptionSecurityID, &e.UnderlyingSecurityID, &e.AccountID, &ot,
		&e.Quantity, &e.ContractSize, &e.StrikePrice,
		&e.NotionalAmt, &c,
		&e.BankOpID, &e.RealizedGainID,
		&e.Status, &e.CreatedAt, &e.UpdatedAt,
	); err != nil {
		return nil, err
	}
	e.UserKind = domain.UserKind(k)
	e.OptionType = domain.OptionType(ot)
	e.Currency = domain.Currency(c)
	return &e, nil
}
