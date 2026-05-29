//go:build integration

// Inbound-flow integration tests for celina 5 (BE-7b). Exercise the
// path a partner takes when they accept / exercise a thread we host
// the seller side of. No outbound partner calls fire — those flows
// land in BE-8a once a mock-partner container is part of the
// test-integration stack.

package service

import (
	"context"
	"testing"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
)

// adminInboundCtx mirrors the gateway's admin sentinel — the
// partner-facing REST handler stamps an admin principal on every
// inbound translation, so Receive* sees an admin caller.
func adminInboundCtx() context.Context {
	return auth.WithPrincipal(context.Background(), auth.Principal{
		UserID:      "00000000-0000-0000-0000-00000000fffc",
		UserKind:    auth.KindEmployee,
		Permissions: []string{permissions.Admin},
	})
}

// TestIntegration_ExternalOTC_InboundAcceptThenExercise walks the
// partner-driven flow against a seeded local seller-holding:
//
//   1. Partner sends an offer (Receive*Offer) — thread minted.
//   2. Partner accepts (Receive*Accept) — thread → accepted +
//      external_otc_contracts row appears with premium_op_id NULL.
//   3. Replay of accept is a no-op (idempotent).
//   4. Partner sends an exercise notice (Receive*ExerciseNotice) —
//      contract → exercised, exercise_op_id stamped from derived
//      partner identifier.
//   5. Replay of exercise is a no-op.
func TestIntegration_ExternalOTC_InboundAcceptThenExercise(t *testing.T) {
	svc := setup(t)
	ctx := adminInboundCtx()

	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	stock, _ := seedStock(t, svc, "AAPL", ex, "200.0000", "200.0500", "199.9500", 1_000_000)

	clientID := "11111111-2222-4222-8222-000000000001"
	accountID := "22222222-3333-4333-8333-000000000002"
	holding := seedHolding(t, svc, clientID, domain.KindClient, stock.ID, accountID, 50, "180")
	// Make a portion publicly available so the OTC discovery
	// invariants aren't insulted; not strictly required for these
	// inbound paths.
	if _, err := svc.Store.SetPublicCount(ctx, holding.ID, 10); err != nil {
		t.Fatalf("SetPublicCount: %v", err)
	}

	settlement := time.Date(2027, 6, 1, 0, 0, 0, 0, time.UTC)

	// 1. Inbound offer.
	thread, err := svc.ReceiveExternalOTCOffer(ctx, ReceiveExternalOTCOfferInput{
		SenderBankCode:    "999",
		SenderUserRef:     "mock-buyer@partner.local",
		SenderDisplayName: "Mock Buyer",
		SenderThreadID:    "p-thr-1",
		SellerHoldingRef:  holding.ID,
		Quantity:          3,
		PricePerUnit:      "150",
		Premium:           "8",
		SettlementDate:    settlement,
	})
	if err != nil {
		t.Fatalf("ReceiveExternalOTCOffer: %v", err)
	}
	if thread.Direction != domain.ExternalOTCIncoming {
		t.Fatalf("want direction=incoming, got %q", thread.Direction)
	}
	if thread.Status != domain.ExternalOTCThreadOpen {
		t.Fatalf("want status=open after offer, got %q", thread.Status)
	}
	if thread.RemoteThreadID != "p-thr-1" {
		t.Fatalf("want remote_thread_id=p-thr-1, got %q", thread.RemoteThreadID)
	}

	// 2. Inbound accept — should mint the contract.
	accepted, err := svc.ReceiveExternalOTCAccept(ctx, ReceiveExternalOTCAction{
		SenderBankCode: "999",
		SenderThreadID: "p-thr-1",
	})
	if err != nil {
		t.Fatalf("ReceiveExternalOTCAccept: %v", err)
	}
	if accepted.Status != domain.ExternalOTCThreadAccepted {
		t.Fatalf("want status=accepted, got %q", accepted.Status)
	}
	contract, err := svc.Store.GetExternalOTCContractByThread(ctx, accepted.ID)
	if err != nil {
		t.Fatalf("GetExternalOTCContractByThread after accept: %v", err)
	}
	if contract.Status != domain.ExternalOTCContractActive {
		t.Fatalf("want contract.status=active, got %q", contract.Status)
	}
	if contract.Direction != domain.ExternalOTCIncoming {
		t.Fatalf("want direction=incoming on contract, got %q", contract.Direction)
	}
	if contract.PremiumOpID != "" {
		t.Fatalf("expected premium_op_id NULL on inbound contract, got %q", contract.PremiumOpID)
	}
	if !numericEq(contract.StrikePrice, "150") || !numericEq(contract.PremiumPaid, "8") {
		t.Fatalf("contract terms drifted: strike=%s premium=%s", contract.StrikePrice, contract.PremiumPaid)
	}

	// 3. Idempotent replay of accept.
	again, err := svc.ReceiveExternalOTCAccept(ctx, ReceiveExternalOTCAction{
		SenderBankCode: "999",
		SenderThreadID: "p-thr-1",
	})
	if err != nil {
		t.Fatalf("replay accept: %v", err)
	}
	if again.ID != accepted.ID {
		t.Fatalf("replay returned a different thread (id %q vs %q)", again.ID, accepted.ID)
	}
	contractsAfter, err := svc.Store.ListExternalOTCContracts(ctx, clientID, "")
	if err != nil {
		t.Fatalf("ListExternalOTCContracts: %v", err)
	}
	if len(contractsAfter) != 1 {
		t.Fatalf("expected exactly 1 contract after replay, got %d", len(contractsAfter))
	}

	// 4. Inbound exercise notice.
	exercised, err := svc.ReceiveExternalOTCExerciseNotice(ctx, ReceiveExternalOTCExerciseNoticeInput{
		SenderBankCode:   "999",
		SenderContractID: "p-thr-1",
		ExerciseOpID:     "strike-1",
	})
	if err != nil {
		t.Fatalf("ReceiveExternalOTCExerciseNotice: %v", err)
	}
	if exercised.Status != domain.ExternalOTCContractExercised {
		t.Fatalf("want contract.status=exercised, got %q", exercised.Status)
	}
	expectedOpID := deriveExternalExerciseOpID("999", "strike-1")
	if exercised.ExerciseOpID != expectedOpID {
		t.Fatalf("derived exercise_op_id mismatch: got %q want %q", exercised.ExerciseOpID, expectedOpID)
	}
	if exercised.ExercisedAt == nil {
		t.Fatalf("want exercised_at stamped")
	}

	// 5. Idempotent replay of exercise.
	rep, err := svc.ReceiveExternalOTCExerciseNotice(ctx, ReceiveExternalOTCExerciseNoticeInput{
		SenderBankCode:   "999",
		SenderContractID: "p-thr-1",
		ExerciseOpID:     "strike-1",
	})
	if err != nil {
		t.Fatalf("replay exercise: %v", err)
	}
	if rep.ExerciseOpID != expectedOpID {
		t.Fatalf("replay exercise_op_id drift: got %q want %q", rep.ExerciseOpID, expectedOpID)
	}
}

// TestIntegration_ExternalOTC_AcceptRequiresIncomingDirection ensures
// the partner can't accept a thread we initiated (outbound) on the
// inbound REST surface — the wrong receiver gets a FailedPrecondition.
func TestIntegration_ExternalOTC_AcceptRequiresIncomingDirection(t *testing.T) {
	svc := setup(t)
	ctx := adminInboundCtx()

	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	stock, _ := seedStock(t, svc, "AAPL", ex, "200.0000", "200.0500", "199.9500", 1_000_000)
	clientID := "33333333-4444-4444-8444-000000000003"
	accountID := "44444444-5555-4555-8555-000000000004"
	seedHolding(t, svc, clientID, domain.KindClient, stock.ID, accountID, 10, "180")

	// Plant an outgoing thread directly (skipping the saga). Then have
	// the partner attempt to "accept" it via the inbound surface.
	thread, err := svc.Store.InsertExternalOTCThread(ctx, nil, &domain.ExternalOTCThread{
		Direction:          domain.ExternalOTCOutgoing,
		RemoteBankCode:     "999",
		RemoteThreadID:     "p-thr-2",
		RemoteUserRef:      "mock-seller",
		LocalUserID:        clientID,
		LocalUserKind:      domain.KindClient,
		LocalAccountID:     accountID,
		LocalAccountNumber: "555555555555555555",
		LocalRole:          domain.ExternalOTCRoleBuyer,
		SecurityTicker:     "AAPL",
		Quantity:           1,
		PricePerUnit:       "100",
		Premium:            "5",
		Currency:           domain.CurrencyUSD,
		SettlementDate:     time.Date(2027, 7, 1, 0, 0, 0, 0, time.UTC),
		ModifiedBySide:     domain.ExternalOTCSideLocal,
		Status:             domain.ExternalOTCThreadOpen,
	})
	if err != nil {
		t.Fatalf("plant outgoing thread: %v", err)
	}
	if thread.Direction != domain.ExternalOTCOutgoing {
		t.Fatalf("want outgoing thread, got %q", thread.Direction)
	}

	_, err = svc.ReceiveExternalOTCAccept(ctx, ReceiveExternalOTCAction{
		SenderBankCode: "999",
		SenderThreadID: "p-thr-2",
	})
	if !isApperr(err, apperr.KindFailedPrecondition) {
		t.Fatalf("expected FailedPrecondition on inbound accept of outgoing thread, got %v", err)
	}
}
