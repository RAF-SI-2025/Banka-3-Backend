// External OTC trading service (celina 5 — spec p.77+).
//
// Cross-bank counterpart of otc.go. Two roles:
//
//   * Outbound flow — a local user opens a thread against a partner-
//     advertised holding. The service writes the local mirror, then
//     dials the partner via PartnerOTC. On partner ACK the local
//     mirror gets stamped with the partner's thread id.
//
//   * Inbound flow — the gateway translates a partner request and calls
//     a Receive* method here. The service updates the local mirror;
//     no outbound partner calls fire (the partner already sent us the
//     event).
//
// Bank-side cash legs (premium on accept, strike on exercise) flow
// through the bank 2PC primitive in BE-5; until that lands the
// Accept/Exercise methods return a FailedPrecondition stub.

package service

import (
	"context"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// PartnerOTC is the outbound-side partner adapter. The trading service
// calls these on Create/Counter/Withdraw/Accept; the gateway's
// interbank client (BE-4) implements them. Nil-safe — every method
// must tolerate a nil receiver in unit tests; the service nil-checks
// before dialing.
//
// Discovery is split out because it fans out across partners, which
// is more naturally a gateway concern.
type PartnerOTC interface {
	// Discover queries every configured partner bank for advertised
	// holdings, optionally filtered by bank_code / ticker. The gateway
	// merges responses across partners. tickerFilter == "" returns all.
	Discover(ctx context.Context, bankCode, tickerFilter string) ([]*PartnerHolding, error)

	// CreateOffer POSTs a new outbound offer to the partner identified
	// by remote_bank_code. Returns the partner's assigned thread id +
	// any display data they echoed back.
	CreateOffer(ctx context.Context, in PartnerCreateOfferInput) (*PartnerCreateOfferOutput, error)

	// Counter / Withdraw / Accept relay the corresponding action to the
	// partner. AcceptOffer also carries the premium-leg 2PC handle so
	// the partner can settle their side; concrete shape lands in BE-5.
	Counter(ctx context.Context, in PartnerActionInput) error
	Withdraw(ctx context.Context, in PartnerActionInput) error
	Accept(ctx context.Context, in PartnerActionInput) error
}

// PartnerHolding is one row from a partner's /otc/public response,
// normalized into the trading-service domain.
type PartnerHolding struct {
	BankCode         string
	SellerUserRef    string
	SellerDisplay    string
	SellerHoldingRef string
	SecurityTicker   string
	SecurityType     domain.SecurityType
	Currency         domain.Currency
	Quantity         int32
	AskPrice         string
	Premium          string
}

// PartnerCreateOfferInput is the outbound side of CreateOffer.
type PartnerCreateOfferInput struct {
	RemoteBankCode   string
	RemoteUserRef    string
	SellerHoldingRef string
	SecurityTicker   string
	SecurityType     domain.SecurityType
	Currency         domain.Currency
	Quantity         int32
	PricePerUnit     string
	Premium          string
	SettlementDate   time.Time
	// LocalThreadID is our local mirror's id; we send it so the
	// partner can echo it back on subsequent messages — keeps the
	// thread identifiable on both sides without forcing the partner
	// to learn our UUID format.
	LocalThreadID    string
	LocalUserRef     string // partner sees this as remote_user_ref on their side
	LocalDisplayName string
	LocalAccountRef  string
}

// PartnerCreateOfferOutput is what the partner sent back.
type PartnerCreateOfferOutput struct {
	RemoteThreadID    string
	RemoteUserDisplay string
	RemoteAccountRef  string
}

// PartnerActionInput is the outbound side of Counter/Withdraw/Accept.
// Counter carries the new terms; Withdraw / Accept ignore them.
//
// LocalThreadID is the sender-side thread id. The native action URL
// encodes it as the path's {thread_id} so the partner's receive
// handler can resolve their local mirror via
// (remote_bank_code=sender_bank, remote_thread_id=sender_thread_id) —
// the same key the mirror was minted under at CreateOffer time.
type PartnerActionInput struct {
	RemoteBankCode string
	RemoteThreadID string
	LocalThreadID  string

	Quantity       int32
	PricePerUnit   string
	Premium        string
	SettlementDate time.Time
}

// =====================================================================
// Outbound — user-facing.
// =====================================================================

// ListExternalPublicHoldings — discovery aggregation across partner
// banks. Authorisation = OTC trader (same as local OTC).
func (s *Service) ListExternalPublicHoldings(ctx context.Context, bankCode, tickerFilter string) ([]*PartnerHolding, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if err := requireOTCTrader(p); err != nil {
		return nil, err
	}
	if s.PartnerOTC == nil {
		return nil, apperr.FailedPrecondition("cross-bank discovery nije konfigurisana")
	}
	return s.PartnerOTC.Discover(ctx, bankCode, tickerFilter)
}

// CreateExternalOTCOfferInput captures the FE form payload.
type CreateExternalOTCOfferInput struct {
	RemoteBankCode    string
	RemoteUserRef     string
	RemoteDisplayName string
	BuyerAccountID    string
	SellerHoldingRef  string
	SecurityTicker    string
	SecurityType      domain.SecurityType
	Currency          domain.Currency
	Quantity          int32
	PricePerUnit      string
	Premium           string
	SettlementDate    time.Time
}

// CreateExternalOTCOffer opens a new outbound thread. Caller is the
// buyer. Writes the local mirror, dials the partner, stamps the
// partner's thread id, returns the live mirror.
func (s *Service) CreateExternalOTCOffer(ctx context.Context, in CreateExternalOTCOfferInput) (*domain.ExternalOTCThread, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if err := requireOTCTrader(p); err != nil {
		return nil, err
	}
	if s.PartnerOTC == nil {
		return nil, apperr.FailedPrecondition("cross-bank trgovina nije konfigurisana")
	}
	if err := validateOTCMoneyFields(in.Quantity, in.PricePerUnit, in.Premium); err != nil {
		return nil, err
	}
	if in.SettlementDate.IsZero() || !in.SettlementDate.After(s.now()) {
		return nil, apperr.Validation("datum izvršenja mora biti u budućnosti")
	}
	if in.Currency == "" || !in.Currency.Supported() {
		return nil, apperr.Validation("nepoznata valuta")
	}
	if in.RemoteBankCode == "" || in.RemoteUserRef == "" || in.SellerHoldingRef == "" {
		return nil, apperr.Validation("nedostaju partner-bank identifikatori")
	}

	// Resolve the buyer's local account (must be owned by p.UserID,
	// 18-digit number, currency match). Bank gRPC dial lives in app/;
	// here we trust the proto-side validate to have rejected obvious
	// malformed input and lean on BankReservations for the lookup.
	if s.Reservations == nil {
		return nil, apperr.FailedPrecondition("bank reservation surface nije konfigurisan")
	}
	acctNum, err := s.Reservations.AccountNumber(ctx, in.BuyerAccountID)
	if err != nil {
		return nil, apperr.Validation("kupčev račun nije pronađen")
	}
	if len(acctNum) != 18 {
		return nil, apperr.Internal("local account number nema 18 cifara", nil)
	}

	thread := &domain.ExternalOTCThread{
		Direction:          domain.ExternalOTCOutgoing,
		RemoteBankCode:     in.RemoteBankCode,
		RemoteThreadID:     "", // filled by partner ACK below.
		RemoteUserRef:      in.RemoteUserRef,
		RemoteDisplayName:  in.RemoteDisplayName,
		LocalUserID:        p.UserID,
		LocalUserKind:      principalUserKind(p),
		LocalAccountID:     in.BuyerAccountID,
		LocalAccountNumber: acctNum,
		LocalRole:          domain.ExternalOTCRoleBuyer,
		SecurityTicker:     in.SecurityTicker,
		SellerHoldingRef:   in.SellerHoldingRef,
		Quantity:           in.Quantity,
		PricePerUnit:       in.PricePerUnit,
		Premium:            in.Premium,
		Currency:           in.Currency,
		SettlementDate:     in.SettlementDate,
		ModifiedBySide:     domain.ExternalOTCSideLocal,
		Status:             domain.ExternalOTCThreadOpen,
	}

	var live *domain.ExternalOTCThread
	if err := s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		t, err := s.Store.InsertExternalOTCThread(ctx, tx, thread)
		if err != nil {
			return err
		}
		_, err = s.Store.InsertExternalOTCIteration(ctx, tx, &domain.ExternalOTCIteration{
			ThreadID:       t.ID,
			ProposedBySide: domain.ExternalOTCSideLocal,
			Quantity:       t.Quantity,
			PricePerUnit:   t.PricePerUnit,
			Premium:        t.Premium,
			SettlementDate: t.SettlementDate,
		})
		if err != nil {
			return err
		}
		live = t
		return nil
	}); err != nil {
		return nil, err
	}

	// Outbound call — partner must ACK before we consider the thread
	// committed. A partner 4xx flips the local mirror to 'rejected'
	// for the user to retry.
	out, err := s.PartnerOTC.CreateOffer(ctx, PartnerCreateOfferInput{
		RemoteBankCode:   in.RemoteBankCode,
		RemoteUserRef:    in.RemoteUserRef,
		SellerHoldingRef: in.SellerHoldingRef,
		SecurityTicker:   in.SecurityTicker,
		SecurityType:     in.SecurityType,
		Currency:         in.Currency,
		Quantity:         in.Quantity,
		PricePerUnit:     in.PricePerUnit,
		Premium:          in.Premium,
		SettlementDate:   in.SettlementDate,
		LocalThreadID:    live.ID,
		LocalUserRef:     live.LocalUserID,
		LocalDisplayName: in.RemoteDisplayName, // partner's view echoes back; FE pre-fills
		LocalAccountRef:  live.LocalAccountNumber,
	})
	if err != nil {
		// Best-effort flip to 'rejected' so the FE can show a final
		// state. Swallow the inner error — the user-facing 5xx is
		// already informative.
		_ = s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
			_, errRej := s.Store.SetExternalOTCThreadStatus(ctx, tx, live.ID, domain.ExternalOTCThreadRejected)
			return errRej
		})
		return nil, apperr.FailedPrecondition("partner banka je odbila ponudu: " + err.Error())
	}

	if out.RemoteThreadID != "" || out.RemoteAccountRef != "" || out.RemoteUserDisplay != "" {
		if err := s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
			t, err := s.Store.SetExternalOTCThreadRemoteIdentity(
				ctx, tx, live.ID, out.RemoteThreadID, out.RemoteAccountRef, out.RemoteUserDisplay)
			if err != nil {
				return err
			}
			live = t
			return nil
		}); err != nil {
			return nil, err
		}
	}
	return live, nil
}

// ListExternalOTCThreads — FE board.
func (s *Service) ListExternalOTCThreads(ctx context.Context, status string) ([]*domain.ExternalOTCThread, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if err := requireOTCTrader(p); err != nil {
		return nil, err
	}
	return s.Store.ListExternalOTCThreads(ctx, store.ExternalOTCThreadFilter{
		LocalUserID: p.UserID,
		Status:      status,
	})
}

// GetExternalOTCThreadResult bundles the thread + its iterations + the
// contract (when minted).
type GetExternalOTCThreadResult struct {
	Thread     *domain.ExternalOTCThread
	Iterations []*domain.ExternalOTCIteration
	Contract   *domain.ExternalOTCContract
}

// GetExternalOTCThread returns one thread + its iterations + the
// contract (if any).
func (s *Service) GetExternalOTCThread(ctx context.Context, threadID string) (*GetExternalOTCThreadResult, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if err := requireOTCTrader(p); err != nil {
		return nil, err
	}
	t, err := s.Store.GetExternalOTCThread(ctx, threadID)
	if err != nil {
		return nil, err
	}
	if err := assertExternalOTCParty(p, t); err != nil {
		return nil, err
	}
	its, err := s.Store.ListExternalOTCIterations(ctx, threadID)
	if err != nil {
		return nil, err
	}
	res := &GetExternalOTCThreadResult{Thread: t, Iterations: its}
	if c, err := s.Store.GetExternalOTCContractByThread(ctx, threadID); err == nil {
		res.Contract = c
	}
	return res, nil
}

// CounterExternalOTCOfferInput captures the FE counter-offer payload.
type CounterExternalOTCOfferInput struct {
	BankCode       string
	ThreadID       string
	Quantity       int32
	PricePerUnit   string
	Premium        string
	SettlementDate time.Time
}

// CounterExternalOTCOffer applies a counter-offer to an outbound or
// inbound thread. Side that just moved must be the *other* side; the
// service enforces that.
func (s *Service) CounterExternalOTCOffer(ctx context.Context, in CounterExternalOTCOfferInput) (*domain.ExternalOTCThread, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if err := requireOTCTrader(p); err != nil {
		return nil, err
	}
	if err := validateOTCMoneyFields(in.Quantity, in.PricePerUnit, in.Premium); err != nil {
		return nil, err
	}
	if s.PartnerOTC == nil {
		return nil, apperr.FailedPrecondition("cross-bank trgovina nije konfigurisana")
	}

	var live *domain.ExternalOTCThread
	if err := s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		t, err := s.Store.GetExternalOTCThread(ctx, in.ThreadID)
		if err != nil {
			return err
		}
		if err := assertExternalOTCParty(p, t); err != nil {
			return err
		}
		if t.Status != domain.ExternalOTCThreadOpen {
			return apperr.FailedPrecondition("nit više nije otvorena")
		}
		if t.ModifiedBySide == domain.ExternalOTCSideLocal {
			return apperr.FailedPrecondition("druga strana je na potezu")
		}
		updated, err := s.Store.UpdateExternalOTCThreadTerms(ctx, tx, t.ID,
			in.Quantity, in.PricePerUnit, in.Premium, in.SettlementDate,
			domain.ExternalOTCSideLocal)
		if err != nil {
			return err
		}
		if _, err := s.Store.InsertExternalOTCIteration(ctx, tx, &domain.ExternalOTCIteration{
			ThreadID:       updated.ID,
			ProposedBySide: domain.ExternalOTCSideLocal,
			Quantity:       updated.Quantity,
			PricePerUnit:   updated.PricePerUnit,
			Premium:        updated.Premium,
			SettlementDate: updated.SettlementDate,
		}); err != nil {
			return err
		}
		live = updated
		return nil
	}); err != nil {
		return nil, err
	}

	if err := s.PartnerOTC.Counter(ctx, PartnerActionInput{
		RemoteBankCode: live.RemoteBankCode,
		RemoteThreadID: live.RemoteThreadID,
		LocalThreadID:  live.ID,
		Quantity:       live.Quantity,
		PricePerUnit:   live.PricePerUnit,
		Premium:        live.Premium,
		SettlementDate: live.SettlementDate,
	}); err != nil {
		return nil, apperr.FailedPrecondition("partner banka je odbila kontraponudu: " + err.Error())
	}
	return live, nil
}

// WithdrawExternalOTCOffer pulls a thread. Either party in 'open' state
// may withdraw.
func (s *Service) WithdrawExternalOTCOffer(ctx context.Context, bankCode, threadID string) (*domain.ExternalOTCThread, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if err := requireOTCTrader(p); err != nil {
		return nil, err
	}
	if s.PartnerOTC == nil {
		return nil, apperr.FailedPrecondition("cross-bank trgovina nije konfigurisana")
	}

	var live *domain.ExternalOTCThread
	if err := s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		t, err := s.Store.GetExternalOTCThread(ctx, threadID)
		if err != nil {
			return err
		}
		if err := assertExternalOTCParty(p, t); err != nil {
			return err
		}
		if t.Status != domain.ExternalOTCThreadOpen {
			return apperr.FailedPrecondition("nit više nije otvorena")
		}
		updated, err := s.Store.SetExternalOTCThreadStatus(ctx, tx, t.ID, domain.ExternalOTCThreadWithdrawn)
		if err != nil {
			return err
		}
		live = updated
		return nil
	}); err != nil {
		return nil, err
	}

	// Best-effort partner notification — local state is already
	// terminal. Partner reconciles on next poll if this fails.
	if err := s.PartnerOTC.Withdraw(ctx, PartnerActionInput{
		RemoteBankCode: live.RemoteBankCode,
		RemoteThreadID: live.RemoteThreadID,
		LocalThreadID:  live.ID,
	}); err != nil {
		s.Log.Warn("partner withdraw notification failed",
			"thread_id", live.ID, "remote_bank_code", live.RemoteBankCode, "err", err.Error())
	}
	return live, nil
}

// AcceptExternalOTCOfferResult bundles the accepted thread + minted
// contract.
type AcceptExternalOTCOfferResult struct {
	Thread   *domain.ExternalOTCThread
	Contract *domain.ExternalOTCContract
}

// AcceptExternalOTCOffer drives the outgoing accept saga (BE-7). The
// caller must be the buyer (local-role buyer) on an open thread the
// partner moved last. For incoming threads the partner's call hits
// ReceiveExternalOTCAccept instead, which mints the local mirror
// contract and awaits the partner's 2PC commit.
func (s *Service) AcceptExternalOTCOffer(ctx context.Context, bankCode, threadID string) (*AcceptExternalOTCOfferResult, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if err := requireOTCTrader(p); err != nil {
		return nil, err
	}
	t, err := s.Store.GetExternalOTCThread(ctx, threadID)
	if err != nil {
		return nil, err
	}
	if err := assertExternalOTCParty(p, t); err != nil {
		return nil, err
	}
	return s.acceptExternalOutgoing(ctx, t)
}

// ListExternalOTCContracts — FE "Sklopljeni eksterni ugovori".
func (s *Service) ListExternalOTCContracts(ctx context.Context, status string) ([]*domain.ExternalOTCContract, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if err := requireOTCTrader(p); err != nil {
		return nil, err
	}
	return s.Store.ListExternalOTCContracts(ctx, p.UserID, status)
}

// ExerciseExternalOTCContract drives the outgoing exercise saga
// (BE-7). Caller must be the local buyer on an active contract.
func (s *Service) ExerciseExternalOTCContract(ctx context.Context, bankCode, contractID, exerciseOpID string) (*domain.ExternalOTCContract, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if err := requireOTCTrader(p); err != nil {
		return nil, err
	}
	c, err := s.Store.GetExternalOTCContract(ctx, contractID)
	if err != nil {
		return nil, err
	}
	// Only the local user-of-record on the contract (or admin) can
	// exercise. The exerciseOpID param is forwarded but currently
	// ignored — the saga derives a deterministic id from contractID.
	_ = exerciseOpID
	if c.LocalUserID != p.UserID && !isAdmin(p) {
		return nil, apperr.PermissionDenied("nemate pravo nad ugovorom")
	}
	return s.exerciseExternalOutgoing(ctx, c)
}

// =====================================================================
// Inbound — invoked by the gateway when a partner reaches us. No
// outbound partner calls fire here.
// =====================================================================

// ReceiveExternalOTCOfferInput is the inbound counterpart of
// CreateExternalOTCOfferInput. Sender = partner; we are the seller.
type ReceiveExternalOTCOfferInput struct {
	SenderBankCode    string
	SenderUserRef     string
	SenderDisplayName string
	SenderThreadID    string
	SellerHoldingRef  string // resolves locally to a portfolio_holdings row uuid
	Quantity          int32
	PricePerUnit      string
	Premium           string
	SettlementDate    time.Time
}

// ReceiveExternalOTCOffer mirrors an incoming partner offer locally.
// Caller is the gateway with admin sentinel principal.
func (s *Service) ReceiveExternalOTCOffer(ctx context.Context, in ReceiveExternalOTCOfferInput) (*domain.ExternalOTCThread, error) {
	if err := validateOTCMoneyFields(in.Quantity, in.PricePerUnit, in.Premium); err != nil {
		return nil, err
	}
	if in.SenderBankCode == "" || in.SenderUserRef == "" || in.SenderThreadID == "" || in.SellerHoldingRef == "" {
		return nil, apperr.Validation("nedostaju partner identifikatori")
	}

	// Resolve the local seller holding the partner is offering against.
	// SellerHoldingRef is a uuid (we control the format on /otc/public).
	holding, err := s.Store.GetHoldingByID(ctx, in.SellerHoldingRef)
	if err != nil {
		return nil, apperr.Validation("hartija ne postoji ili nije javno objavljena")
	}
	sec, err := s.Store.GetSecurity(ctx, holding.SecurityID)
	if err != nil {
		return nil, apperr.Internal("get security", err)
	}
	acctNum, err := s.resolveSellerAccountNumber(ctx, holding)
	if err != nil {
		return nil, err
	}

	thread := &domain.ExternalOTCThread{
		Direction:          domain.ExternalOTCIncoming,
		RemoteBankCode:     in.SenderBankCode,
		RemoteThreadID:     in.SenderThreadID,
		RemoteUserRef:      in.SenderUserRef,
		RemoteDisplayName:  in.SenderDisplayName,
		LocalUserID:        holding.UserID,
		LocalUserKind:      holding.UserKind,
		LocalAccountID:     holding.AccountID,
		LocalAccountNumber: acctNum,
		LocalRole:          domain.ExternalOTCRoleSeller,
		SecurityID:         sec.ID,
		SecurityTicker:     sec.Ticker,
		SellerHoldingRef:   holding.ID,
		Quantity:           in.Quantity,
		PricePerUnit:       in.PricePerUnit,
		Premium:            in.Premium,
		Currency:           sec.Currency,
		SettlementDate:     in.SettlementDate,
		ModifiedBySide:     domain.ExternalOTCSideRemote,
		Status:             domain.ExternalOTCThreadOpen,
	}

	var live *domain.ExternalOTCThread
	if err := s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		t, err := s.Store.InsertExternalOTCThread(ctx, tx, thread)
		if err != nil {
			return err
		}
		_, err = s.Store.InsertExternalOTCIteration(ctx, tx, &domain.ExternalOTCIteration{
			ThreadID:       t.ID,
			ProposedBySide: domain.ExternalOTCSideRemote,
			Quantity:       t.Quantity,
			PricePerUnit:   t.PricePerUnit,
			Premium:        t.Premium,
			SettlementDate: t.SettlementDate,
		})
		if err != nil {
			return err
		}
		live = t
		return nil
	}); err != nil {
		return nil, err
	}
	return live, nil
}

// ReceiveExternalOTCCounterInput.
type ReceiveExternalOTCCounterInput struct {
	SenderBankCode string
	SenderThreadID string
	Quantity       int32
	PricePerUnit   string
	Premium        string
	SettlementDate time.Time
}

// ReceiveExternalOTCCounter applies an incoming counter-offer to the
// local mirror.
func (s *Service) ReceiveExternalOTCCounter(ctx context.Context, in ReceiveExternalOTCCounterInput) (*domain.ExternalOTCThread, error) {
	if err := validateOTCMoneyFields(in.Quantity, in.PricePerUnit, in.Premium); err != nil {
		return nil, err
	}
	var live *domain.ExternalOTCThread
	if err := s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		t, err := s.Store.GetExternalOTCThreadByRemote(ctx, tx, in.SenderBankCode, in.SenderThreadID)
		if err != nil {
			return err
		}
		if t.Status != domain.ExternalOTCThreadOpen {
			return apperr.FailedPrecondition("nit više nije otvorena")
		}
		updated, err := s.Store.UpdateExternalOTCThreadTerms(ctx, tx, t.ID,
			in.Quantity, in.PricePerUnit, in.Premium, in.SettlementDate,
			domain.ExternalOTCSideRemote)
		if err != nil {
			return err
		}
		if _, err := s.Store.InsertExternalOTCIteration(ctx, tx, &domain.ExternalOTCIteration{
			ThreadID:       updated.ID,
			ProposedBySide: domain.ExternalOTCSideRemote,
			Quantity:       updated.Quantity,
			PricePerUnit:   updated.PricePerUnit,
			Premium:        updated.Premium,
			SettlementDate: updated.SettlementDate,
		}); err != nil {
			return err
		}
		live = updated
		return nil
	}); err != nil {
		return nil, err
	}
	return live, nil
}

// ReceiveExternalOTCAction is the inbound shape of withdraw/accept.
type ReceiveExternalOTCAction struct {
	SenderBankCode string
	SenderThreadID string
}

// ReceiveExternalOTCWithdraw flips the local mirror to 'withdrawn'.
func (s *Service) ReceiveExternalOTCWithdraw(ctx context.Context, in ReceiveExternalOTCAction) (*domain.ExternalOTCThread, error) {
	return s.setRemoteThreadStatus(ctx, in.SenderBankCode, in.SenderThreadID, domain.ExternalOTCThreadWithdrawn)
}

// ReceiveExternalOTCAccept handles a partner-initiated accept on an
// incoming thread (we are the seller). Flips the thread to 'accepted'
// AND mints the local contract row so "Sklopljeni eksterni ugovori"
// renders immediately. premium_op_id is left NULL — the partner
// separately drives the cross-bank premium 2PC against our bank, and
// the eventual op_id can be stamped later via SetExternalOTCContractPremiumOp
// (BE-7c). Idempotent: replays return the existing contract.
func (s *Service) ReceiveExternalOTCAccept(ctx context.Context, in ReceiveExternalOTCAction) (*domain.ExternalOTCThread, error) {
	var live *domain.ExternalOTCThread
	if err := s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		t, err := s.Store.GetExternalOTCThreadByRemote(ctx, tx, in.SenderBankCode, in.SenderThreadID)
		if err != nil {
			return err
		}
		// Only an incoming thread can be partner-accepted. (If they're
		// accepting a thread we initiated, the partner_otc.go inbound
		// route should never have routed it here.)
		if t.Direction != domain.ExternalOTCIncoming {
			return apperr.FailedPrecondition("partner može prihvatiti samo dolaznu nit")
		}
		if t.Status == domain.ExternalOTCThreadAccepted {
			// Idempotent — refetch + return.
			live = t
			return nil
		}
		if t.Status != domain.ExternalOTCThreadOpen {
			return apperr.FailedPrecondition("nit nije otvorena")
		}
		updated, err := s.Store.SetExternalOTCThreadStatus(ctx, tx, t.ID, domain.ExternalOTCThreadAccepted)
		if err != nil {
			return err
		}
		// Mint the contract row mirroring the thread's terms. Strike =
		// price_per_unit at accept time (spec p.67.b).
		if _, err := s.Store.InsertExternalOTCContract(ctx, tx, &domain.ExternalOTCContract{
			ThreadID:           updated.ID,
			Direction:          domain.ExternalOTCIncoming,
			RemoteBankCode:     updated.RemoteBankCode,
			RemoteThreadID:     updated.RemoteThreadID,
			RemoteUserRef:      updated.RemoteUserRef,
			RemoteDisplayName:  updated.RemoteDisplayName,
			RemoteAccountRef:   updated.RemoteAccountRef,
			LocalUserID:        updated.LocalUserID,
			LocalUserKind:      updated.LocalUserKind,
			LocalAccountID:     updated.LocalAccountID,
			LocalAccountNumber: updated.LocalAccountNumber,
			LocalRole:          updated.LocalRole,
			SecurityID:         updated.SecurityID,
			SecurityTicker:     updated.SecurityTicker,
			SellerHoldingRef:   updated.SellerHoldingRef,
			Quantity:           updated.Quantity,
			StrikePrice:        updated.PricePerUnit,
			PremiumPaid:        updated.Premium,
			Currency:           updated.Currency,
			SettlementDate:     updated.SettlementDate,
			AcceptedBySide:     domain.ExternalOTCSideRemote,
			Status:             domain.ExternalOTCContractActive,
			// PremiumOpID intentionally empty — stamped post-commit.
		}); err != nil {
			return err
		}
		live = updated
		return nil
	}); err != nil {
		return nil, err
	}
	return live, nil
}

// ReceiveExternalOTCExerciseNoticeInput.
type ReceiveExternalOTCExerciseNoticeInput struct {
	SenderBankCode   string
	SenderContractID string
	ExerciseOpID     string
}

// ReceiveExternalOTCExerciseNotice handles a partner-initiated
// exercise on a contract we hold the seller side of. Flips the local
// contract to 'exercised' and stamps the partner's exercise_op_id.
// Cross-bank strike payment arrives via the partner's 2PC against
// bank.InterbankProtocolService (separate request); this method only
// updates the trading-side audit row.
//
// The partner's exercise_op_id is a free-form string; we derive a
// stable UUID via uuid.NewSHA1 so the local uuid column accepts it.
// Idempotent: replays with the same exercise_op_id no-op (the store's
// SetExternalOTCContractExercised guards on it).
func (s *Service) ReceiveExternalOTCExerciseNotice(ctx context.Context, in ReceiveExternalOTCExerciseNoticeInput) (*domain.ExternalOTCContract, error) {
	if in.SenderContractID == "" || in.ExerciseOpID == "" {
		return nil, apperr.Validation("sender_contract_id and exercise_op_id are required")
	}
	derivedOpID := deriveExternalExerciseOpID(in.SenderBankCode, in.ExerciseOpID)
	// Map partner's contract id to ours via the thread's remote_thread_id
	// — convention: partner's contract_id == their remote_thread_id (the
	// thread they minted from their accept). Look up by (sender_bank_code,
	// that id).
	var live *domain.ExternalOTCContract
	if err := s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		thread, err := s.Store.GetExternalOTCThreadByRemote(ctx, tx, in.SenderBankCode, in.SenderContractID)
		if err != nil {
			return err
		}
		contract, err := s.Store.GetExternalOTCContractByThread(ctx, thread.ID)
		if err != nil {
			return err
		}
		if contract.Status == domain.ExternalOTCContractExercised {
			// Idempotent — already exercised.
			live = contract
			return nil
		}
		if contract.Status != domain.ExternalOTCContractActive {
			return apperr.FailedPrecondition("ugovor nije aktivan")
		}
		updated, err := s.Store.SetExternalOTCContractExercised(ctx, tx, contract.ID, derivedOpID, s.now())
		if err != nil {
			return err
		}
		live = updated
		return nil
	}); err != nil {
		return nil, err
	}
	return live, nil
}

// externalExerciseOpIDNS — namespace for deriving inbound-exercise
// op_ids from (partner_bank_code, partner_exercise_op_id). Disjoint
// from externalCommitOpIDNS so a partner-supplied identifier can't
// collide with our outbound-leg op_ids.
var externalExerciseOpIDNS = uuid.MustParse("c5e1ae00-7e62-4f00-9c1d-1f24f2d8a403")

func deriveExternalExerciseOpID(bankCode, partnerOpID string) string {
	return uuid.NewSHA1(externalExerciseOpIDNS, []byte(bankCode+":"+partnerOpID)).String()
}

// =====================================================================
// Helpers
// =====================================================================

func (s *Service) setRemoteThreadStatus(ctx context.Context, bankCode, remoteThreadID string, status domain.ExternalOTCThreadStatus) (*domain.ExternalOTCThread, error) {
	var live *domain.ExternalOTCThread
	if err := s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		t, err := s.Store.GetExternalOTCThreadByRemote(ctx, tx, bankCode, remoteThreadID)
		if err != nil {
			return err
		}
		updated, err := s.Store.SetExternalOTCThreadStatus(ctx, tx, t.ID, status)
		if err != nil {
			return err
		}
		live = updated
		return nil
	}); err != nil {
		return nil, err
	}
	return live, nil
}

// assertExternalOTCParty rejects principals who aren't the thread's
// local user. Admin bypasses (for support flows).
func assertExternalOTCParty(p auth.Principal, t *domain.ExternalOTCThread) error {
	if t.LocalUserID == p.UserID {
		return nil
	}
	if isAdmin(p) {
		return nil
	}
	return apperr.PermissionDenied("nemate pravo nad ovom niti")
}

// isAdmin shadows the package's existing helpers (used in other
// service files) without re-implementing the auth import dance.
func isAdmin(p auth.Principal) bool {
	for _, perm := range p.Permissions {
		if perm == "admin" {
			return true
		}
	}
	return false
}

// resolveSellerAccountNumber looks up the holding's settlement account
// number on the bank side. Used by inbound Receive* to populate the
// local mirror's account_number column (kept canonical so the gateway
// can echo it back on partner-facing exports).
func (s *Service) resolveSellerAccountNumber(ctx context.Context, h *domain.Holding) (string, error) {
	if s.Reservations == nil {
		return "", apperr.FailedPrecondition("bank surface nije konfigurisan")
	}
	num, err := s.Reservations.AccountNumber(ctx, h.AccountID)
	if err != nil {
		return "", apperr.Internal("resolve seller account number", err)
	}
	if len(num) != 18 {
		return "", apperr.Internal("seller account number nema 18 cifara", nil)
	}
	return num, nil
}

// ensure the helpers exist that we leaned on above.
var _ = money.Parse
