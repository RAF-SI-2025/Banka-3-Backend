// Package store is the trading service's persistence layer.
package store

import (
	"context"
	"errors"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/postgres"
	"github.com/jackc/pgx/v5"
)

// Store wraps a routed *postgres.DB. Each *.go file in this package
// groups queries by aggregate (actuaries, exchanges, …). Writes and
// transactions land on the primary; reads marked postgres.WithRead(ctx)
// route to the read replica. Flow/worker/saga/idempotency/cron/gate
// reads deliberately stay unmarked (primary) to avoid replica lag.
type Store struct {
	DB *postgres.DB
}

func New(db *postgres.DB) *Store { return &Store{DB: db} }

// noRows reports whether err wraps pgx.ErrNoRows.
func noRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// isCheckViolation reports whether err is a Postgres check-constraint
// violation (SQLSTATE 23514). Used by the OTC reservation helpers so a
// `reserved_count > quantity` attempt surfaces as FailedPrecondition
// instead of a generic Internal.
func isCheckViolation(err error) bool {
	type pgErr interface {
		SQLState() string
	}
	var pe pgErr
	return errors.As(err, &pe) && pe.SQLState() == "23514"
}

// isUniqueViolation reports whether err is a Postgres unique-constraint
// violation (SQLSTATE 23505). Used by SAGA persistence so a duplicate
// transaction_id surfaces cleanly to the orchestrator.
func isUniqueViolation(err error) bool {
	type pgErr interface {
		SQLState() string
	}
	var pe pgErr
	return errors.As(err, &pe) && pe.SQLState() == "23505"
}

// intArg returns "$N" for placeholder building in dynamic queries.
// The store keeps queries with up to ~16 placeholders; if a query
// needs more, prefer pgx.Identifier or static SQL.
func intArg(n int) string {
	switch n {
	case 1:
		return "$1"
	case 2:
		return "$2"
	case 3:
		return "$3"
	case 4:
		return "$4"
	case 5:
		return "$5"
	case 6:
		return "$6"
	case 7:
		return "$7"
	case 8:
		return "$8"
	case 9:
		return "$9"
	case 10:
		return "$10"
	case 11:
		return "$11"
	case 12:
		return "$12"
	case 13:
		return "$13"
	case 14:
		return "$14"
	case 15:
		return "$15"
	case 16:
		return "$16"
	}
	panic("intArg out of range")
}

// ExecuteAtomic runs fn inside a single pgx transaction; commits on
// nil return, rolls back on error.
func (s *Store) ExecuteAtomic(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return apperr.Internal("begin tx", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return apperr.Internal("commit tx", err)
	}
	return nil
}
