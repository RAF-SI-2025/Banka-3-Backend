//go:build integration

// OTC c4-PR2 integration tests against a real Postgres + in-process
// bank-reservation stub.

package service

import (
	"context"
	"testing"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/store"
	"github.com/google/uuid"
)

func clientOTCCtx(id string) context.Context {
	return auth.WithPrincipal(context.Background(), auth.Principal{
		UserID:      id,
		UserKind:    auth.KindClient,
		Permissions: []string{permissions.TradingClient, permissions.OTCRead, permissions.OTCTradeClient},
	})
}

func supervisorOTCCtx(id string) context.Context {
	return auth.WithPrincipal(context.Background(), auth.Principal{
		UserID:      id,
		UserKind:    auth.KindEmployee,
		Permissions: []string{permissions.Actuary, permissions.ActuarySupervisor, permissions.OTCRead, permissions.OTCTradeSupervisor},
	})
}

// publishHolding seeds a holding with public_count=qty so the row
// shows up on discovery.
func publishHolding(t *testing.T, svc *Service, userID string, kind domain.UserKind, secID, accID string, qty int32, avg string) *domain.Holding {
	t.Helper()
	h := seedHolding(t, svc, userID, kind, secID, accID, qty, avg)
	updated, err := svc.Store.SetPublicCount(context.Background(), h.ID, qty)
	if err != nil {
		t.Fatalf("SetPublicCount: %v", err)
	}
	return updated
}

func TestIntegration_OTC_CreateOffer_BumpsReservation(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	sec, _ := seedStock(t, svc, "AAPL", ex, "150", "150", "149", 1000)

	sellerID := uuid.NewString()
	sellerAcc := uuid.NewString()
	publishHolding(t, svc, sellerID, domain.KindClient, sec.ID, sellerAcc, 12, "100")

	buyerID := uuid.NewString()
	buyerAcc := uuid.NewString()

	h := findHolding(t, svc, sellerID)
	out, err := svc.CreateOTCOffer(clientOTCCtx(buyerID), CreateOTCOfferInput{
		SellerHoldingID: h.ID,
		BuyerAccountID:  buyerAcc,
		SellerAccountID: sellerAcc,
		Quantity:        5,
		PricePerUnit:    "155",
		Premium:         "10",
		SettlementDate:  time.Now().AddDate(0, 1, 0),
	})
	if err != nil {
		t.Fatalf("CreateOTCOffer: %v", err)
	}
	if out.Status != domain.OTCStatusOpen {
		t.Fatalf("status=%s want open", out.Status)
	}
	if out.ThreadID != out.ID {
		t.Fatalf("first iteration: thread_id (%s) must equal id (%s)", out.ThreadID, out.ID)
	}
	// Reservation bumped.
	post, err := svc.Store.GetHoldingByID(context.Background(), h.ID)
	if err != nil {
		t.Fatalf("GetHoldingByID: %v", err)
	}
	if post.ReservedCount != 5 {
		t.Fatalf("reserved_count=%d want 5", post.ReservedCount)
	}
}

// EDGE-8: spec p.79 — clients ↔ clients OR supervisors ↔ supervisors.
// Cross-kind offers fail.
func TestIntegration_OTC_CreateOffer_RejectsMixedKind(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	sec, _ := seedStock(t, svc, "AAPL", ex, "150", "150", "149", 1000)

	// Seller is a client; buyer attempts to act as supervisor.
	sellerID := uuid.NewString()
	sellerAcc := uuid.NewString()
	publishHolding(t, svc, sellerID, domain.KindClient, sec.ID, sellerAcc, 5, "100")

	supervisor := uuid.NewString()
	h := findHolding(t, svc, sellerID)
	_, err := svc.CreateOTCOffer(supervisorOTCCtx(supervisor), CreateOTCOfferInput{
		SellerHoldingID: h.ID,
		BuyerAccountID:  uuid.NewString(),
		SellerAccountID: sellerAcc,
		Quantity:        2,
		PricePerUnit:    "160",
		Premium:         "5",
		SettlementDate:  time.Now().AddDate(0, 1, 0),
	})
	if err == nil {
		t.Fatal("CreateOTCOffer mixed-kind: want failure, got nil")
	}
}

// EDGE-1: reservation overlap rule (spec p.68) — `Σ open offer qty ≤
// holding.quantity`. The CHECK constraint catches us via the
// reserved_count helper.
func TestIntegration_OTC_CreateOffer_OverlapRule(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	sec, _ := seedStock(t, svc, "AAPL", ex, "150", "150", "149", 1000)

	sellerID := uuid.NewString()
	sellerAcc := uuid.NewString()
	publishHolding(t, svc, sellerID, domain.KindClient, sec.ID, sellerAcc, 5, "100")

	h := findHolding(t, svc, sellerID)
	buyer1 := uuid.NewString()
	if _, err := svc.CreateOTCOffer(clientOTCCtx(buyer1), CreateOTCOfferInput{
		SellerHoldingID: h.ID,
		BuyerAccountID:  uuid.NewString(),
		SellerAccountID: sellerAcc,
		Quantity:        4,
		PricePerUnit:    "155",
		Premium:         "5",
		SettlementDate:  time.Now().AddDate(0, 1, 0),
	}); err != nil {
		t.Fatalf("first offer: %v", err)
	}
	buyer2 := uuid.NewString()
	_, err := svc.CreateOTCOffer(clientOTCCtx(buyer2), CreateOTCOfferInput{
		SellerHoldingID: h.ID,
		BuyerAccountID:  uuid.NewString(),
		SellerAccountID: sellerAcc,
		Quantity:        2,
		PricePerUnit:    "160",
		Premium:         "5",
		SettlementDate:  time.Now().AddDate(0, 1, 0),
	})
	if err == nil {
		t.Fatal("second offer (4+2 > 5): want overlap failure, got success")
	}
}

func TestIntegration_OTC_Withdraw_ReleasesReservation(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	sec, _ := seedStock(t, svc, "AAPL", ex, "150", "150", "149", 1000)

	sellerID := uuid.NewString()
	sellerAcc := uuid.NewString()
	publishHolding(t, svc, sellerID, domain.KindClient, sec.ID, sellerAcc, 10, "100")
	buyerID := uuid.NewString()

	h := findHolding(t, svc, sellerID)
	offer, err := svc.CreateOTCOffer(clientOTCCtx(buyerID), CreateOTCOfferInput{
		SellerHoldingID: h.ID,
		BuyerAccountID:  uuid.NewString(),
		SellerAccountID: sellerAcc,
		Quantity:        3,
		PricePerUnit:    "155",
		Premium:         "5",
		SettlementDate:  time.Now().AddDate(0, 1, 0),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.WithdrawOTCOffer(clientOTCCtx(buyerID), offer.ThreadID); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	post, err := svc.Store.GetHoldingByID(context.Background(), h.ID)
	if err != nil {
		t.Fatalf("GetHoldingByID: %v", err)
	}
	if post.ReservedCount != 0 {
		t.Fatalf("reserved_count after withdraw=%d want 0", post.ReservedCount)
	}
}

// Counter from the seller adjusts reservation upward when qty grows.
func TestIntegration_OTC_Counter_AdjustsReservation(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	sec, _ := seedStock(t, svc, "AAPL", ex, "150", "150", "149", 1000)

	sellerID := uuid.NewString()
	sellerAcc := uuid.NewString()
	publishHolding(t, svc, sellerID, domain.KindClient, sec.ID, sellerAcc, 10, "100")
	buyerID := uuid.NewString()

	h := findHolding(t, svc, sellerID)
	offer, err := svc.CreateOTCOffer(clientOTCCtx(buyerID), CreateOTCOfferInput{
		SellerHoldingID: h.ID,
		BuyerAccountID:  uuid.NewString(),
		SellerAccountID: sellerAcc,
		Quantity:        3,
		PricePerUnit:    "155",
		Premium:         "5",
		SettlementDate:  time.Now().AddDate(0, 1, 0),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Seller counters with higher qty.
	counter, err := svc.CounterOfferOTC(clientOTCCtx(sellerID), CounterOfferOTCInput{
		ThreadID:       offer.ThreadID,
		Quantity:       6,
		PricePerUnit:   "160",
		Premium:        "8",
		SettlementDate: offer.SettlementDate,
	})
	if err != nil {
		t.Fatalf("counter: %v", err)
	}
	if counter.Quantity != 6 {
		t.Fatalf("counter.qty=%d want 6", counter.Quantity)
	}
	post, err := svc.Store.GetHoldingByID(context.Background(), h.ID)
	if err != nil {
		t.Fatalf("GetHoldingByID: %v", err)
	}
	if post.ReservedCount != 6 {
		t.Fatalf("reserved_count after counter=%d want 6", post.ReservedCount)
	}
}

// AcceptOTCOffer end-to-end: premium SAGA runs, contract is minted,
// reservation rolls over to the contract (seller's reserved_count stays
// at the iteration's qty).
func TestIntegration_OTC_Accept_MintsContract(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	sec, _ := seedStock(t, svc, "AAPL", ex, "150", "150", "149", 1000)

	sellerID := uuid.NewString()
	sellerAcc := uuid.NewString()
	publishHolding(t, svc, sellerID, domain.KindClient, sec.ID, sellerAcc, 10, "100")
	buyerID := uuid.NewString()
	buyerAcc := uuid.NewString()
	currentReservations.setBalance(buyerAcc, "10000")

	h := findHolding(t, svc, sellerID)
	offer, err := svc.CreateOTCOffer(clientOTCCtx(buyerID), CreateOTCOfferInput{
		SellerHoldingID: h.ID,
		BuyerAccountID:  buyerAcc,
		SellerAccountID: sellerAcc,
		Quantity:        4,
		PricePerUnit:    "155",
		Premium:         "20",
		SettlementDate:  time.Now().AddDate(0, 1, 0),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Seller accepts (modifier was the buyer; counterparty = seller).
	res, err := svc.AcceptOTCOffer(clientOTCCtx(sellerID), AcceptOTCOfferInput{ThreadID: offer.ThreadID})
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if res.Contract == nil {
		t.Fatal("contract is nil")
	}
	if res.Contract.Status != domain.OTCContractActive {
		t.Fatalf("contract status=%s want active", res.Contract.Status)
	}
	if res.Contract.StrikePrice != "155.0000" {
		t.Fatalf("strike=%s want 155.0000", res.Contract.StrikePrice)
	}
	if res.Contract.PremiumPaid != "20.0000" {
		t.Fatalf("premium_paid=%s want 20.0000", res.Contract.PremiumPaid)
	}
	// Premium debited from buyer, credited to seller.
	if currentReservations.balance(buyerAcc) != "9980.0000" {
		t.Fatalf("buyer balance=%s want 9980.0000", currentReservations.balance(buyerAcc))
	}
	if currentReservations.balance(sellerAcc) != "20.0000" {
		t.Fatalf("seller balance=%s want 20.0000", currentReservations.balance(sellerAcc))
	}
	// Holding reservation stays at 4 (rolled over to contract).
	post, err := svc.Store.GetHoldingByID(context.Background(), h.ID)
	if err != nil {
		t.Fatalf("GetHoldingByID: %v", err)
	}
	if post.ReservedCount != 4 {
		t.Fatalf("reserved_count after accept=%d want 4", post.ReservedCount)
	}
	// Offer flipped to accepted.
	thread, err := svc.GetOTCThread(clientOTCCtx(sellerID), offer.ThreadID)
	if err != nil {
		t.Fatalf("GetOTCThread: %v", err)
	}
	if len(thread.Iterations) != 1 || thread.Iterations[0].Status != domain.OTCStatusAccepted {
		t.Fatalf("offer status not flipped to accepted: %+v", thread.Iterations)
	}
}

// ExerciseOTCContract end-to-end: buyer pays qty*strike, shares
// transfer (with weighted-avg cost basis = strike on buyer side),
// seller's holding decrements + reserved_count drops; seller's
// realized_gain row appears.
func TestIntegration_OTC_Exercise_TransfersSharesAndCash(t *testing.T) {
	svc := setup(t)
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
		PricePerUnit:    "155", // strike
		Premium:         "10",
		SettlementDate:  time.Now().AddDate(0, 1, 0),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	accept, err := svc.AcceptOTCOffer(clientOTCCtx(sellerID), AcceptOTCOfferInput{ThreadID: offer.ThreadID})
	if err != nil {
		t.Fatalf("accept: %v", err)
	}

	// Buyer exercises.
	res, err := svc.ExerciseOTCContract(clientOTCCtx(buyerID), ExerciseOTCContractInput{ContractID: accept.Contract.ID})
	if err != nil {
		t.Fatalf("exercise: %v", err)
	}
	if res.Contract.Status != domain.OTCContractExercised {
		t.Fatalf("contract status after exercise=%s want exercised", res.Contract.Status)
	}

	// Buyer balance: started 100000, paid 10 premium + 4*155=620 strike = 99370.
	if got := currentReservations.balance(buyerAcc); got != "99370.0000" {
		t.Fatalf("buyer balance after exercise=%s want 99370.0000", got)
	}
	// Seller balance: 10 (premium) + 620 (strike) = 630.
	if got := currentReservations.balance(sellerAcc); got != "630.0000" {
		t.Fatalf("seller balance after exercise=%s want 630.0000", got)
	}

	// Seller holding: 10 - 4 = 6 quantity, 0 reserved (contract released
	// its reservation on the transfer_shares step).
	postSeller, err := svc.Store.GetHoldingByID(context.Background(), h.ID)
	if err != nil {
		t.Fatalf("GetHoldingByID seller: %v", err)
	}
	if postSeller.Quantity != 6 {
		t.Fatalf("seller qty after exercise=%d want 6", postSeller.Quantity)
	}
	if postSeller.ReservedCount != 0 {
		t.Fatalf("seller reserved_count after exercise=%d want 0", postSeller.ReservedCount)
	}

	// Buyer holding: 4 shares at weighted-avg = strike (155).
	buyerH := findHolding(t, svc, buyerID)
	if buyerH.Quantity != 4 {
		t.Fatalf("buyer qty after exercise=%d want 4", buyerH.Quantity)
	}
	if !numericEq(buyerH.WeightedAvgPrice, "155") {
		t.Fatalf("buyer weighted_avg=%s want 155", buyerH.WeightedAvgPrice)
	}

	// Seller realized_gain row exists. profit_native = 4 * (155 - 100) = 220.
	gains, err := svc.Store.ListRealizedGains(context.Background(), store.RealizedGainFilter{UserID: sellerID})
	if err != nil {
		t.Fatalf("ListRealizedGains: %v", err)
	}
	if len(gains) != 1 {
		t.Fatalf("realized_gains rows=%d want 1", len(gains))
	}
	if !numericEq(gains[0].GainNative, "220") {
		t.Fatalf("realized gain native=%s want 220", gains[0].GainNative)
	}
}

// EDGE-9: expired contract releases shares but does NOT refund premium.
func TestIntegration_OTC_Expiry_ReleasesSharesNotPremium(t *testing.T) {
	svc := setup(t)
	ex := seedExchange(t, svc, "XNYS", domain.CurrencyUSD)
	sec, _ := seedStock(t, svc, "AAPL", ex, "150", "150", "149", 1000)

	sellerID := uuid.NewString()
	sellerAcc := uuid.NewString()
	publishHolding(t, svc, sellerID, domain.KindClient, sec.ID, sellerAcc, 10, "100")
	buyerID := uuid.NewString()
	buyerAcc := uuid.NewString()
	currentReservations.setBalance(buyerAcc, "10000")

	h := findHolding(t, svc, sellerID)
	offer, err := svc.CreateOTCOffer(clientOTCCtx(buyerID), CreateOTCOfferInput{
		SellerHoldingID: h.ID,
		BuyerAccountID:  buyerAcc,
		SellerAccountID: sellerAcc,
		Quantity:        3,
		PricePerUnit:    "155",
		Premium:         "15",
		SettlementDate:  time.Now().AddDate(0, 0, 1), // tomorrow
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.AcceptOTCOffer(clientOTCCtx(sellerID), AcceptOTCOfferInput{ThreadID: offer.ThreadID}); err != nil {
		t.Fatalf("accept: %v", err)
	}
	// Premium has been moved: buyer 9985, seller 15.
	prePremiumBuyer := currentReservations.balance(buyerAcc)
	prePremiumSeller := currentReservations.balance(sellerAcc)

	// Time-shift the service clock to past the settlement date.
	svc.Now = func() time.Time { return time.Now().AddDate(0, 0, 2) }
	res, err := svc.SweepExpiredOTCContracts(context.Background())
	if err != nil {
		t.Fatalf("SweepExpiredOTCContracts: %v", err)
	}
	if res.ContractsExpired != 1 {
		t.Fatalf("expired=%d want 1", res.ContractsExpired)
	}
	// Reservation released.
	postHolding, err := svc.Store.GetHoldingByID(context.Background(), h.ID)
	if err != nil {
		t.Fatalf("GetHoldingByID: %v", err)
	}
	if postHolding.ReservedCount != 0 {
		t.Fatalf("reserved_count after expiry=%d want 0", postHolding.ReservedCount)
	}
	// Premium NOT refunded (EDGE-9).
	if currentReservations.balance(buyerAcc) != prePremiumBuyer {
		t.Fatalf("expiry refunded premium to buyer (got %s, want %s)", currentReservations.balance(buyerAcc), prePremiumBuyer)
	}
	if currentReservations.balance(sellerAcc) != prePremiumSeller {
		t.Fatalf("expiry reversed seller premium (got %s, want %s)", currentReservations.balance(sellerAcc), prePremiumSeller)
	}
}

// =====================================================================
// Helpers
// =====================================================================

func findHolding(t *testing.T, svc *Service, userID string) *domain.Holding {
	t.Helper()
	holdings, err := svc.Store.ListHoldings(context.Background(), store.HoldingFilter{UserID: userID})
	if err != nil {
		t.Fatalf("ListHoldings: %v", err)
	}
	if len(holdings) == 0 {
		t.Fatalf("no holding for user %s", userID)
	}
	return holdings[0]
}
