// Package store is the bank service's persistence layer.
package store

import (
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	Pool *pgxpool.Pool
	// ReadPool routes read-only queries (SELECT … FROM …) to a hot
	// standby when set. Repos pick the read pool via Store.reader().
	// Defaults to the primary so call sites that haven't been
	// migrated stay correct. BonusReadReplicaRouting / PR #287.
	ReadPool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store { return &Store{Pool: pool} }

// reader returns the read pool when configured, primary otherwise.
// Use in SELECTs that don't need read-after-write consistency. Hot
// paths (account/balance lookups inside a transfer, …) MUST stay on
// the primary because async streaming replication can lag the
// committing write.
func (s *Store) reader() *pgxpool.Pool {
	if s.ReadPool != nil {
		return s.ReadPool
	}
	return s.Pool
}

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
