package saga

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// =====================================================================
// In-memory Store for tests
// =====================================================================

// memStore is an in-memory saga.Store satisfying the orchestrator's
// persistence surface. The advisory lock is emulated with a per-row
// mutex; TryLock returns false when the row's mutex is held by
// another goroutine.
type memStore struct {
	mu   sync.Mutex
	rows map[string]*Row
	// locks is the per-row advisory-lock emulation.
	locks   map[string]*sync.Mutex
	locksMu sync.Mutex
}

func newMemStore() *memStore {
	return &memStore{rows: map[string]*Row{}, locks: map[string]*sync.Mutex{}}
}

func (m *memStore) Insert(_ context.Context, row *Row) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.rows[row.TransactionID]; ok {
		return ErrAlreadyExists
	}
	row.CreatedAt = time.Now()
	row.UpdatedAt = time.Now()
	cp := *row
	m.rows[row.TransactionID] = &cp
	return nil
}

func (m *memStore) Get(_ context.Context, id string) (*Row, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[id]
	if !ok {
		return nil, nil
	}
	cp := *r
	return &cp, nil
}

func (m *memStore) Update(_ context.Context, row *Row) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.rows[row.TransactionID]; !ok {
		return errors.New("not found")
	}
	row.UpdatedAt = time.Now()
	cp := *row
	m.rows[row.TransactionID] = &cp
	return nil
}

func (m *memStore) TryLock(ctx context.Context, id string, fn func(ctx context.Context) error) (bool, error) {
	m.locksMu.Lock()
	l, ok := m.locks[id]
	if !ok {
		l = &sync.Mutex{}
		m.locks[id] = l
	}
	m.locksMu.Unlock()
	if !l.TryLock() {
		return false, nil
	}
	defer l.Unlock()
	return true, fn(ctx)
}

func (m *memStore) DueForRecovery(_ context.Context, limit int) ([]*Row, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*Row
	for _, r := range m.rows {
		if r.Status != StatusRunning && r.Status != StatusCompensating {
			continue
		}
		if r.NextAttemptAt.After(time.Now()) {
			continue
		}
		cp := *r
		out = append(out, &cp)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// =====================================================================
// Tests
// =====================================================================

type echoPayload struct {
	Notes []string `json:"notes"`
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestForwardHappyPath drives a 3-step saga to completion and verifies
// each step ran, in order, and payload mutations from one step are
// visible to the next.
func TestForwardHappyPath(t *testing.T) {
	reg := NewRegistry()
	Register[echoPayload](reg, Definition[echoPayload]{
		Type: "test_echo",
		Steps: []Step[echoPayload]{
			{
				Name: "a",
				Forward: func(_ context.Context, sc *Context[echoPayload]) error {
					sc.State.Notes = append(sc.State.Notes, "a:fwd")
					return nil
				},
			},
			{
				Name: "b",
				Forward: func(_ context.Context, sc *Context[echoPayload]) error {
					sc.State.Notes = append(sc.State.Notes, "b:fwd")
					return nil
				},
			},
			{
				Name: "c",
				Forward: func(_ context.Context, sc *Context[echoPayload]) error {
					sc.State.Notes = append(sc.State.Notes, "c:fwd")
					return nil
				},
			},
		},
	})
	o := New(newMemStore(), reg, quietLogger())
	row, err := Start(context.Background(), o, StartInput[echoPayload]{
		TransactionID: "00000000-0000-0000-0000-000000000001",
		SagaType:      "test_echo",
		InitialState:  echoPayload{},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if row.Status != StatusCompleted {
		t.Fatalf("status = %s, want completed", row.Status)
	}
	var got echoPayload
	if err := json.Unmarshal(row.State, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := []string{"a:fwd", "b:fwd", "c:fwd"}
	if fmt.Sprint(got.Notes) != fmt.Sprint(want) {
		t.Errorf("notes=%v, want %v", got.Notes, want)
	}
	if row.CurrentStep != "c" {
		t.Errorf("current_step=%q, want c", row.CurrentStep)
	}
}

// TestPermanentErrorCompensatesInReverse stops at step 2's permanent
// failure and walks step 1's Compensate. Verifies (a) reverse order,
// (b) step 2's own Compensate isn't called (it never committed), and
// (c) terminal status is `failed`.
func TestPermanentErrorCompensatesInReverse(t *testing.T) {
	reg := NewRegistry()
	Register[echoPayload](reg, Definition[echoPayload]{
		Type: "test_perm",
		Steps: []Step[echoPayload]{
			{
				Name: "a",
				Forward: func(_ context.Context, sc *Context[echoPayload]) error {
					sc.State.Notes = append(sc.State.Notes, "a:fwd")
					return nil
				},
				Compensate: func(_ context.Context, sc *Context[echoPayload]) error {
					sc.State.Notes = append(sc.State.Notes, "a:comp")
					return nil
				},
			},
			{
				Name: "b",
				Forward: func(_ context.Context, sc *Context[echoPayload]) error {
					return status.Error(codes.InvalidArgument, "bad")
				},
				Compensate: func(_ context.Context, sc *Context[echoPayload]) error {
					sc.State.Notes = append(sc.State.Notes, "b:comp")
					return nil
				},
			},
		},
	})
	o := New(newMemStore(), reg, quietLogger())
	row, err := Start(context.Background(), o, StartInput[echoPayload]{
		TransactionID: "00000000-0000-0000-0000-000000000002",
		SagaType:      "test_perm",
		InitialState:  echoPayload{},
	})
	if err == nil {
		t.Fatalf("Start: expected error")
	}
	if row.Status != StatusFailed {
		t.Errorf("status = %s, want failed", row.Status)
	}
	var got echoPayload
	_ = json.Unmarshal(row.State, &got)
	want := []string{"a:fwd", "a:comp"}
	if fmt.Sprint(got.Notes) != fmt.Sprint(want) {
		t.Errorf("notes=%v, want %v (b should not compensate — it never committed)", got.Notes, want)
	}
}

// TestTransientErrorParksWithBackoff verifies that a transient error
// bumps attempts, schedules a future retry, and Resume on a later call
// makes progress when the step now succeeds. Tests the orchestrator's
// "park for the recovery worker" behaviour.
func TestTransientErrorParksWithBackoff(t *testing.T) {
	reg := NewRegistry()
	var hits int
	Register[echoPayload](reg, Definition[echoPayload]{
		Type: "test_transient",
		Steps: []Step[echoPayload]{
			{
				Name: "flaky",
				Forward: func(_ context.Context, sc *Context[echoPayload]) error {
					hits++
					if hits == 1 {
						return status.Error(codes.Unavailable, "bank down")
					}
					sc.State.Notes = append(sc.State.Notes, "flaky:fwd")
					return nil
				},
			},
		},
	})
	store := newMemStore()
	o := New(store, reg, quietLogger())
	o.MaxBackoff = 0 // disabled — first retry should set NextAttemptAt 1s ahead
	row, err := Start(context.Background(), o, StartInput[echoPayload]{
		TransactionID: "00000000-0000-0000-0000-000000000003",
		SagaType:      "test_transient",
		InitialState:  echoPayload{},
	})
	if err == nil {
		t.Fatalf("Start: expected transient error first time")
	}
	if row.Status != StatusRunning {
		t.Errorf("status = %s, want running", row.Status)
	}
	if row.Attempts != 1 {
		t.Errorf("attempts = %d, want 1", row.Attempts)
	}
	if !row.NextAttemptAt.After(time.Now()) {
		t.Errorf("next_attempt_at should be in the future")
	}

	// Backdate so the recovery worker would pick it up, then Resume.
	stored, _ := store.Get(context.Background(), row.TransactionID)
	stored.NextAttemptAt = time.Now().Add(-time.Second)
	_ = store.Update(context.Background(), stored)

	if err := o.Resume(context.Background(), row.TransactionID); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	final, _ := store.Get(context.Background(), row.TransactionID)
	if final.Status != StatusCompleted {
		t.Errorf("status after retry = %s, want completed", final.Status)
	}
	if hits != 2 {
		t.Errorf("hits = %d, want 2", hits)
	}
}

// TestDeriveOpIDDeterministic pins the (transaction_id, step_name) →
// uuid mapping so a retry of the same step keeps the same op_id and
// the bank's idempotency on (op_id, leg_index) does its job. Different
// step names must produce different op_ids; the same inputs must
// produce the same op_id across calls.
func TestDeriveOpIDDeterministic(t *testing.T) {
	a := DeriveOpID("tx-1", "step-a")
	b := DeriveOpID("tx-1", "step-a")
	c := DeriveOpID("tx-1", "step-b")
	d := DeriveOpID("tx-2", "step-a")
	if a != b {
		t.Errorf("same inputs → different op_id: %s vs %s", a, b)
	}
	if a == c {
		t.Errorf("different step names → same op_id: %s", a)
	}
	if a == d {
		t.Errorf("different transaction_ids → same op_id: %s", a)
	}
}

// TestBackoffCap pins the exponential-growth + cap formula so a hot
// loop on a transient-failing step doesn't burn cycles after the cap
// is hit.
func TestBackoffCap(t *testing.T) {
	cap := 5 * time.Minute
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{1, 1 * time.Second},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
		{4, 8 * time.Second},
		{5, 16 * time.Second},
		{6, 32 * time.Second},
		{7, 64 * time.Second},
		{8, 128 * time.Second},
		// 1s << 8 = 256s (4m16s) — still under the 5min cap, so the
		// raw exponential value is returned.
		{9, 256 * time.Second},
		// 1s << 9 = 512s (8m32s) — first attempt past the 5min cap.
		{10, cap},
		{20, cap},
	}
	for _, c := range cases {
		got := backoff(c.attempt, cap)
		if got != c.want {
			t.Errorf("backoff(%d) = %s, want %s", c.attempt, got, c.want)
		}
	}
}

// TestIsPermanent pins the gRPC code classifier so the SAGA and the
// existing pending-execution recovery sweep stay in sync.
func TestIsPermanent(t *testing.T) {
	permanent := []codes.Code{
		codes.InvalidArgument,
		codes.FailedPrecondition,
		codes.PermissionDenied,
		codes.NotFound,
		codes.OutOfRange,
		codes.Unauthenticated,
	}
	for _, c := range permanent {
		if !isPermanent(status.Error(c, "")) {
			t.Errorf("code %s should be permanent", c)
		}
	}
	transient := []codes.Code{
		codes.Unavailable,
		codes.DeadlineExceeded,
		codes.Internal,
		codes.Aborted,
		codes.ResourceExhausted,
	}
	for _, c := range transient {
		if isPermanent(status.Error(c, "")) {
			t.Errorf("code %s should be transient", c)
		}
	}
	// Non-gRPC errors are transient (DB blip, json error, …).
	if isPermanent(errors.New("plain")) {
		t.Errorf("plain error should be transient")
	}
	if isPermanent(nil) {
		t.Errorf("nil error should not be permanent")
	}
}
