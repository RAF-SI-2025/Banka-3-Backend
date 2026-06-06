package saga

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// =====================================================================
// SAGA_test.pdf conformance — SG-01 .. SG-11
// =====================================================================
//
// These drive the orchestrator against a synthetic five-phase saga
// shaped exactly like the spec's OTC-exercise flow:
//
//	F1 reserve_buyer_strike   C1 release buyer reservation
//	F2 reserve_seller_shares  C2 release seller reservation
//	F3 transfer_strike        C3 reverse transfer (+1 bank.transactions)
//	F4 transfer_shares        C4 reverse ownership
//	F5 finalize (contract!="važeći")  C5 restore "važeći"
//
// A *exWorld plays the part of the bank + holdings + contract row so we
// can assert the per-currency / per-symbol invariants (I1, I2), the
// reserved-columns-zeroed invariant (I3), the log-completeness invariant
// (I4), the terminal-status invariant (I5), and the contract-validity
// invariant (I6) — the load-bearing checks from the spec's "Forma
// svakog testa". CompensateOnTransient is set so any forward error
// rolls back to a Compensated terminal, matching the real exercise saga.

type exState struct {
	Qty    int     `json:"qty"`
	Strike float64 `json:"strike"`
}

type exWorld struct {
	buyerAvail, buyerReserved   float64
	sellerAvail, sellerReserved float64

	sellerSharesAvail, sellerSharesReserved int
	buyerShares                             int

	contractValid bool
	txns          int // rows in bank.transactions (F3 + each C3)
}

func newExWorld(buyerUSD float64, sellerShares int) *exWorld {
	return &exWorld{
		buyerAvail:        buyerUSD,
		sellerSharesAvail: sellerShares,
		contractValid:     true,
	}
}

// usdTotal is SUM(available + reserved) over both parties' USD — the
// I1 quantity (must be conserved across same-currency transfers).
func (w *exWorld) usdTotal() float64 {
	return w.buyerAvail + w.buyerReserved + w.sellerAvail + w.sellerReserved
}

// sharesTotal is SUM(quantity + reserved) over the symbol — the I2
// quantity (must be conserved).
func (w *exWorld) sharesTotal() int {
	return w.sellerSharesAvail + w.sellerSharesReserved + w.buyerShares
}

// allReservedZero is I3 — no reservation is left stranded.
func (w *exWorld) allReservedZero() bool {
	return w.buyerReserved == 0 && w.sellerReserved == 0 && w.sellerSharesReserved == 0
}

func buildExerciseSaga(w *exWorld) Definition[exState] {
	total := func(s *exState) float64 { return float64(s.Qty) * s.Strike }
	return Definition[exState]{
		Type:                  "ex",
		CompensateOnTransient: true,
		Steps: []Step[exState]{
			{
				Name: "reserve_buyer_strike", // F1 / C1
				Forward: func(_ context.Context, sc *Context[exState]) error {
					t := total(sc.State)
					if w.buyerAvail < t {
						return status.Error(codes.FailedPrecondition, "nedovoljno sredstava na računu")
					}
					w.buyerAvail -= t
					w.buyerReserved += t
					return nil
				},
				Compensate: func(_ context.Context, sc *Context[exState]) error {
					// Release whatever of this reservation is still held.
					// Once F3 (transfer_strike) consumes it, there's
					// nothing left to release and C3's reverse transfer is
					// what returns the money — releasing a fixed amount
					// here would double-credit and break I1.
					w.buyerAvail += w.buyerReserved
					w.buyerReserved = 0
					return nil
				},
			},
			{
				Name: "reserve_seller_shares", // F2 / C2
				Forward: func(_ context.Context, sc *Context[exState]) error {
					if w.sellerSharesAvail < sc.State.Qty {
						return status.Error(codes.FailedPrecondition, "prodavac nema dovoljno hartija")
					}
					w.sellerSharesAvail -= sc.State.Qty
					w.sellerSharesReserved += sc.State.Qty
					return nil
				},
				Compensate: func(_ context.Context, sc *Context[exState]) error {
					// Release the share reservation still held (symmetric
					// to C1: after F4 hands the shares to the buyer there's
					// nothing reserved, and C4 is what restores them).
					w.sellerSharesAvail += w.sellerSharesReserved
					w.sellerSharesReserved = 0
					return nil
				},
			},
			{
				Name: "transfer_strike", // F3 / C3
				Forward: func(_ context.Context, sc *Context[exState]) error {
					t := total(sc.State)
					w.buyerReserved -= t
					w.sellerAvail += t
					w.txns++
					return nil
				},
				Compensate: func(_ context.Context, sc *Context[exState]) error {
					t := total(sc.State)
					w.sellerAvail -= t
					w.buyerAvail += t
					w.txns++ // compensation writes its own ledger row
					return nil
				},
			},
			{
				Name: "transfer_shares", // F4 / C4
				Forward: func(_ context.Context, sc *Context[exState]) error {
					w.sellerSharesReserved -= sc.State.Qty
					w.buyerShares += sc.State.Qty
					return nil
				},
				Compensate: func(_ context.Context, sc *Context[exState]) error {
					w.buyerShares -= sc.State.Qty
					w.sellerSharesReserved += sc.State.Qty
					return nil
				},
			},
			{
				Name: "finalize", // F5 / C5
				Forward: func(_ context.Context, sc *Context[exState]) error {
					w.contractValid = false
					return nil
				},
				Compensate: func(_ context.Context, sc *Context[exState]) error {
					w.contractValid = true
					return nil
				},
			},
		},
	}
}

// exFixture wires a fresh world + orchestrator + saga for one scenario.
type exFixture struct {
	w     *exWorld
	o     *Orchestrator
	store *memStore
	txID  string
	state exState
}

func newExFixture(t *testing.T, buyerUSD float64, sellerShares int, qty int, strike float64) *exFixture {
	t.Helper()
	w := newExWorld(buyerUSD, sellerShares)
	reg := NewRegistry()
	Register[exState](reg, buildExerciseSaga(w))
	o := New(newMemStoreFrom(reg), reg, quietLogger())
	o.MaxBackoff = 0
	return &exFixture{
		w:     w,
		o:     o,
		store: o.Store.(*memStore),
		txID:  fmt.Sprintf("sg-%d", time.Now().UnixNano()),
		state: exState{Qty: qty, Strike: strike},
	}
}

// newMemStoreFrom is newMemStore — the registry arg keeps call sites
// readable about which saga the store backs.
func newMemStoreFrom(_ *Registry) *memStore { return newMemStore() }

func (f *exFixture) start(ctx context.Context) *Row {
	row, _ := Start(ctx, f.o, StartInput[exState]{
		TransactionID: f.txID,
		SagaType:      "ex",
		InitialState:  f.state,
		AttemptsMax:   8,
	})
	return row
}

// driveToTerminal resumes a parked saga (status running/compensating)
// until it reaches a terminal status, simulating the recovery worker.
// ctx carries any fault directive so Times-budgeted comp faults are
// honoured across resumes.
func (f *exFixture) driveToTerminal(t *testing.T, ctx context.Context) *Row {
	t.Helper()
	for i := 0; i < 30; i++ {
		row, _ := f.store.Get(context.Background(), f.txID)
		if row == nil {
			t.Fatalf("saga row vanished")
		}
		if row.Status == StatusCompleted || row.Status == StatusCompensated || row.Status == StatusFailed {
			return row
		}
		row.NextAttemptAt = time.Now().Add(-time.Second)
		_ = f.store.Update(context.Background(), row)
		if err := f.o.Resume(ctx, f.txID); err != nil && i > 25 {
			t.Fatalf("Resume: %v", err)
		}
	}
	t.Fatalf("saga did not reach terminal status within budget")
	return nil
}

// assertLog checks the persisted log matches an expected "F1:ok F3:err
// C2:ok"-style sequence.
func assertLog(t *testing.T, row *Row, want ...string) {
	t.Helper()
	got := make([]string, len(row.Log))
	for i, e := range row.Log {
		got[i] = e.Step + ":" + e.Result
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("log = %v\n          want %v", got, want)
	}
}

func snapshot(w *exWorld) exWorld { return *w }

// assertBalancesRestored compares everything except the append-only txn
// counter (compensation legitimately appends a reversing ledger row).
func assertBalancesRestored(t *testing.T, got, want exWorld) {
	t.Helper()
	g, wnt := got, want
	g.txns, wnt.txns = 0, 0
	if g != wnt {
		t.Errorf("state not restored:\n got  %+v\n want %+v", got, want)
	}
}

// ---------------------------------------------------------------------

// SG-01 happy path: all five phases commit, contract stops being
// "važeći", current_step=5, five ok log entries, exactly one ledger row.
func TestSG01_HappyPath(t *testing.T) {
	f := newExFixture(t, 5000, 10, 10, 300)
	before := snapshot(f.w)
	row := f.start(context.Background())

	if row.Status != StatusCompleted {
		t.Fatalf("status = %s, want completed", row.Status)
	}
	if row.StepNo != 5 {
		t.Errorf("current_step = %d, want 5", row.StepNo)
	}
	assertLog(t, row, "F1:ok", "F2:ok", "F3:ok", "F4:ok", "F5:ok")
	if f.w.buyerAvail != 2000 || f.w.sellerAvail != 3000 {
		t.Errorf("buyer=%.0f seller=%.0f, want 2000 / 3000", f.w.buyerAvail, f.w.sellerAvail)
	}
	if f.w.buyerShares != 10 || f.w.sellerSharesAvail != 0 {
		t.Errorf("shares buyer=%d sellerAvail=%d, want 10 / 0", f.w.buyerShares, f.w.sellerSharesAvail)
	}
	if f.w.contractValid { // I6
		t.Errorf("contract still važeći after Completed")
	}
	if f.w.txns != 1 {
		t.Errorf("bank.transactions = %d, want 1 (the F3 transfer)", f.w.txns)
	}
	// I1, I2, I3.
	if f.w.usdTotal() != before.usdTotal() {
		t.Errorf("I1 violated: USD %.0f != %.0f", f.w.usdTotal(), before.usdTotal())
	}
	if f.w.sharesTotal() != before.sharesTotal() {
		t.Errorf("I2 violated: shares %d != %d", f.w.sharesTotal(), before.sharesTotal())
	}
	if !f.w.allReservedZero() {
		t.Errorf("I3 violated: reserved columns not zero: %+v", f.w)
	}
}

// SG-03 insufficient funds: F1 fails, nothing to compensate, clean
// Compensated terminal at current_step=1 with a single F1:err entry.
func TestSG03_InsufficientFunds(t *testing.T) {
	f := newExFixture(t, 500, 10, 10, 300) // needs 3000, has 500
	before := snapshot(f.w)
	row := f.start(context.Background())

	if row.Status != StatusCompensated {
		t.Fatalf("status = %s, want compensated", row.Status)
	}
	if row.StepNo != 1 {
		t.Errorf("current_step = %d, want 1", row.StepNo)
	}
	assertLog(t, row, "F1:err")
	assertBalancesRestored(t, *f.w, before) // no side effects
	if !f.w.contractValid {                 // I6 — never finalized
		t.Errorf("contract should still be važeći")
	}
	if !f.w.allReservedZero() {
		t.Errorf("I3 violated: %+v", f.w)
	}
}

// SG-04 insufficient shares: F2 fails after F1 committed; C1 releases
// the buyer reservation. Compensated, current_step=2, log F1,F2-err,C1.
func TestSG04_InsufficientShares(t *testing.T) {
	f := newExFixture(t, 5000, 3, 10, 300) // seller has 3, needs 10
	before := snapshot(f.w)
	row := f.start(context.Background())

	if row.Status != StatusCompensated {
		t.Fatalf("status = %s, want compensated", row.Status)
	}
	if row.StepNo != 2 {
		t.Errorf("current_step = %d, want 2", row.StepNo)
	}
	assertLog(t, row, "F1:ok", "F2:err", "C1:ok")
	assertBalancesRestored(t, *f.w, before)
	if !f.w.allReservedZero() {
		t.Errorf("I3 violated: %+v", f.w)
	}
}

// SG-05 force-fail F3: compensate C2 then C1. current_step=3, state
// identical to before, all invariants hold.
func TestSG05_TransferStrikeFails(t *testing.T) {
	f := newExFixture(t, 5000, 10, 10, 300)
	before := snapshot(f.w)
	ctx := WithForceFail(context.Background(), ForceFail{Step: "F3"})
	row := f.start(ctx)

	if row.Status != StatusCompensated {
		t.Fatalf("status = %s, want compensated", row.Status)
	}
	if row.StepNo != 3 {
		t.Errorf("current_step = %d, want 3", row.StepNo)
	}
	assertLog(t, row, "F1:ok", "F2:ok", "F3:err", "C2:ok", "C1:ok")
	assertBalancesRestored(t, *f.w, before)
	if f.w.txns != 0 {
		t.Errorf("no ledger row should exist (F3 never committed); got %d", f.w.txns)
	}
	if !f.w.allReservedZero() {
		t.Errorf("I3 violated: %+v", f.w)
	}
}

// SG-06 force-fail F4: compensate C3 (reverses the committed transfer),
// C2, C1. current_step=4, balances restored, ledger has the transfer +
// its reversal.
func TestSG06_TransferSharesFails(t *testing.T) {
	f := newExFixture(t, 5000, 10, 10, 300)
	before := snapshot(f.w)
	ctx := WithForceFail(context.Background(), ForceFail{Step: "F4"})
	row := f.start(ctx)

	if row.Status != StatusCompensated {
		t.Fatalf("status = %s, want compensated", row.Status)
	}
	if row.StepNo != 4 {
		t.Errorf("current_step = %d, want 4", row.StepNo)
	}
	assertLog(t, row, "F1:ok", "F2:ok", "F3:ok", "F4:err", "C3:ok", "C2:ok", "C1:ok")
	assertBalancesRestored(t, *f.w, before)
	if f.w.txns != 2 {
		t.Errorf("ledger rows = %d, want 2 (F3 transfer + C3 reversal)", f.w.txns)
	}
	if !f.w.allReservedZero() {
		t.Errorf("I3 violated: %+v", f.w)
	}
}

// SG-07 force-fail F5: compensate C4 (shares back), C3 (funds back), C2,
// C1. current_step=5, state restored, contract stays važeći (I6).
func TestSG07_FinalizeFails(t *testing.T) {
	f := newExFixture(t, 5000, 10, 10, 300)
	before := snapshot(f.w)
	ctx := WithForceFail(context.Background(), ForceFail{Step: "F5"})
	row := f.start(ctx)

	if row.Status != StatusCompensated {
		t.Fatalf("status = %s, want compensated", row.Status)
	}
	if row.StepNo != 5 {
		t.Errorf("current_step = %d, want 5", row.StepNo)
	}
	assertLog(t, row, "F1:ok", "F2:ok", "F3:ok", "F4:ok", "F5:err", "C4:ok", "C3:ok", "C2:ok", "C1:ok")
	assertBalancesRestored(t, *f.w, before)
	if !f.w.contractValid { // I6 — never completed, so must stay važeći
		t.Errorf("contract must remain važeći after a rolled-back exercise")
	}
	if !f.w.allReservedZero() {
		t.Errorf("I3 violated: %+v", f.w)
	}
}

// SG-08 compensator fails once then succeeds: force-fail F3, fail C2
// once (Times=1). The log carries two C2 entries (err then ok); the
// saga ends Compensated with state restored.
func TestSG08_CompensatorRetries(t *testing.T) {
	f := newExFixture(t, 5000, 10, 10, 300)
	before := snapshot(f.w)
	ctx := WithForceFail(context.Background(), ForceFail{Step: "F3"})
	ctx = WithForceCompensateFail(ctx, ForceCompensateFail{Step: "C2", Times: 1})

	first := f.start(ctx)
	if first.Status != StatusCompensating {
		t.Fatalf("status after first pass = %s, want compensating (C2 parked)", first.Status)
	}
	row := f.driveToTerminal(t, ctx)

	if row.Status != StatusCompensated {
		t.Fatalf("terminal status = %s, want compensated", row.Status)
	}
	if row.StepNo != 3 {
		t.Errorf("current_step = %d, want 3", row.StepNo)
	}
	assertLog(t, row, "F1:ok", "F2:ok", "F3:err", "C2:err", "C2:ok", "C1:ok")
	assertBalancesRestored(t, *f.w, before)
	if !f.w.allReservedZero() {
		t.Errorf("I3 violated: %+v", f.w)
	}
}

// SG-09 infrastructure failure at F1 (transient): with no prior phases
// to compensate, the saga goes straight to Compensated, current_step=1,
// single F1:err entry, no side effects.
func TestSG09_InfraFailureAtF1(t *testing.T) {
	f := newExFixture(t, 5000, 10, 10, 300)
	before := snapshot(f.w)
	ctx := WithForceFail(context.Background(), ForceFail{Step: "F1", Kind: "transient"})
	row := f.start(ctx)

	if row.Status != StatusCompensated {
		t.Fatalf("status = %s, want compensated (no forward retry for the exercise saga)", row.Status)
	}
	if row.StepNo != 1 {
		t.Errorf("current_step = %d, want 1", row.StepNo)
	}
	assertLog(t, row, "F1:err")
	assertBalancesRestored(t, *f.w, before)
}

// SG-10 service unavailable mid-saga (transient F3): trading-side
// compensators (C2) and the bank-side C1 both run, saga ends
// Compensated at current_step=3. (The pause/unpause timing is an
// environmental detail; at the orchestrator level it's a transient F3.)
func TestSG10_ServiceUnavailableMidSaga(t *testing.T) {
	f := newExFixture(t, 5000, 10, 10, 300)
	before := snapshot(f.w)
	ctx := WithForceFail(context.Background(), ForceFail{Step: "F3", Kind: "transient"})
	f.start(ctx)
	row := f.driveToTerminal(t, ctx)

	if row.Status != StatusCompensated {
		t.Fatalf("status = %s, want compensated", row.Status)
	}
	if row.StepNo != 3 {
		t.Errorf("current_step = %d, want 3", row.StepNo)
	}
	assertLog(t, row, "F1:ok", "F2:ok", "F3:err", "C2:ok", "C1:ok")
	assertBalancesRestored(t, *f.w, before)
	if !f.w.allReservedZero() {
		t.Errorf("I3 violated: %+v", f.w)
	}
}

// A compensator that fails permanently (no Times budget) gives up the
// rollback and lands in the genuinely-stuck `failed` terminal — the
// pathological case invariant I5 calls out, distinct from a clean
// Compensated rollback.
func TestSG_PermanentCompensatorFailure_EndsFailed(t *testing.T) {
	f := newExFixture(t, 5000, 10, 10, 300)
	ctx := WithForceFail(context.Background(), ForceFail{Step: "F3"})
	ctx = WithForceCompensateFail(ctx, ForceCompensateFail{Step: "C2"}) // Times=0 → permanent
	row := f.start(ctx)
	if row.Status != StatusFailed {
		t.Fatalf("status = %s, want failed (stuck rollback)", row.Status)
	}
	if row.StepNo != 3 {
		t.Errorf("current_step = %d, want 3 (frozen)", row.StepNo)
	}
	assertLog(t, row, "F1:ok", "F2:ok", "F3:err", "C2:err")
}

// SG-11 coordinator killed mid-flight: a fresh orchestrator reads the
// persisted log + current_step back and continues to a valid terminal.
// Both resume directions are exercised.
func TestSG11_CoordinatorRestart(t *testing.T) {
	// (a) Forward resume — crashed after F2, recovers and completes.
	t.Run("forward-resume-completes", func(t *testing.T) {
		w := newExWorld(5000, 10)
		before := snapshot(w)
		reg := NewRegistry()
		Register[exState](reg, buildExerciseSaga(w))
		store := newMemStore()
		// Replay F1 + F2 onto the world (what the dead coordinator had
		// committed before SIGKILL) and persist the matching mid-flight
		// row: status=running, two ok log entries, current_step=2.
		w.buyerAvail, w.buyerReserved = 2000, 3000
		w.sellerSharesAvail, w.sellerSharesReserved = 0, 10
		_ = store.Insert(context.Background(), &Row{
			TransactionID: "sg11a", SagaType: "ex",
			CurrentStep: "reserve_seller_shares", StepNo: 2,
			Log:    []LogEntry{{Step: "F1", Result: "ok"}, {Step: "F2", Result: "ok"}},
			State:  mustJSON(exState{Qty: 10, Strike: 300}),
			Status: StatusRunning, AttemptsMax: 8, NextAttemptAt: time.Now().Add(-time.Second),
		})
		o := New(store, reg, quietLogger())

		if err := o.Resume(context.Background(), "sg11a"); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		row, _ := store.Get(context.Background(), "sg11a")
		if row.Status != StatusCompleted { // I5
			t.Fatalf("status = %s, want completed", row.Status)
		}
		assertLog(t, row, "F1:ok", "F2:ok", "F3:ok", "F4:ok", "F5:ok")
		if w.buyerShares != 10 || w.contractValid {
			t.Errorf("post-resume world wrong: %+v", w)
		}
		if w.usdTotal() != before.usdTotal() || w.sharesTotal() != before.sharesTotal() || !w.allReservedZero() {
			t.Errorf("invariants violated after resume: %+v", w)
		}
	})

	// (b) Compensating resume — crashed mid-rollback, recovers to
	// Compensated with no dangling reservations.
	t.Run("compensating-resume-rolls-back", func(t *testing.T) {
		w := newExWorld(5000, 10)
		before := snapshot(w)
		reg := NewRegistry()
		Register[exState](reg, buildExerciseSaga(w))
		store := newMemStore()
		// F1 committed (buyer reserved), F2 failed → compensating, C1
		// pending. current_step frozen at 2.
		w.buyerAvail, w.buyerReserved = 2000, 3000
		_ = store.Insert(context.Background(), &Row{
			TransactionID: "sg11b", SagaType: "ex",
			CurrentStep: "reserve_buyer_strike", StepNo: 2,
			Log:    []LogEntry{{Step: "F1", Result: "ok"}, {Step: "F2", Result: "err"}},
			State:  mustJSON(exState{Qty: 10, Strike: 300}),
			Status: StatusCompensating, AttemptsMax: 8, NextAttemptAt: time.Now().Add(-time.Second),
		})
		o := New(store, reg, quietLogger())

		if err := o.Resume(context.Background(), "sg11b"); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		row, _ := store.Get(context.Background(), "sg11b")
		if row.Status != StatusCompensated { // I5
			t.Fatalf("status = %s, want compensated", row.Status)
		}
		if row.StepNo != 2 {
			t.Errorf("current_step = %d, want 2 (frozen)", row.StepNo)
		}
		assertLog(t, row, "F1:ok", "F2:err", "C1:ok")
		assertBalancesRestored(t, *w, before)
		if !w.allReservedZero() {
			t.Errorf("dangling reservation after resume: %+v", w)
		}
	})
}
