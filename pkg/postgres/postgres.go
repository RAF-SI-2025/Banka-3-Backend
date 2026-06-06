// Package postgres wraps pgxpool with a primary/replica routing
// abstraction. A *DB holds a write pool (RW, always the primary) and
// an optional read pool (RO, a hot streaming standby exposed by CNPG's
// banka-pg-ro Service).
//
// # Routing contract
//
//   - Default: every query goes to RW. Safe under streaming-replica
//     lag; read-after-write always sees the latest.
//   - Opt-in: callers mark a context with WithRead(ctx) to send the
//     query to RO. Use for list/search/dashboard reads that tolerate
//     a few hundred ms of staleness.
//   - Writes (Exec) are hard-pinned to RW — the ctx marker is ignored,
//     so a stray WithRead on a code path that ends in INSERT/UPDATE/
//     DELETE doesn't silently misroute. Mutating statements issued via
//     Query+RETURNING under WithRead will fail loudly at the standby
//     (read-only server).
//   - Transactions: Begin / BeginTx always start on RW. Async
//     streaming replication can lag a commit, so "begin on replica,
//     read, commit" can see data that's stale even for queries that
//     look read-only at the call site. Use BeginRead for explicit
//     long-running read-only reports against RO.
//
// When RO is nil (DATABASE_READ_URL unset), the routing collapses to
// RW and WithRead is a no-op. Callers don't branch on config.
//
// # Migration from the old shape
//
// Existing service stores hold a raw `Pool *pgxpool.Pool` and an
// optional `ReadPool *pgxpool.Pool`, with a per-store `reader()`
// helper that picks one. Migrate per service:
//
//  1. In `internal/app/app.go`, replace the two postgres.Open calls
//     with a single OpenPair(ctx, rwDSN, roDSN). Hold the returned
//     *postgres.DB and pass it to store.New.
//  2. In `internal/store/postgres.go`, swap the Pool + ReadPool fields
//     for a single `DB *postgres.DB`. Delete the reader() helper.
//  3. Find call sites of s.Pool.Query/.QueryRow/.Exec/.Begin/.Acquire
//     and replace with s.DB.X (same signatures).
//  4. Find call sites of s.reader().Query/.QueryRow and replace with
//     s.DB.Query/.QueryRow(postgres.WithRead(ctx), ...). The
//     read-routing intent is now visible at the call site.
//  5. For multi-statement reporting that wants snapshot isolation on
//     the replica, use s.DB.BeginRead(ctx).
//
// The legacy Open / Ping helpers stay exported so any remaining
// `*pgxpool.Pool` consumers keep compiling during the migration.
package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Open dials Postgres and returns a connected pool. Caller closes via
// pool.Close(). Times out after 10s if the database is unreachable.
//
// Retained for callers that hold a raw *pgxpool.Pool; new code opens a
// routed *DB via OpenPair instead.
func Open(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	cfg.MaxConns = 10
	cfg.MinConns = 1
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute
	// otelpgx hooks into pgx's per-query callbacks so every SQL op
	// becomes a child span of the calling request's span. Query
	// parameters are omitted from the span (default) to keep PII like
	// account numbers / amounts out of Tempo — only the statement text
	// and timings are recorded.
	cfg.ConnConfig.Tracer = otelpgx.NewTracer()

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("new pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return pool, nil
}

// Ping returns nil if the pool can round-trip a query.
func Ping(ctx context.Context, pool *pgxpool.Pool) error {
	return pool.Ping(ctx)
}

// DB is a routed handle that dispatches reads to a replica pool when
// the caller marks the context with WithRead, and everything else to
// the primary. See the package doc for the full routing contract.
//
// The zero value is unusable — construct via NewDB or OpenPair.
type DB struct {
	// RW is the primary pool. Always non-nil for a valid DB.
	// Writes, transactions, and unmarked reads land here.
	RW *pgxpool.Pool

	// RO is the read-replica pool. May be nil; nil collapses
	// routing to RW (WithRead becomes a no-op). Backed by a
	// hot standby — async streaming, may lag the primary.
	RO *pgxpool.Pool
}

// NewDB wraps an existing pair of pools. rw is required; ro may be
// nil to opt out of replica routing.
func NewDB(rw, ro *pgxpool.Pool) *DB {
	if rw == nil {
		panic("postgres.NewDB: rw pool is required")
	}
	return &DB{RW: rw, RO: ro}
}

// OpenPair dials both endpoints and returns a routed DB. roDSN may be
// empty to skip the replica — the returned DB has RO == nil and all
// reads fall through to RW. If the ro dial fails the rw pool is
// closed before returning, so the caller never inherits a half-open
// handle.
func OpenPair(ctx context.Context, rwDSN, roDSN string) (*DB, error) {
	rw, err := Open(ctx, rwDSN)
	if err != nil {
		return nil, fmt.Errorf("rw: %w", err)
	}
	if roDSN == "" {
		return &DB{RW: rw}, nil
	}
	ro, err := Open(ctx, roDSN)
	if err != nil {
		rw.Close()
		return nil, fmt.Errorf("ro: %w", err)
	}
	return &DB{RW: rw, RO: ro}, nil
}

// Close releases both pools. Safe to call when RO is nil.
func (d *DB) Close() {
	if d.RO != nil {
		d.RO.Close()
	}
	d.RW.Close()
}

// Ping verifies both pools can round-trip. RO is skipped when unset.
func (d *DB) Ping(ctx context.Context) error {
	if err := d.RW.Ping(ctx); err != nil {
		return fmt.Errorf("rw: %w", err)
	}
	if d.RO != nil {
		if err := d.RO.Ping(ctx); err != nil {
			return fmt.Errorf("ro: %w", err)
		}
	}
	return nil
}

// route picks RO when WithRead is set and RO is configured; RW
// otherwise.
func (d *DB) route(ctx context.Context) *pgxpool.Pool {
	if d.RO != nil && IsRead(ctx) {
		return d.RO
	}
	return d.RW
}

// Query dispatches via ctx. Reads marked with WithRead go to RO;
// everything else goes to RW.
func (d *DB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return d.route(ctx).Query(ctx, sql, args...)
}

// QueryRow dispatches via ctx. Same rules as Query.
func (d *DB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return d.route(ctx).QueryRow(ctx, sql, args...)
}

// Exec always lands on RW — writes are never routed to a replica. The
// ctx marker is intentionally ignored so a stray WithRead deep in a
// call chain doesn't aim an INSERT/UPDATE/DELETE at the standby.
func (d *DB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return d.RW.Exec(ctx, sql, args...)
}

// Acquire dispatches via ctx. Holding an RO conn across a multi-step
// snapshot is fine; mutating statements on it will fail at the server
// because the standby refuses writes.
func (d *DB) Acquire(ctx context.Context) (*pgxpool.Conn, error) {
	return d.route(ctx).Acquire(ctx)
}

// Begin starts a transaction on RW. Transactions are hard-pinned to
// the primary because async streaming replication can lag a commit —
// "begin on replica, read, commit" can return stale rows even for
// queries that look read-only at the call site.
//
// Use BeginRead for explicit read-only reports against the replica.
func (d *DB) Begin(ctx context.Context) (pgx.Tx, error) {
	return d.RW.Begin(ctx)
}

// BeginTx starts a transaction on RW with caller-supplied options. See
// Begin for why tx routing is hard-coded to the primary.
func (d *DB) BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
	return d.RW.BeginTx(ctx, opts)
}

// BeginRead starts a read-only transaction on RO when configured, RW
// otherwise. For multi-statement reporting that wants snapshot
// isolation across a few SELECTs; mutating statements inside will
// fail because AccessMode is forced to ReadOnly.
func (d *DB) BeginRead(ctx context.Context) (pgx.Tx, error) {
	pool := d.RW
	if d.RO != nil {
		pool = d.RO
	}
	return pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
}

// Stats returns the underlying pool stats. ro is nil when no replica
// is configured.
func (d *DB) Stats() (rw, ro *pgxpool.Stat) {
	rw = d.RW.Stat()
	if d.RO != nil {
		ro = d.RO.Stat()
	}
	return rw, ro
}

// readKey is the ctx key for the read-routing marker. Unexported so
// callers can't fake the value; set via WithRead.
type readKey struct{}

// WithRead returns a context that routes queries through it to the
// replica pool. Composes with cancellation and deadlines as usual.
//
//	rows, err := s.DB.Query(postgres.WithRead(ctx), `select ...`)
//
// No-op when the DB has no replica configured.
func WithRead(ctx context.Context) context.Context {
	return context.WithValue(ctx, readKey{}, true)
}

// IsRead reports whether ctx carries the read-routing marker. Useful
// for middleware that tags or logs replica-bound traffic.
func IsRead(ctx context.Context) bool {
	v, _ := ctx.Value(readKey{}).(bool)
	return v
}
