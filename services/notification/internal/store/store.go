// Package store holds the notification service's postgres queries.
// Hand-written pgx; one file per aggregate.
package store

import "github.com/jackc/pgx/v5/pgxpool"

// Store wraps the postgres pool. The notification feed is low-traffic,
// so a single pool (no RW/RO split) is sufficient.
type Store struct {
	Pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store { return &Store{Pool: pool} }
