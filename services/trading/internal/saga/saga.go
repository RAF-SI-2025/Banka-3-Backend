// Package saga is the trading service's SAGA orchestrator. c4 OTC
// premium/exercise + fund invest/withdraw flows are all multi-step
// reservations against the bank service; the orchestrator drives them
// forward, runs compensations on failure, retries with backoff on
// transient errors, and persists every state transition to
// trading.saga_executions so a crashed worker can resume.
//
// Design notes
// ============
//
//   - A Step is a (Forward, Compensate) pair. Step handlers must be
//     idempotent — every step's effective op_id is
//     uuid.NewSHA1(transaction_id, step_name), so bank-side idempotency
//     on (op_id, leg_index) (bank migration 0011 + the 0012
//     reservations unique-on-op_id) protects against replay.
//   - A Definition is a registered (Type, Steps) pair. The OTC accept
//     SAGA, OTC exercise SAGA, fund-invest SAGA, etc. each register a
//     Definition at startup.
//   - The Orchestrator persists `current_step` + `state` (JSON payload)
//     after every forward step. On transient gRPC error (Unavailable,
//     DeadlineExceeded, ResourceExhausted, Aborted, Internal) it bumps
//     `attempts`, schedules a retry with exponential backoff (1s, 2s,
//     4s, …, capped at 5min), and bails when attempts > attempts_max.
//     On permanent error (InvalidArgument, FailedPrecondition,
//     PermissionDenied, NotFound, OutOfRange, Unauthenticated) it
//     flips to `compensating` and walks completed steps in reverse.
//   - A per-transaction_id advisory lock (pg_try_advisory_xact_lock)
//     guards against the recovery worker and a foreground call racing
//     on the same row.
package saga

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// =====================================================================
// Types
// =====================================================================

// Status mirrors the trading.saga_executions.status column.
type Status string

const (
	StatusRunning      Status = "running"
	StatusCompensating Status = "compensating"
	StatusCompleted    Status = "completed"
	// StatusCompensated is the clean-rollback terminal: every required
	// compensator ran to success, so all reservations are released and
	// the per-currency / per-symbol invariants are restored. Distinct
	// from StatusFailed, which means the saga could NOT finish its
	// rollback and needs manual intervention (SAGA_test.pdf I5).
	StatusCompensated Status = "compensated"
	StatusFailed      Status = "failed"
)

// LogEntry is one record in a saga's append-only attempt log. Step is
// the phase code — "F<n>" for the forward of step n (1-based), "C<n>"
// for its compensator. Result is "ok" or "err"; Error carries the
// failure detail when Result is "err". The log is the authoritative
// resume trail (SAGA_test.pdf I4): a coordinator that died mid-flight
// reads it back to know which forward steps committed and which
// compensators have already run.
type LogEntry struct {
	Step   string `json:"step"`
	Result string `json:"result"`
	Error  string `json:"error,omitempty"`
}

const (
	resultOK  = "ok"
	resultErr = "err"
)

// Step is one transition in a SAGA. Forward returns nil to advance to
// the next step; an error rolls back via Compensate on every prior
// completed step (in reverse).
//
// Both functions take a *Context which carries the saga's persisted
// `state` (decoded into the per-saga payload type), a deterministic
// per-step op_id, and a logger pinned to the saga.
//
// Compensate may be nil for read-only steps that have no effect to
// undo — the orchestrator simply skips it.
type Step[T any] struct {
	Name       string
	Forward    func(ctx context.Context, sc *Context[T]) error
	Compensate func(ctx context.Context, sc *Context[T]) error
}

// Definition registers a saga type with its ordered step list. The
// generic parameter is the payload type carried in `state`.
type Definition[T any] struct {
	Type  string
	Steps []Step[T]
	// CompensateOnTransient routes ALL forward-step errors — transient
	// included — straight to compensation, instead of parking the saga
	// for a forward retry. This matches SAGA_test.pdf's "Running →
	// Compensating on any error after log write" rule. Leave false
	// (the default) for sagas that prefer forward recovery on a
	// transient blip (funds, cross-bank 2PC); the OTC exercise saga
	// sets it true. Compensators always retry transiently regardless.
	CompensateOnTransient bool
}

// Context is what step handlers see. It carries the saga's payload
// (decoded from saga_executions.state) and a deterministic op_id for
// use with bank's idempotency-keyed RPCs.
type Context[T any] struct {
	// TransactionID is the saga's primary key. Step handlers should
	// avoid storing it inside the payload — it's authoritative on the
	// saga_executions row.
	TransactionID string
	// State is the payload. Forward steps may mutate it; the
	// orchestrator persists the mutated value at the end of each step.
	State *T
	// StepName is the current step's Name. Same as the matching
	// Step.Name; convenient for log fields.
	StepName string
	// OpID is uuid.NewSHA1(transaction_id, step_name). Bank RPCs that
	// dedupe on op_id (Reserve, Commit, SettleTrade, …) use this.
	OpID string
	// Log is the saga-pinned logger.
	Log *slog.Logger
}

// =====================================================================
// Store interface
// =====================================================================

// Store is the persistence surface the orchestrator needs. Trading's
// store/saga.go implements this against trading.saga_executions.
type Store interface {
	// Insert persists a new running saga. Returns ErrAlreadyExists if
	// `transaction_id` is taken — callers that want idempotent starts
	// should pre-check or use the SHA1-derived transaction_id pattern.
	Insert(ctx context.Context, row *Row) error
	// Get loads a saga by transaction_id. Returns nil, nil when not
	// found so callers can branch on "first run" vs "resume".
	Get(ctx context.Context, transactionID string) (*Row, error)
	// Update writes the current_step + state + status + attempts +
	// next_attempt_at + last_error fields. All atomic in one row update.
	Update(ctx context.Context, row *Row) error
	// TryLock attempts a per-transaction_id pg_try_advisory_xact_lock
	// within fn's tx. fn runs only when the lock is acquired; when the
	// lock is held by another worker, TryLock returns nil immediately
	// (the foreground call and the recovery sweep can both call without
	// blocking each other). The locking transaction commits after fn
	// returns, so the lock is held for as long as fn runs.
	TryLock(ctx context.Context, transactionID string, fn func(ctx context.Context) error) (acquired bool, err error)
	// DueForRecovery returns up to `limit` rows where status is
	// running/compensating and next_attempt_at <= now(). Used by the
	// recovery worker.
	DueForRecovery(ctx context.Context, limit int) ([]*Row, error)
}

// Row is the persisted shape of a saga execution. State is the raw
// JSON bytes; the orchestrator decodes them into the Definition's
// generic payload type per-step.
type Row struct {
	TransactionID string
	SagaType      string
	// CurrentStep is the step-name resume pointer: the last completed
	// forward step on the way out, walked backward as compensators run.
	// The orchestrator keys resume off this; it is NOT the spec's
	// numeric current_step (that's StepNo).
	CurrentStep string
	// StepNo is SAGA_test.pdf's numeric current_step: the ordinal
	// (1-based) of the last *attempted* phase, frozen at the failed
	// phase for the whole compensation walk.
	StepNo int
	// Log is the append-only per-attempt trail (see LogEntry).
	Log           []LogEntry
	State         json.RawMessage
	Status        Status
	Attempts      int
	AttemptsMax   int
	LastError     string
	NextAttemptAt time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// appendLog records one phase attempt on the row. kind is "F" or "C";
// stepIdx is the 0-based step index.
func (r *Row) appendLog(kind string, stepIdx int, result string, err error) {
	e := LogEntry{Step: fmt.Sprintf("%s%d", kind, stepIdx+1), Result: result}
	if err != nil {
		e.Error = err.Error()
	}
	r.Log = append(r.Log, e)
}

// ErrAlreadyExists is what Store.Insert returns when the transaction_id
// is taken. Callers may treat this as "saga is already running".
var ErrAlreadyExists = errors.New("saga: transaction_id already exists")

// =====================================================================
// Registry
// =====================================================================

// Registry maps saga types to their drivers. The generic parameter on
// Definition is erased at register time — drivers store closures that
// decode state into the appropriate payload type.
type Registry struct {
	mu      sync.RWMutex
	drivers map[string]driver
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{drivers: map[string]driver{}}
}

// driver is the type-erased view of a Definition. The orchestrator
// stays generic-free at the runtime layer; each Definition registers
// a driver that knows how to materialize its payload.
type driver struct {
	steps                 []driverStep
	compensateOnTransient bool
}

type driverStep struct {
	name       string
	forward    func(ctx context.Context, sc *runCtx) error
	compensate func(ctx context.Context, sc *runCtx) error
}

// runCtx is the type-erased Context the driver hands the per-step
// closures. The wrapper handles decoding state → T, calling the
// generic-typed handler, and re-encoding state.
type runCtx struct {
	TransactionID string
	StepName      string
	OpID          string
	Log           *slog.Logger
	StateRaw      *json.RawMessage
}

// Register adds a Definition to the registry. Subsequent reads via
// Orchestrator.Run resolve the saga type to its driver here.
//
// Generic at-call-site, erased at storage — the registry can hold
// definitions for differently-typed payloads side-by-side.
func Register[T any](r *Registry, def Definition[T]) {
	if def.Type == "" {
		panic("saga: Definition.Type required")
	}
	if len(def.Steps) == 0 {
		panic("saga: Definition.Steps must be non-empty")
	}
	steps := make([]driverStep, 0, len(def.Steps))
	for _, s := range def.Steps {
		s := s // capture
		ds := driverStep{name: s.Name}
		if s.Forward != nil {
			ds.forward = wrap[T](s.Forward)
		}
		if s.Compensate != nil {
			ds.compensate = wrap[T](s.Compensate)
		}
		steps = append(steps, ds)
	}
	r.mu.Lock()
	r.drivers[def.Type] = driver{steps: steps, compensateOnTransient: def.CompensateOnTransient}
	r.mu.Unlock()
}

func wrap[T any](fn func(ctx context.Context, sc *Context[T]) error) func(ctx context.Context, sc *runCtx) error {
	return func(ctx context.Context, sc *runCtx) error {
		var payload T
		if len(*sc.StateRaw) > 0 {
			if err := json.Unmarshal(*sc.StateRaw, &payload); err != nil {
				return fmt.Errorf("saga: decode state: %w", err)
			}
		}
		typed := &Context[T]{
			TransactionID: sc.TransactionID,
			State:         &payload,
			StepName:      sc.StepName,
			OpID:          sc.OpID,
			Log:           sc.Log,
		}
		if err := fn(ctx, typed); err != nil {
			return err
		}
		// Persist potentially-mutated payload back to the raw view.
		buf, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("saga: encode state: %w", err)
		}
		*sc.StateRaw = buf
		return nil
	}
}

// Lookup returns the driver for `sagaType` or ok=false.
func (r *Registry) Lookup(sagaType string) (driver, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.drivers[sagaType]
	return d, ok
}

// =====================================================================
// Orchestrator
// =====================================================================

// Orchestrator drives sagas through their step lists.
type Orchestrator struct {
	Store    Store
	Registry *Registry
	Log      *slog.Logger
	// Now is the wall-clock; tests pin it.
	Now func() time.Time
	// MaxBackoff caps the per-attempt retry delay. Default 5min.
	MaxBackoff time.Duration
}

// New constructs an Orchestrator with sane defaults.
func New(store Store, reg *Registry, log *slog.Logger) *Orchestrator {
	return &Orchestrator{Store: store, Registry: reg, Log: log}
}

func (o *Orchestrator) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

func (o *Orchestrator) maxBackoff() time.Duration {
	if o.MaxBackoff > 0 {
		return o.MaxBackoff
	}
	return 5 * time.Minute
}

// StartInput is the foreground call: create a new saga and run it to
// completion (or until the first transient error parks it for the
// recovery worker).
type StartInput[T any] struct {
	TransactionID string
	SagaType      string
	InitialState  T
	AttemptsMax   int
}

// Start inserts a new saga row and runs it forward. Returns when the
// saga reaches terminal status (completed or failed) or when a
// transient error parks it; the recovery worker resumes parked sagas.
//
// Idempotent on TransactionID: if a saga with that id already exists,
// Start drives it forward from its current state rather than failing.
// Callers that want strict "create or fail" semantics should pre-check.
func Start[T any](ctx context.Context, o *Orchestrator, in StartInput[T]) (*Row, error) {
	if in.TransactionID == "" || in.SagaType == "" {
		return nil, errors.New("saga: TransactionID and SagaType required")
	}
	if _, ok := o.Registry.Lookup(in.SagaType); !ok {
		return nil, fmt.Errorf("saga: unknown type %q", in.SagaType)
	}
	state, err := json.Marshal(in.InitialState)
	if err != nil {
		return nil, fmt.Errorf("saga: encode initial state: %w", err)
	}
	max := in.AttemptsMax
	if max <= 0 {
		max = 8
	}
	row := &Row{
		TransactionID: in.TransactionID,
		SagaType:      in.SagaType,
		CurrentStep:   "", // empty = "ready to start with step 0"
		State:         state,
		Status:        StatusRunning,
		Attempts:      0,
		AttemptsMax:   max,
		NextAttemptAt: o.now(),
	}
	if err := o.Store.Insert(ctx, row); err != nil {
		if errors.Is(err, ErrAlreadyExists) {
			// Resume the existing saga rather than fail.
			existing, gerr := o.Store.Get(ctx, in.TransactionID)
			if gerr != nil {
				return nil, gerr
			}
			row = existing
		} else {
			return nil, err
		}
	}
	resumeErr := o.Resume(ctx, row.TransactionID)
	// Re-read the row so callers see the post-Resume state (attempts
	// bumped, status changed, payload updated). Surface the resume
	// error alongside the post-state when the caller wants both.
	updated, gerr := o.Store.Get(ctx, row.TransactionID)
	if gerr != nil {
		return row, gerr
	}
	if updated != nil {
		row = updated
	}
	// A transient step error that left the row at status=running is
	// the orchestrator's normal "I parked, retry later" signal — the
	// recovery worker will drive it forward. From the caller's POV
	// the saga is healthy and pending, not failed; suppress the err.
	// (The recovery worker still gets the original LastError via the
	// persisted row for logging/observability.)
	if resumeErr != nil && row.Status == StatusRunning {
		resumeErr = nil
	}
	// Also surface a terminal rollback as an error so a caller blind to
	// status sees something went wrong. A forward error that ran
	// compensation to completion leaves the saga at status=compensated
	// (clean rollback) or status=failed (rollback itself stuck);
	// runCompensations returns nil on a clean compensated terminal, so
	// we synthesize the originating-failure error here for both.
	if resumeErr == nil && (row.Status == StatusCompensated || row.Status == StatusFailed) && row.LastError != "" {
		resumeErr = errors.New(row.LastError)
	}
	return row, resumeErr
}

// Resume drives a saga forward (or backward, if compensating) from
// whatever state it's in. Used by the foreground Start path and the
// recovery worker.
//
// Concurrency: the saga's transaction_id is locked via
// pg_try_advisory_xact_lock so two workers can't drive the same saga
// at once. When the lock isn't acquired Resume returns nil immediately
// (the holding worker will continue).
func (o *Orchestrator) Resume(ctx context.Context, transactionID string) error {
	acquired, err := o.Store.TryLock(ctx, transactionID, func(ctx context.Context) error {
		return o.driveLocked(ctx, transactionID)
	})
	if err != nil {
		return err
	}
	if !acquired {
		o.Log.Debug("saga: skipped (lock held)", "transaction_id", transactionID)
	}
	return nil
}

// driveLocked is the inner driver — runs inside the advisory-lock tx.
func (o *Orchestrator) driveLocked(ctx context.Context, transactionID string) error {
	row, err := o.Store.Get(ctx, transactionID)
	if err != nil {
		return err
	}
	if row == nil {
		return fmt.Errorf("saga: %s not found", transactionID)
	}
	if row.Status == StatusCompleted || row.Status == StatusCompensated || row.Status == StatusFailed {
		return nil
	}
	drv, ok := o.Registry.Lookup(row.SagaType)
	if !ok {
		return fmt.Errorf("saga: unknown type %q", row.SagaType)
	}

	log := o.Log.With(
		"saga_type", row.SagaType,
		"transaction_id", row.TransactionID,
	)

	if row.Status == StatusCompensating {
		return o.runCompensations(ctx, row, drv, log)
	}
	return o.runForward(ctx, row, drv, log)
}

// runForward walks forward from the current step until the saga
// completes, hits a transient error (parks it), or hits a permanent
// error (flips to compensating and recurses).
func (o *Orchestrator) runForward(ctx context.Context, row *Row, drv driver, log *slog.Logger) error {
	startIdx := indexOfStep(drv, row.CurrentStep)
	// startIdx == -1 means the saga hasn't run any step yet (current_step="").
	// startIdx >= 0 means current_step already completed — resume from idx+1.
	resumeIdx := startIdx + 1

	for i := resumeIdx; i < len(drv.steps); i++ {
		s := drv.steps[i]
		stepCtx := &runCtx{
			TransactionID: row.TransactionID,
			StepName:      s.name,
			OpID:          DeriveOpID(row.TransactionID, s.name),
			Log:           log.With("step", s.name),
			StateRaw:      &row.State,
		}
		// StepNo tracks the last *attempted* phase (1-based) for the
		// spec's numeric current_step; stamp it before running so a
		// failure leaves it pointing at the failed phase.
		row.StepNo = i + 1
		if s.forward == nil {
			// No-op step — advance.
			row.CurrentStep = s.name
			row.appendLog("F", i, resultOK, nil)
			row.LastError = ""
			row.Attempts = 0
			row.NextAttemptAt = o.now()
			if err := o.Store.Update(ctx, row); err != nil {
				log.ErrorContext(ctx, "saga persist step failed", "err", err, "step", s.name)
				return err
			}
			continue
		}
		log.DebugContext(ctx, "saga step start", "step", s.name, "step_no", i+1)
		var err error
		if ferr := forceForwardErr(ctx, s.name, i); ferr != nil {
			log.Warn("saga: fault-injection forward fail",
				"step", s.name, "err", ferr.Error())
			err = ferr
		} else {
			forceDelay(ctx, s.name, i)
			err = s.forward(ctx, stepCtx)
		}
		if err == nil {
			row.CurrentStep = s.name
			row.appendLog("F", i, resultOK, nil)
			row.LastError = ""
			row.Attempts = 0
			row.NextAttemptAt = o.now()
			if err := o.Store.Update(ctx, row); err != nil {
				log.ErrorContext(ctx, "saga persist step failed", "err", err, "step", s.name)
				return err
			}
			log.InfoContext(ctx, "saga step completed", "step", s.name, "step_no", i+1)
			continue
		}
		// Forward error. Record the attempt, then decide: roll back, or
		// (for forward-recovery sagas) park the transient for retry.
		row.appendLog("F", i, resultErr, err)
		if isPermanent(err) || drv.compensateOnTransient {
			log.ErrorContext(ctx, "saga step forward failed, compensating",
				"err", err.Error(), "step", s.name, "permanent", isPermanent(err))
			row.LastError = err.Error()
			row.Status = StatusCompensating
			// Don't bump CurrentStep — the failed step never committed,
			// so compensations start at the previous step.
			row.NextAttemptAt = o.now()
			row.Attempts = 0
			if uerr := o.Store.Update(ctx, row); uerr != nil {
				log.ErrorContext(ctx, "saga persist compensating flip failed", "err", uerr, "step", s.name)
				return uerr
			}
			return o.runCompensations(ctx, row, drv, log)
		}
		// Transient error, forward-recovery mode — bump attempts,
		// schedule retry, leave the saga running for the recovery worker.
		row.Attempts++
		row.LastError = err.Error()
		row.NextAttemptAt = o.now().Add(backoff(row.Attempts, o.maxBackoff()))
		if row.Attempts > row.AttemptsMax {
			log.Error("saga: transient retries exhausted → failing",
				"step", s.name, "attempts", row.Attempts, "err", err.Error())
			row.Status = StatusFailed
		} else {
			log.Warn("saga: transient error, will retry",
				"step", s.name, "attempts", row.Attempts,
				"retry_after", row.NextAttemptAt.UTC().Format(time.RFC3339),
				"err", err.Error())
		}
		if uerr := o.Store.Update(ctx, row); uerr != nil {
			log.ErrorContext(ctx, "saga persist retry schedule failed", "err", uerr, "step", s.name)
			return uerr
		}
		return err
	}

	// All steps succeeded.
	row.Status = StatusCompleted
	row.StepNo = len(drv.steps)
	row.LastError = ""
	row.NextAttemptAt = o.now()
	if err := o.Store.Update(ctx, row); err != nil {
		log.ErrorContext(ctx, "saga persist completion failed", "err", err)
		return err
	}
	log.InfoContext(ctx, "saga completed", "steps", len(drv.steps))
	return nil
}

// runCompensations walks the completed steps in reverse and runs each
// step's Compensate. Compensations are best-effort: a transient
// failure parks the saga for retry; a permanent compensation failure
// flips the saga to status='failed' with the error recorded.
func (o *Orchestrator) runCompensations(ctx context.Context, row *Row, drv driver, log *slog.Logger) error {
	// StepNo stays frozen at the failed phase for the whole
	// compensation walk (SAGA_test.pdf: SG-05 reads current_step=3
	// through C2 and C1).
	startIdx := indexOfStep(drv, row.CurrentStep)
	if startIdx < 0 {
		// Nothing to compensate — the failure was at the first phase, so
		// no side effects committed. This is a clean rollback.
		row.Status = StatusCompensated
		row.NextAttemptAt = o.now()
		if err := o.Store.Update(ctx, row); err != nil {
			log.ErrorContext(ctx, "saga persist compensated terminal failed", "err", err)
			return err
		}
		log.InfoContext(ctx, "saga compensated", "last_error", row.LastError)
		return nil
	}
	// Preserve the original forward-step error across compensation
	// updates — it's the audit-trail "why did this saga roll back" line.
	// Each successful compensation clears `last_error` on its own row
	// update; we re-stamp at the terminal write below.
	origFailure := row.LastError
	for i := startIdx; i >= 0; i-- {
		s := drv.steps[i]
		if s.compensate == nil {
			// Read-only / no-op compensation — still record the phase as
			// attempted and OK (invariant I4 wants a record per phase),
			// then advance the resume pointer.
			row.appendLog("C", i, resultOK, nil)
			row.CurrentStep = previousStepName(drv, i)
			row.LastError = ""
			row.Attempts = 0
			if err := o.Store.Update(ctx, row); err != nil {
				log.ErrorContext(ctx, "saga persist compensation step failed", "err", err, "step", s.name)
				return err
			}
			continue
		}
		stepCtx := &runCtx{
			TransactionID: row.TransactionID,
			StepName:      s.name,
			OpID:          DeriveOpID(row.TransactionID, s.name),
			Log:           log.With("step", s.name, "phase", "compensate"),
			StateRaw:      &row.State,
		}
		log.DebugContext(ctx, "saga compensation step start", "step", s.name, "step_no", i+1)
		var err error
		if ferr := forceCompensateErr(ctx, s.name, i, row.Attempts); ferr != nil {
			log.Warn("saga: fault-injection compensate fail",
				"step", s.name, "attempt", row.Attempts, "err", ferr.Error())
			err = ferr
		} else {
			forceDelay(ctx, s.name, i)
			err = s.compensate(ctx, stepCtx)
		}
		if err == nil {
			row.appendLog("C", i, resultOK, nil)
			row.CurrentStep = previousStepName(drv, i)
			row.LastError = ""
			row.Attempts = 0
			row.NextAttemptAt = o.now()
			if uerr := o.Store.Update(ctx, row); uerr != nil {
				log.ErrorContext(ctx, "saga persist compensation step failed", "err", uerr, "step", s.name)
				return uerr
			}
			log.InfoContext(ctx, "saga compensation step completed", "step", s.name, "step_no", i+1)
			continue
		}
		// Compensation failed. Record the attempt. A compensator must
		// eventually succeed (idempotent, retried until it does), so a
		// transient failure parks the saga in `compensating` for the
		// recovery worker. Only a permanent compensator error or
		// retry-budget exhaustion flips to `failed` (genuinely stuck —
		// the pathological case I5 warns about).
		row.appendLog("C", i, resultErr, err)
		if isPermanent(err) {
			log.Error("saga: compensation permanent error → failing",
				"step", s.name, "err", err.Error())
			row.Status = StatusFailed
			row.LastError = err.Error()
			row.NextAttemptAt = o.now()
			if uerr := o.Store.Update(ctx, row); uerr != nil {
				log.ErrorContext(ctx, "saga persist failed terminal failed", "err", uerr, "step", s.name)
				return uerr
			}
			return err
		}
		row.Attempts++
		row.LastError = err.Error()
		row.NextAttemptAt = o.now().Add(backoff(row.Attempts, o.maxBackoff()))
		if row.Attempts > row.AttemptsMax {
			log.Error("saga: compensation transient retries exhausted → failing",
				"step", s.name, "attempts", row.Attempts, "err", err.Error())
			row.Status = StatusFailed
			if uerr := o.Store.Update(ctx, row); uerr != nil {
				log.ErrorContext(ctx, "saga persist failed terminal failed", "err", uerr, "step", s.name)
				return uerr
			}
			return err
		}
		log.WarnContext(ctx, "saga compensation transient error, will retry",
			"err", err.Error(), "step", s.name, "attempts", row.Attempts,
			"retry_after", row.NextAttemptAt.UTC().Format(time.RFC3339))
		if uerr := o.Store.Update(ctx, row); uerr != nil {
			log.ErrorContext(ctx, "saga persist retry schedule failed", "err", uerr, "step", s.name)
			return uerr
		}
		return err
	}

	// Every required compensator ran to success — clean rollback.
	row.Status = StatusCompensated
	if origFailure != "" {
		row.LastError = origFailure
	}
	row.NextAttemptAt = o.now()
	if err := o.Store.Update(ctx, row); err != nil {
		log.ErrorContext(ctx, "saga persist compensated terminal failed", "err", err)
		return err
	}
	log.InfoContext(ctx, "saga compensated", "last_error", row.LastError)
	return nil
}

// =====================================================================
// Debug fault injection
// =====================================================================
//
// These helpers expose a per-request hook for forcing a named step to
// fail. They exist so the cypress c4-tests scenarios that walk SAGA
// failure modes (transfer_strike fail, compensation fail, transient
// retry) can be driven from end-to-end FE tests; without them, the
// internal step failures are FE-indistinguishable from the natural
// "insufficient funds" failure since each one collapses to the same
// 5xx with a Serbian message.
//
// The mechanism is gated by the trading service's caller (an env-gated
// gRPC interceptor reads metadata and stamps the context). Production
// code paths never set the directive.

type forceFailKey struct{}
type forceCompFailKey struct{}
type injectDelayKey struct{}

// ForceFail asks the orchestrator to fail a forward step instead of
// calling its Forward. Step is matched either by the step's Name or by
// its phase code "F<n>" (1-based position), so a test can target the
// step the SAGA_test.pdf way (`X-Saga-Force-Fail: F3`) or by name.
// Kind picks the gRPC error code class:
//
//   - "permanent" (default): codes.FailedPrecondition.
//   - "transient": codes.Unavailable.
//
// Either way, for a Definition with CompensateOnTransient the failed
// forward routes to compensation; the failed step itself is never
// compensated (it never committed), so its own C<n> does not appear in
// the log — matching SG-05/06/07.
type ForceFail struct {
	Step string
	Kind string // "permanent" | "transient"
}

// ForceCompensateFail asks the orchestrator to fail a step's Compensate.
// Step matches by Name or by phase code "C<n>". Times shapes the failure:
//
//   - Times == 0: fail permanently (codes.FailedPrecondition) → the saga
//     gives up the rollback and ends `failed` immediately — the
//     genuinely-stuck case (I5).
//   - Times == N > 0: fail the first N attempts transiently
//     (codes.Unavailable), so the saga parks in `compensating` and the
//     compensator is retried, then succeed → `compensated`. SG-08 uses
//     Times=1, yielding a {C err}{C ok} pair.
type ForceCompensateFail struct {
	Step  string
	Times int
}

// InjectDelay sleeps inside the named forward phase (SAGA_test.pdf
// `X-Saga-Inject-Delay: F<n>:Nms`) to widen the window a concurrent
// fault (pause/kill) can land in. Matched by Name or "F<n>".
type InjectDelay struct {
	Step string
	Dur  time.Duration
}

// WithForceFail attaches a Forward fault directive to ctx.
func WithForceFail(ctx context.Context, d ForceFail) context.Context {
	if d.Step == "" {
		return ctx
	}
	return context.WithValue(ctx, forceFailKey{}, d)
}

// WithForceCompensateFail attaches a Compensate fault directive.
func WithForceCompensateFail(ctx context.Context, d ForceCompensateFail) context.Context {
	if d.Step == "" {
		return ctx
	}
	return context.WithValue(ctx, forceCompFailKey{}, d)
}

// WithInjectDelay attaches a per-phase delay directive.
func WithInjectDelay(ctx context.Context, d InjectDelay) context.Context {
	if d.Step == "" || d.Dur <= 0 {
		return ctx
	}
	return context.WithValue(ctx, injectDelayKey{}, d)
}

// stepMatches reports whether a fault directive targets the step at
// stepIdx — either by exact Name or by phase code (prefix "F"/"C" +
// 1-based ordinal, e.g. "F3", "C2").
func stepMatches(directive, stepName, prefix string, stepIdx int) bool {
	if directive == "" {
		return false
	}
	if directive == stepName {
		return true
	}
	return directive == fmt.Sprintf("%s%d", prefix, stepIdx+1)
}

// forceForwardErr returns a synthetic Forward error if ctx carries a
// directive matching the step at stepIdx, nil otherwise.
func forceForwardErr(ctx context.Context, stepName string, stepIdx int) error {
	v, ok := ctx.Value(forceFailKey{}).(ForceFail)
	if !ok || !stepMatches(v.Step, stepName, "F", stepIdx) {
		return nil
	}
	if v.Kind == "transient" {
		return status.Error(codes.Unavailable, "saga: fault-injection (transient)")
	}
	return status.Error(codes.FailedPrecondition, "saga: fault-injection (permanent)")
}

// forceCompensateErr returns a synthetic (transient) Compensate error
// if ctx carries a directive matching the step at stepIdx and the
// directive's failure budget is not yet spent. attempts is the number
// of failures this compensator has already logged.
func forceCompensateErr(ctx context.Context, stepName string, stepIdx, attempts int) error {
	v, ok := ctx.Value(forceCompFailKey{}).(ForceCompensateFail)
	if !ok || !stepMatches(v.Step, stepName, "C", stepIdx) {
		return nil
	}
	if v.Times <= 0 {
		// No retry budget — a permanent compensator failure that flips
		// the saga to the genuinely-stuck `failed` terminal.
		return status.Error(codes.FailedPrecondition, "saga: fault-injection (compensate)")
	}
	if attempts >= v.Times {
		return nil // budget spent — let the real compensator run
	}
	return status.Error(codes.Unavailable, "saga: fault-injection (compensate)")
}

// forceDelay sleeps if ctx carries a delay directive matching the
// forward step at stepIdx.
func forceDelay(ctx context.Context, stepName string, stepIdx int) {
	v, ok := ctx.Value(injectDelayKey{}).(InjectDelay)
	if !ok || !stepMatches(v.Step, stepName, "F", stepIdx) {
		return
	}
	t := time.NewTimer(v.Dur)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

// FaultsFromMetadata reads the trading-debug fault-injection headers
// out of incoming gRPC metadata. grpc-gateway forwards request headers
// as "grpcgateway-<lower-header>" by default. Both the SAGA_test.pdf
// header names and the original aliases are accepted:
//
//   - X-Saga-Force-Fail               (Fi or step name)
//   - X-Saga-Force-Fail-Kind          (permanent | transient)
//   - X-Saga-Compensate-Fail          (Ci or step name)
//   - X-Saga-Force-Compensate-Fail    (alias of the above)
//   - X-Saga-Compensate-Fail-Times    (N)
//   - X-Saga-Inject-Delay             (Fi:Nms)
//
// Returns ctx unchanged when enabled is false or when no directives
// are present. SAGA-kickoff handlers call this in front of saga.Start.
func FaultsFromMetadata(ctx context.Context, enabled bool) context.Context {
	if !enabled {
		return ctx
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ctx
	}
	step := firstMD(md, "grpcgateway-x-saga-force-fail")
	kind := firstMD(md, "grpcgateway-x-saga-force-fail-kind")
	comp := firstMD(md, "grpcgateway-x-saga-compensate-fail")
	if comp == "" {
		comp = firstMD(md, "grpcgateway-x-saga-force-compensate-fail")
	}
	times := atoiSafe(firstMD(md, "grpcgateway-x-saga-compensate-fail-times"))
	delay := firstMD(md, "grpcgateway-x-saga-inject-delay")
	if step != "" {
		ctx = WithForceFail(ctx, ForceFail{Step: step, Kind: kind})
	}
	if comp != "" {
		ctx = WithForceCompensateFail(ctx, ForceCompensateFail{Step: comp, Times: times})
	}
	if delay != "" {
		if d := parseInjectDelay(delay); d.Step != "" {
			ctx = WithInjectDelay(ctx, d)
		}
	}
	return ctx
}

func firstMD(md metadata.MD, key string) string {
	v := md.Get(key)
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

func atoiSafe(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}

// parseInjectDelay parses "F3:5000" / "F3:5000ms" into an InjectDelay.
func parseInjectDelay(s string) InjectDelay {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return InjectDelay{}
	}
	num := strings.TrimSuffix(strings.TrimSpace(parts[1]), "ms")
	ms, err := strconv.Atoi(num)
	if err != nil || ms <= 0 {
		return InjectDelay{}
	}
	return InjectDelay{Step: strings.TrimSpace(parts[0]), Dur: time.Duration(ms) * time.Millisecond}
}

// =====================================================================
// Helpers
// =====================================================================

// DeriveOpID returns the deterministic op_id for a step. The bank's
// `(op_id, leg_index)` unique index turns retries into no-ops.
func DeriveOpID(transactionID, stepName string) string {
	return uuid.NewSHA1(sagaNS, []byte(transactionID+"|"+stepName)).String()
}

// sagaNS is the v5 namespace UUID for SAGA op_id derivation. Constant
// so different code paths derive the same op_id from the same inputs.
var sagaNS = uuid.MustParse("3c5c4f15-8b86-4f6f-9d22-c0d4d9c8f7b3")

// indexOfStep returns the index of `name` in drv.steps, or -1 when
// `name` is empty or unmatched (a fresh saga's CurrentStep is "").
func indexOfStep(drv driver, name string) int {
	if name == "" {
		return -1
	}
	for i, s := range drv.steps {
		if s.name == name {
			return i
		}
	}
	return -1
}

// previousStepName returns the step before idx in drv (or "" when idx
// is 0). Used by the compensation loop to walk current_step backward.
func previousStepName(drv driver, idx int) string {
	if idx <= 0 {
		return ""
	}
	return drv.steps[idx-1].name
}

// isPermanent classifies a gRPC error as permanent (no retry) or
// transient (retry with backoff). Mirrors the classifier in
// services/trading/internal/service/execution.go so the SAGA and the
// pending-execution recovery agree on what's worth re-trying.
func isPermanent(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	if !ok {
		// Non-grpc error — treat as transient (DB blips, encoding
		// errors, …). Step authors should wrap permanent failures as
		// status.Error(codes.InvalidArgument, ...) for the classifier
		// to pick them up.
		return false
	}
	switch st.Code() {
	case codes.InvalidArgument,
		codes.FailedPrecondition,
		codes.PermissionDenied,
		codes.NotFound,
		codes.OutOfRange,
		codes.Unauthenticated:
		return true
	}
	return false
}

// backoff returns the delay before retry `n`. Exponential with cap,
// matching the pattern documented in the celina 4 plan:
//
//	attempt=1 → 1s
//	attempt=2 → 2s
//	attempt=3 → 4s
//	…
//	cap at maxBackoff (default 5min)
func backoff(attempt int, cap time.Duration) time.Duration {
	if attempt <= 0 {
		return 0
	}
	// 1s, 2s, 4s, 8s, … 2^(attempt-1) seconds.
	d := time.Second << (attempt - 1)
	if d <= 0 || d > cap {
		return cap
	}
	return d
}
