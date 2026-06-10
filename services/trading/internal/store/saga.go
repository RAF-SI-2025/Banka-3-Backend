package store

import (
	"context"
	"encoding/json"
	"errors"
	"hash/fnv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/saga"
)

// SagaStore adapts the trading.saga_executions table to the orchestrator's
// saga.Store interface. Kept in a separate type (rather than methods on
// *Store) so it can be passed to the orchestrator without exposing the
// rest of the store surface.
type SagaStore struct {
	Pool poolIface
}

// NewSagaStore returns a store bound to the given pgxpool. Caller is
// usually the app layer: pass `st.Pool` from store.New().
func (s *Store) Sagas() *SagaStore { return &SagaStore{Pool: s.DB.RW} }

// poolIface is the slice of pgxpool.Pool we depend on, so tests can
// stub it. The two methods cover both BeginTx (TryLock) and
// QueryRow/Query/Exec (everything else).
type poolIface interface {
	BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

const sagaCols = `
    transaction_id, saga_type, current_step, step_no, log, state,
    status, attempts, attempts_max, coalesce(last_error, ''),
    next_attempt_at, created_at, updated_at`

func scanSaga(row interface{ Scan(...any) error }) (*saga.Row, error) {
	var r saga.Row
	var status string
	var state []byte
	var logRaw []byte
	if err := row.Scan(
		&r.TransactionID, &r.SagaType, &r.CurrentStep, &r.StepNo, &logRaw, &state,
		&status, &r.Attempts, &r.AttemptsMax, &r.LastError,
		&r.NextAttemptAt, &r.CreatedAt, &r.UpdatedAt,
	); err != nil {
		return nil, err
	}
	r.Status = saga.Status(status)
	r.State = json.RawMessage(state)
	if len(logRaw) > 0 {
		if err := json.Unmarshal(logRaw, &r.Log); err != nil {
			return nil, err
		}
	}
	return &r, nil
}

// marshalLog encodes the saga's attempt log to JSON bytes, defaulting
// to an empty array so the not-null `log jsonb` column is satisfied.
func marshalLog(entries []saga.LogEntry) ([]byte, error) {
	if len(entries) == 0 {
		return []byte("[]"), nil
	}
	return json.Marshal(entries)
}

// Insert writes a new running saga row.
func (s *SagaStore) Insert(ctx context.Context, row *saga.Row) error {
	const q = `
        insert into "trading".saga_executions (
            transaction_id, saga_type, current_step, step_no, log, state,
            status, attempts, attempts_max, next_attempt_at
        ) values (
            $1, $2, $3, $4, $5::jsonb, $6::jsonb,
            $7, $8, $9, $10
        )`
	state := []byte(row.State)
	if len(state) == 0 {
		state = []byte("{}")
	}
	logRaw, err := marshalLog(row.Log)
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "encode saga log failed", "err", err, "transaction_id", row.TransactionID)
		return apperr.Internal("encode saga log", err)
	}
	_, err = s.Pool.Exec(ctx, q,
		row.TransactionID, row.SagaType, row.CurrentStep, row.StepNo, logRaw, state,
		string(row.Status), row.Attempts, row.AttemptsMax, row.NextAttemptAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return saga.ErrAlreadyExists
		}
		logger.From(ctx).ErrorContext(ctx, "insert saga failed", "err", err, "transaction_id", row.TransactionID)
		return apperr.Internal("insert saga", err)
	}
	return nil
}

// Get returns the saga row keyed by transaction_id, or nil when missing.
func (s *SagaStore) Get(ctx context.Context, transactionID string) (*saga.Row, error) {
	const q = `select ` + sagaCols + ` from "trading".saga_executions where transaction_id = $1`
	out, err := scanSaga(s.Pool.QueryRow(ctx, q, transactionID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		logger.From(ctx).ErrorContext(ctx, "get saga failed", "err", err, "transaction_id", transactionID)
		return nil, apperr.Internal("get saga", err)
	}
	return out, nil
}

// Update writes the mutable columns of a saga row.
func (s *SagaStore) Update(ctx context.Context, row *saga.Row) error {
	const q = `
        update "trading".saga_executions set
            current_step    = $2,
            step_no         = $3,
            log             = $4::jsonb,
            state           = $5::jsonb,
            status          = $6,
            attempts        = $7,
            attempts_max    = $8,
            last_error      = nullif($9, ''),
            next_attempt_at = $10,
            updated_at      = now()
        where transaction_id = $1`
	state := []byte(row.State)
	if len(state) == 0 {
		state = []byte("{}")
	}
	logRaw, err := marshalLog(row.Log)
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "encode saga log failed", "err", err, "transaction_id", row.TransactionID)
		return apperr.Internal("encode saga log", err)
	}
	tag, err := s.Pool.Exec(ctx, q,
		row.TransactionID, row.CurrentStep, row.StepNo, logRaw, state,
		string(row.Status), row.Attempts, row.AttemptsMax, row.LastError,
		row.NextAttemptAt,
	)
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "update saga failed", "err", err, "transaction_id", row.TransactionID)
		return apperr.Internal("update saga", err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("saga not found")
	}
	return nil
}

// TryLock takes pg_try_advisory_xact_lock(hashtext(transaction_id))
// inside a single transaction and runs fn there. Returns acquired=false
// (with no error) when the lock is held by another worker. fn does not
// run when the lock isn't acquired.
//
// We open a serializable-default tx (TxOptions{}) because the lock has
// to be tied to a tx — pg_try_advisory_xact_lock auto-releases on
// commit/rollback. fn's own writes (saga row updates via Update) go
// through the same pool but a different connection — that's fine: the
// advisory lock guards the saga's *progression*, not the saga row
// updates themselves (those are last-write-wins per attempt).
func (s *SagaStore) TryLock(ctx context.Context, transactionID string, fn func(ctx context.Context) error) (bool, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "begin saga lock tx failed", "err", err, "transaction_id", transactionID)
		return false, apperr.Internal("begin saga lock tx", err)
	}
	defer func() {
		if rerr := tx.Rollback(ctx); rerr != nil && !errors.Is(rerr, pgx.ErrTxClosed) {
			logger.From(ctx).WarnContext(ctx, "rollback saga lock tx failed", "err", rerr, "transaction_id", transactionID)
		}
	}()

	// Use bigint hash of the transaction_id so we don't depend on
	// hashtext (search_path-sensitive). FNV-1a is stable across runs
	// and collisions just block unrelated sagas occasionally — not a
	// correctness issue.
	key := lockKey(transactionID)
	var acquired bool
	if err := tx.QueryRow(ctx, `select pg_try_advisory_xact_lock($1)`, key).Scan(&acquired); err != nil {
		logger.From(ctx).ErrorContext(ctx, "advisory lock failed", "err", err, "transaction_id", transactionID)
		return false, apperr.Internal("advisory lock", err)
	}
	if !acquired {
		return false, nil
	}
	if err := fn(ctx); err != nil {
		return true, err
	}
	if err := tx.Commit(ctx); err != nil {
		logger.From(ctx).ErrorContext(ctx, "commit saga lock tx failed", "err", err, "transaction_id", transactionID)
		return true, apperr.Internal("commit saga lock tx", err)
	}
	return true, nil
}

// lockKey hashes transaction_id to a stable int64. FNV-1a 64-bit is
// good enough — collisions only block unrelated sagas briefly.
func lockKey(transactionID string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("saga:" + transactionID))
	return int64(h.Sum64())
}

// DueForRecovery returns up to `limit` sagas with status in
// running/compensating and next_attempt_at <= now(). Ordered by
// next_attempt_at so old sagas get attention before fresh ones.
func (s *SagaStore) DueForRecovery(ctx context.Context, limit int) ([]*saga.Row, error) {
	if limit <= 0 {
		limit = 100
	}
	q := `select ` + sagaCols + ` from "trading".saga_executions
	      where status in ('running','compensating')
	        and next_attempt_at <= $1
	      order by next_attempt_at
	      limit $2`
	rows, err := s.Pool.Query(ctx, q, time.Now(), limit)
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "list due sagas failed", "err", err)
		return nil, apperr.Internal("list due sagas", err)
	}
	defer rows.Close()
	var out []*saga.Row
	for rows.Next() {
		r, err := scanSaga(rows)
		if err != nil {
			logger.From(ctx).ErrorContext(ctx, "scan saga failed", "err", err)
			return nil, apperr.Internal("scan saga", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		logger.From(ctx).ErrorContext(ctx, "due for recovery rows failed", "err", err)
		return out, err
	}
	return out, nil
}
