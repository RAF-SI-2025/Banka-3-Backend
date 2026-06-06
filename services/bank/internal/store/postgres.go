// Package store is the bank service's persistence layer.
package store

import (
	"errors"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/postgres"
	"github.com/jackc/pgx/v5"
)

type Store struct {
	// DB routes writes/transactions to the primary and reads marked
	// postgres.WithRead(ctx) to the read replica. Hot paths
	// (account/balance lookups inside a transfer, idempotency guards,
	// 2PC and cron reads) deliberately stay unmarked so they hit the
	// primary — async streaming replication can lag a just-committed
	// write. See pkg/postgres for the routing contract.
	DB *postgres.DB
}

func New(db *postgres.DB) *Store { return &Store{DB: db} }

// IsUniqueViolation reports whether err is a Postgres unique-constraint
// violation. Exported so the service layer can detect conflict-on-retry
// when an idempotency key races (op_id + leg_index unique index).
func IsUniqueViolation(err error) bool {
	type pgErr interface {
		SQLState() string
	}
	var pe pgErr
	return errors.As(err, &pe) && pe.SQLState() == "23505"
}

func isUniqueViolation(err error) bool { return IsUniqueViolation(err) }

func isCheckViolation(err error) bool {
	type pgErr interface {
		SQLState() string
	}
	var pe pgErr
	return errors.As(err, &pe) && pe.SQLState() == "23514"
}

func noRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
