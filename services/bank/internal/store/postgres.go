// Package store is the bank service's persistence layer.
package store

import (
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	Pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store { return &Store{Pool: pool} }

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
