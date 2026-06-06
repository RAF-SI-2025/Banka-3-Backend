// Package store is the user service's persistence layer. All public
// methods take ctx and surface domain.* types; pgx errors are translated
// to apperr at the boundary so the service layer never sees pgx.
package store

import (
	"errors"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/postgres"
	"github.com/jackc/pgx/v5"
)

// Store wraps a routed *postgres.DB with helpers shared across the
// per-aggregate query files in this package. Writes and transactions
// land on the primary; reads marked postgres.WithRead(ctx) route to the
// read replica.
type Store struct {
	DB *postgres.DB
}

// New returns a Store using db.
func New(db *postgres.DB) *Store { return &Store{DB: db} }

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
