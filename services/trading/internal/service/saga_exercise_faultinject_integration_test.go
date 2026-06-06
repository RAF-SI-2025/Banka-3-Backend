//go:build integration

// Force-fault integration tests for the REAL otc_exercise SAGA against a
// real Postgres + the in-process bank-reservation stub. These complement
// the orchestrator-level synthetic SG-01..SG-11 suite in
// services/trading/internal/saga/saga_sg_test.go by proving that the
// actual five-step exercise definition, driven through ExerciseOTCContract,
// writes the SAGA_test.pdf observability model to trading.saga_executions
// (status / step_no / log) and restores all balances + holdings on
// rollback.
//
// Faults are injected by attaching a saga.WithForceFail /
// WithForceCompensateFail directive to the call context. The handler runs
// ctx through saga.FaultsFromMetadata(ctx, Cfg.SagaDebugFaultInjection) —
// with the flag off and no gRPC metadata present that's a no-op, so the
// ctx-borne directive survives into the orchestrator.

package service

import (
	"context"
	"testing"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/saga"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/store"
	"github.com/google/uuid"
)

// exerciseFixture is the post-accept state shared by the fault tests: an
// active contract for 4 AAPL @ strike 155, premium 10, on a buyer with a
// 100000 USD account.
type exerciseFixture struct {
	contractID string
	buyerID    string
	buyerAcc   string
	sellerID   string
	sellerAcc  string
	holdingID  string
}

func mintActiveExerciseContract(t *testing.T, svc *Service) exerciseFixture {
	t.Helper()
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	sec, _ := seedStock(t, svc, "AAPL", ex, "150", "150", "149", 1000)

	sellerID := uuid.NewString()
	sellerAcc := uuid.NewString()
	publishHolding(t, svc, sellerID, domain.KindClient, sec.ID, sellerAcc, 10, "100")
	buyerID := uuid.NewString()
	buyerAcc := uuid.NewString()
	currentReservations.setBalance(buyerAcc, "100000")

	h := findHolding(t, svc, sellerID)
	offer, err := svc.CreateOTCOffer(clientOTCCtx(buyerID), CreateOTCOfferInput{
		SellerHoldingID: h.ID,
		BuyerAccountID:  buyerAcc,
		SellerAccountID: sellerAcc,
		Quantity:        4,
		PricePerUnit:    "155",
		Premium:         "10",
		SettlementDate:  time.Now().AddDate(0, 1, 0),
	})
	if err != nil {
		t.Fatalf("create offer: %v", err)
	}
	accept, err := svc.AcceptOTCOffer(clientOTCCtx(sellerID), AcceptOTCOfferInput{ThreadID: offer.ThreadID})
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	return exerciseFixture{
		contractID: accept.Contract.ID,
		buyerID:    buyerID,
		buyerAcc:   buyerAcc,
		sellerID:   sellerID,
		sellerAcc:  sellerAcc,
		holdingID:  h.ID,
	}
}

// sagaRow loads the persisted exercise saga row for a contract.
func sagaRow(t *testing.T, svc *Service, contractID string) *saga.Row {
	t.Helper()
	row, err := svc.SagaStore.Get(context.Background(), otcExerciseTxID(contractID))
	if err != nil {
		t.Fatalf("SagaStore.Get: %v", err)
	}
	return row
}

// logCodes renders a saga row's log as ["F1:ok","F3:err",…] for comparison.
func logCodes(row *saga.Row) []string {
	out := make([]string, len(row.Log))
	for i, e := range row.Log {
		out[i] = e.Step + ":" + e.Result
	}
	return out
}

func eqStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestIntegration_OTC_Exercise_ForceFail_TransferStrike_Compensated drives
// the real saga to a transfer_strike (F3) failure: F3 never commits, so the
// only compensation is the no-op C2 plus the buyer-reservation release
// (C1). The DB row must read status=compensated, step_no=3, log
// F1,F2 ok / F3 err / C2,C1 ok, and every balance/holding must be exactly
// as it was after accept (premium gone, strike reservation released, shares
// untouched, contract still active).
func TestIntegration_OTC_Exercise_ForceFail_TransferStrike_Compensated(t *testing.T) {
	svc := setup(t)
	f := mintActiveExerciseContract(t, svc)

	ctx := saga.WithForceFail(clientOTCCtx(f.buyerID), saga.ForceFail{Step: "transfer_strike"})
	if _, err := svc.ExerciseOTCContract(ctx, ExerciseOTCContractInput{ContractID: f.contractID}); err == nil {
		t.Fatal("exercise: expected a rollback error, got nil")
	}

	row := sagaRow(t, svc, f.contractID)
	if row == nil {
		t.Fatal("saga row missing")
	}
	if row.Status != saga.StatusCompensated {
		t.Errorf("status=%s want compensated", row.Status)
	}
	if row.StepNo != 3 {
		t.Errorf("step_no=%d want 3", row.StepNo)
	}
	if want := []string{"F1:ok", "F2:ok", "F3:err", "C2:ok", "C1:ok"}; !eqStrs(logCodes(row), want) {
		t.Errorf("log=%v want %v", logCodes(row), want)
	}

	// Balances: only the premium (10) left the buyer at accept; the strike
	// reservation was released, never committed.
	if got := currentReservations.balance(f.buyerAcc); got != "99990.0000" {
		t.Errorf("buyer balance=%s want 99990.0000 (only premium gone)", got)
	}
	if got := currentReservations.balance(f.sellerAcc); got != "10.0000" {
		t.Errorf("seller balance=%s want 10.0000 (premium only)", got)
	}
	// Seller holding intact: 10 qty, 4 still reserved for the live contract.
	hs, err := svc.Store.GetHoldingByID(context.Background(), f.holdingID)
	if err != nil {
		t.Fatalf("GetHoldingByID: %v", err)
	}
	if hs.Quantity != 10 || hs.ReservedCount != 4 {
		t.Errorf("seller holding qty=%d reserved=%d want 10 / 4", hs.Quantity, hs.ReservedCount)
	}
	// No realized gain, contract still active (I6).
	assertNoRealizedGains(t, svc, f.sellerID)
	assertContractActive(t, svc, f.contractID)
}

// TestIntegration_OTC_Exercise_ForceFail_Finalize_Compensated drives the
// deepest compensation chain: finalize (F5) fails after F1-F4 committed, so
// the rollback must reverse share ownership (C4), the cash transfer (C3),
// the share reservation (C2 no-op) and the buyer reservation (C1).
// status=compensated, step_no=5, full state restoration.
func TestIntegration_OTC_Exercise_ForceFail_Finalize_Compensated(t *testing.T) {
	svc := setup(t)
	f := mintActiveExerciseContract(t, svc)

	ctx := saga.WithForceFail(clientOTCCtx(f.buyerID), saga.ForceFail{Step: "finalize"})
	if _, err := svc.ExerciseOTCContract(ctx, ExerciseOTCContractInput{ContractID: f.contractID}); err == nil {
		t.Fatal("exercise: expected a rollback error, got nil")
	}

	row := sagaRow(t, svc, f.contractID)
	if row.Status != saga.StatusCompensated {
		t.Errorf("status=%s want compensated", row.Status)
	}
	if row.StepNo != 5 {
		t.Errorf("step_no=%d want 5", row.StepNo)
	}
	if want := []string{"F1:ok", "F2:ok", "F3:ok", "F4:ok", "F5:err", "C4:ok", "C3:ok", "C2:ok", "C1:ok"}; !eqStrs(logCodes(row), want) {
		t.Errorf("log=%v want %v", logCodes(row), want)
	}

	// Cash fully restored: buyer back to opening-minus-premium, seller to
	// premium only (C3 reversed the 620 strike transfer).
	if got := currentReservations.balance(f.buyerAcc); got != "99990.0000" {
		t.Errorf("buyer balance=%s want 99990.0000", got)
	}
	if got := currentReservations.balance(f.sellerAcc); got != "10.0000" {
		t.Errorf("seller balance=%s want 10.0000", got)
	}
	// Shares back with the seller (C4 reversed transfer_shares).
	hs, err := svc.Store.GetHoldingByID(context.Background(), f.holdingID)
	if err != nil {
		t.Fatalf("GetHoldingByID: %v", err)
	}
	if hs.Quantity != 10 || hs.ReservedCount != 4 {
		t.Errorf("seller holding qty=%d reserved=%d want 10 / 4", hs.Quantity, hs.ReservedCount)
	}
	// Buyer never kept the shares.
	if bh := tryFindHolding(t, svc, f.buyerID); bh != nil && bh.Quantity != 0 {
		t.Errorf("buyer holding qty=%d want 0 after rollback", bh.Quantity)
	}
	// Realized-gain row deleted by C4, contract still active (I6).
	assertNoRealizedGains(t, svc, f.sellerID)
	assertContractActive(t, svc, f.contractID)
}

// TestIntegration_OTC_Exercise_PermanentCompensatorFailure_Failed proves
// the genuinely-stuck terminal: a forward fault triggers rollback, but a
// permanent compensator failure (no Times budget) on C1 leaves the saga in
// status=failed with last_error set — the I5 pathological case, distinct
// from a clean compensated rollback.
func TestIntegration_OTC_Exercise_PermanentCompensatorFailure_Failed(t *testing.T) {
	svc := setup(t)
	f := mintActiveExerciseContract(t, svc)

	ctx := saga.WithForceFail(clientOTCCtx(f.buyerID), saga.ForceFail{Step: "transfer_strike"})
	ctx = saga.WithForceCompensateFail(ctx, saga.ForceCompensateFail{Step: "reserve_buyer_strike"}) // C1, Times=0 → permanent
	if _, err := svc.ExerciseOTCContract(ctx, ExerciseOTCContractInput{ContractID: f.contractID}); err == nil {
		t.Fatal("exercise: expected a failure error, got nil")
	}

	row := sagaRow(t, svc, f.contractID)
	if row.Status != saga.StatusFailed {
		t.Errorf("status=%s want failed (stuck rollback)", row.Status)
	}
	if row.StepNo != 3 {
		t.Errorf("step_no=%d want 3", row.StepNo)
	}
	if want := []string{"F1:ok", "F2:ok", "F3:err", "C2:ok", "C1:err"}; !eqStrs(logCodes(row), want) {
		t.Errorf("log=%v want %v", logCodes(row), want)
	}
	if row.LastError == "" {
		t.Error("last_error should be set on a stuck (failed) saga")
	}
	// Contract must NOT be exercised (rollback never finished, but the
	// contract was never finalized either).
	assertContractActive(t, svc, f.contractID)
}

// TestIntegration_OTC_Exercise_PreSaga_Rejections covers SG-02: pre-saga
// validation failures return an HTTP error and create NO saga row.
func TestIntegration_OTC_Exercise_PreSaga_Rejections(t *testing.T) {
	t.Run("not_the_buyer", func(t *testing.T) {
		svc := setup(t)
		f := mintActiveExerciseContract(t, svc)
		// The seller (not the buyer, not admin) cannot exercise.
		_, err := svc.ExerciseOTCContract(clientOTCCtx(f.sellerID), ExerciseOTCContractInput{ContractID: f.contractID})
		if err == nil {
			t.Fatal("expected permission rejection")
		}
		if row := sagaRow(t, svc, f.contractID); row != nil {
			t.Errorf("a saga row was created for a pre-saga rejection: %+v", row)
		}
		assertContractActive(t, svc, f.contractID)
	})

	t.Run("contract_not_found", func(t *testing.T) {
		svc := setup(t)
		buyer := uuid.NewString()
		missing := uuid.NewString()
		if _, err := svc.ExerciseOTCContract(clientOTCCtx(buyer), ExerciseOTCContractInput{ContractID: missing}); err == nil {
			t.Fatal("expected not-found rejection")
		}
		if row := sagaRow(t, svc, missing); row != nil {
			t.Errorf("a saga row was created for a missing contract: %+v", row)
		}
	})

	t.Run("already_exercised_no_double_debit", func(t *testing.T) {
		svc := setup(t)
		f := mintActiveExerciseContract(t, svc)
		if _, err := svc.ExerciseOTCContract(clientOTCCtx(f.buyerID), ExerciseOTCContractInput{ContractID: f.contractID}); err != nil {
			t.Fatalf("first exercise: %v", err)
		}
		afterFirst := currentReservations.balance(f.buyerAcc)
		row := sagaRow(t, svc, f.contractID)
		if row == nil || row.Status != saga.StatusCompleted {
			t.Fatalf("first exercise should leave a completed saga, got %+v", row)
		}
		// Second exercise is rejected pre-saga (contract no longer active);
		// the existing saga row is untouched and no extra debit occurs.
		if _, err := svc.ExerciseOTCContract(clientOTCCtx(f.buyerID), ExerciseOTCContractInput{ContractID: f.contractID}); err == nil {
			t.Fatal("expected rejection on the second exercise")
		}
		if got := currentReservations.balance(f.buyerAcc); got != afterFirst {
			t.Errorf("buyer balance moved on rejected re-exercise: %s -> %s", afterFirst, got)
		}
		if row2 := sagaRow(t, svc, f.contractID); row2 == nil || row2.Status != saga.StatusCompleted {
			t.Errorf("saga row should stay completed, got %+v", row2)
		}
	})
}

// ---- small assertion helpers -----------------------------------------

func assertNoRealizedGains(t *testing.T, svc *Service, userID string) {
	t.Helper()
	gains, err := svc.Store.ListRealizedGains(context.Background(), store.RealizedGainFilter{UserID: userID})
	if err != nil {
		t.Fatalf("ListRealizedGains: %v", err)
	}
	if len(gains) != 0 {
		t.Errorf("expected no realized_gains after rollback, got %d", len(gains))
	}
}

func assertContractActive(t *testing.T, svc *Service, contractID string) {
	t.Helper()
	c, err := svc.Store.GetOTCContract(context.Background(), contractID)
	if err != nil {
		t.Fatalf("GetOTCContract: %v", err)
	}
	if c.Status != domain.OTCContractActive {
		t.Errorf("contract status=%s want active (rolled-back exercise must not finalize)", c.Status)
	}
}

// tryFindHolding returns the buyer's holding for the seeded AAPL security
// or nil when none exists (the buyer may never have received shares).
func tryFindHolding(t *testing.T, svc *Service, userID string) *domain.Holding {
	t.Helper()
	hs, err := svc.Store.ListHoldings(context.Background(), store.HoldingFilter{UserID: userID})
	if err != nil {
		t.Fatalf("ListHoldings: %v", err)
	}
	if len(hs) == 0 {
		return nil
	}
	return hs[0]
}
