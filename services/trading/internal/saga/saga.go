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
	StatusFailed       Status = "failed"
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
	CurrentStep   string
	State         json.RawMessage
	Status        Status
	Attempts      int
	AttemptsMax   int
	LastError     string
	NextAttemptAt time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
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
	steps []driverStep
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
	r.drivers[def.Type] = driver{steps: steps}
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
	// Also surface a "terminal-failed" condition as an error so a
	// caller blind to status sees something went wrong. Permanent
	// forward errors that ran compensation to completion still leave
	// the saga at status=failed; resume only reports the immediate
	// error, so we synthesize one here for the failed case.
	if resumeErr == nil && row.Status == StatusFailed && row.LastError != "" {
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
	if row.Status == StatusCompleted || row.Status == StatusFailed {
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
		if s.forward == nil {
			// No-op step — advance.
			row.CurrentStep = s.name
			row.LastError = ""
			row.Attempts = 0
			row.NextAttemptAt = o.now()
			if err := o.Store.Update(ctx, row); err != nil {
				return err
			}
			continue
		}
		var err error
		if ferr := forceForwardErr(ctx, s.name); ferr != nil {
			log.Warn("saga: fault-injection forward fail",
				"step", s.name, "err", ferr.Error())
			err = ferr
		} else {
			err = s.forward(ctx, stepCtx)
		}
		if err == nil {
			row.CurrentStep = s.name
			row.LastError = ""
			row.Attempts = 0
			row.NextAttemptAt = o.now()
			if err := o.Store.Update(ctx, row); err != nil {
				return err
			}
			continue
		}
		if isPermanent(err) {
			log.Warn("saga: permanent error → compensating",
				"step", s.name, "err", err.Error())
			row.LastError = err.Error()
			row.Status = StatusCompensating
			// Don't bump CurrentStep — the failed step never committed,
			// so compensations start at the previous step.
			row.NextAttemptAt = o.now()
			row.Attempts = 0
			if uerr := o.Store.Update(ctx, row); uerr != nil {
				return uerr
			}
			return o.runCompensations(ctx, row, drv, log)
		}
		// Transient error — bump attempts, schedule retry.
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
			return uerr
		}
		return err
	}

	// All steps succeeded.
	row.Status = StatusCompleted
	row.LastError = ""
	row.NextAttemptAt = o.now()
	return o.Store.Update(ctx, row)
}

// runCompensations walks the completed steps in reverse and runs each
// step's Compensate. Compensations are best-effort: a transient
// failure parks the saga for retry; a permanent compensation failure
// flips the saga to status='failed' with the error recorded.
func (o *Orchestrator) runCompensations(ctx context.Context, row *Row, drv driver, log *slog.Logger) error {
	startIdx := indexOfStep(drv, row.CurrentStep)
	if startIdx < 0 {
		// Nothing to compensate — the failure was at step 0.
		row.Status = StatusFailed
		row.NextAttemptAt = o.now()
		return o.Store.Update(ctx, row)
	}
	// Preserve the original forward-step error across compensation
	// updates — it's the audit-trail "why did this saga fail" line.
	// Each successful compensation does clear `last_error` on its
	// own row update; we re-stamp at the terminal failed write below.
	origFailure := row.LastError
	for i := startIdx; i >= 0; i-- {
		s := drv.steps[i]
		if s.compensate == nil {
			// Read-only step or no-op compensation — skip but update
			// current_step pointer.
			row.CurrentStep = previousStepName(drv, i)
			row.LastError = ""
			row.Attempts = 0
			if err := o.Store.Update(ctx, row); err != nil {
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
		var err error
		if ferr := forceCompensateErr(ctx, s.name); ferr != nil {
			log.Warn("saga: fault-injection compensate fail",
				"step", s.name, "err", ferr.Error())
			err = ferr
		} else {
			err = s.compensate(ctx, stepCtx)
		}
		if err == nil {
			row.CurrentStep = previousStepName(drv, i)
			row.LastError = ""
			row.Attempts = 0
			row.NextAttemptAt = o.now()
			if uerr := o.Store.Update(ctx, row); uerr != nil {
				return uerr
			}
			continue
		}
		if isPermanent(err) {
			log.Error("saga: compensation permanent error → failing",
				"step", s.name, "err", err.Error())
			row.Status = StatusFailed
			row.LastError = err.Error()
			row.NextAttemptAt = o.now()
			if uerr := o.Store.Update(ctx, row); uerr != nil {
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
		}
		if uerr := o.Store.Update(ctx, row); uerr != nil {
			return uerr
		}
		return err
	}

	row.Status = StatusFailed
	if origFailure != "" {
		row.LastError = origFailure
	}
	row.NextAttemptAt = o.now()
	return o.Store.Update(ctx, row)
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

// ForceFail asks the orchestrator to fail the named forward step
// instead of calling its Forward. Kind picks the gRPC error code class:
//
//   - "permanent" (default): codes.FailedPrecondition → compensations
//     run in reverse for prior completed steps; the saga ends in
//     status=failed.
//   - "transient": codes.Unavailable → runForward parks the saga for
//     the recovery worker; attempts is bumped, next_attempt_at is set
//     per the backoff schedule. Use this to exercise S5 (retry).
type ForceFail struct {
	Step string
	Kind string // "permanent" | "transient"
}

// ForceCompensateFail asks the orchestrator to fail the named step's
// Compensate. Always permanent — a compensation that legitimately fails
// past the retry budget flips the saga to status=failed with
// last_error set, mirroring S9.
type ForceCompensateFail struct {
	Step string
}

// WithForceFail attaches a Forward fault directive to ctx. The
// directive is consumed when runForward reaches the matching step
// name; non-matching steps are unaffected.
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

// forceForwardErr returns a synthetic Forward error if ctx carries a
// directive matching stepName, nil otherwise.
func forceForwardErr(ctx context.Context, stepName string) error {
	v, ok := ctx.Value(forceFailKey{}).(ForceFail)
	if !ok || v.Step != stepName {
		return nil
	}
	if v.Kind == "transient" {
		return status.Error(codes.Unavailable, "saga: fault-injection (transient)")
	}
	return status.Error(codes.FailedPrecondition, "saga: fault-injection (permanent)")
}

// forceCompensateErr returns a synthetic Compensate error if ctx
// carries a directive matching stepName, nil otherwise. Compensate
// faults are always permanent.
func forceCompensateErr(ctx context.Context, stepName string) error {
	v, ok := ctx.Value(forceCompFailKey{}).(ForceCompensateFail)
	if !ok || v.Step != stepName {
		return nil
	}
	return status.Error(codes.FailedPrecondition, "saga: fault-injection (compensate)")
}

// FaultsFromMetadata reads the trading-debug fault-injection headers
// out of incoming gRPC metadata. grpc-gateway forwards request headers
// as "grpcgateway-<lower-header>" by default, so:
//
//   - X-Saga-Force-Fail        → grpcgateway-x-saga-force-fail
//   - X-Saga-Force-Fail-Kind   → grpcgateway-x-saga-force-fail-kind
//   - X-Saga-Force-Compensate-Fail → grpcgateway-x-saga-force-compensate-fail
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
	comp := firstMD(md, "grpcgateway-x-saga-force-compensate-fail")
	if step != "" {
		ctx = WithForceFail(ctx, ForceFail{Step: step, Kind: kind})
	}
	if comp != "" {
		ctx = WithForceCompensateFail(ctx, ForceCompensateFail{Step: comp})
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
