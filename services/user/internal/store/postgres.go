// Package store is the user service's persistence layer. All public
// methods take ctx and surface domain.* types; pgx errors are translated
// to apperr at the boundary so the service layer never sees pgx.
package store

import (
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store wraps a pgx pool with helpers shared across the per-aggregate
// query files in this package.
type Store struct {
	Pool *pgxpool.Pool
	// ReadPool routes SELECTs to a hot standby when set; nil means
	// reads go to the primary. BonusReadReplicaRouting / PR #287.
	ReadPool *pgxpool.Pool
}

// New returns a Store using pool.
func New(pool *pgxpool.Pool) *Store { return &Store{Pool: pool} }

// reader returns the read pool when configured, primary otherwise.
func (s *Store) reader() *pgxpool.Pool {
	if s.ReadPool != nil {
		return s.ReadPool
	}
	return s.Pool
}

// isUniqueViolation reports whether err is a Postgres unique-violation.
func isUniqueViolation(err error) bool {
	type pgErr interface {
		SQLState() string
	}
	var pe pgErr
	return errors.As(err, &pe) && pe.SQLState() == "23505"
}

// noRows reports whether err is pgx.ErrNoRows.
func noRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
