// OTC trading service (c4 — spec p.64-69, 79).
//
// Two parties (clients ↔ clients OR supervisors ↔ supervisors, per spec
// p.79 the mixed case is forbidden) negotiate iterations against a
// single seller-holding row. Accepting the live iteration runs the
// otc_premium SAGA, transfers the premium, and mints an option
// contract that's exercisable until settlement_date.
//
// CRUD shape (this file)
// ======================
//   * ListPublicHoldings — discovery feed (public_count > reserved_count).
//   * CreateOTCOffer — open a thread.
//   * CounterOfferOTC — append a new iteration; supersede the prior open
//     row; adjust reservation if qty changed.
//   * WithdrawOTCOffer — either party releases the thread; reservation
//     is released.
//   * ListOTCThreads / GetOTCThread — buyer/seller view of their threads.
//   * AcceptOTCOffer — see otc_accept_saga.go (this file holds the
//     pre-flight validation + saga kick-off).
//   * ListOTCContracts / GetOTCContract — buyer/seller view of signed
//     contracts.
//
// Reservation invariant (spec p.68)
// =================================
// On offer create / counter-up: `seller_holding.reserved_count` grows
// by the new quantity (delta vs. the prior open iteration when
// countering). On withdraw / counter-down: it shrinks. On accept: it
// rolls over (the offer's reservation transfers to the new contract;
// effectively a no-op since the seller_holding_id is reused). On
// exercise / expiry: the contract releases its share of the reservation.
//
// The Postgres CHECK on portfolio_holdings (reserved_count ≤ quantity)
// is the backstop; the service layer's pre-check translates the
// 23514 to a Serbian FailedPrecondition.

package service

import (
	"context"
	"strings"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/store"
	"github.com/jackc/pgx/v5"
)

// =====================================================================
// Discovery
// =====================================================================

// PublicHoldingRow is one decorated row on the discovery board.
type PublicHoldingRow struct {
	Holding           *domain.Holding
	Security          *domain.Security
	CurrentPrice      string
	AvailableCount    int32
	SellerDisplayName string
}

// ListPublicHoldings returns the OTC discovery board rows the caller is
// allowed to see (own rows always excluded; spec p.79 limits the view
// to peers of the same kind: clients see only client-side public
// holdings, supervisors only supervisor-side).
func (s *Service) ListPublicHoldings(ctx context.Context, tickerFilter string) ([]*PublicHoldingRow, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if err := requireOTCTrader(p); err != nil {
		return nil, err
	}

	peerKind, err := otcPeerKind(p)
	if err != nil {
		return nil, err
	}

	rows, err := s.Store.ListPublicHoldings(ctx, p.UserID)
	if err != nil {
		return nil, err
	}

	out := make([]*PublicHoldingRow, 0, len(rows))
	for _, h := range rows {
		if peerKind != "" && h.UserKind != peerKind {
			continue
		}
		sec, err := s.Store.GetSecurity(ctx, h.SecurityID)
		if err != nil {
			s.Log.Warn("public holding security lookup failed",
				"holding_id", h.ID, "err", err.Error())
			continue
		}
		// Spec p.58: forex/option are not OTC-tradable instruments; the
		// negotiable inventory is stocks (and futures, treated like stocks
		// for OTC discovery).
		if sec.Type != domain.SecurityStock && sec.Type != domain.SecurityFuture {
			continue
		}
		if tickerFilter != "" && !strings.EqualFold(sec.Ticker, tickerFilter) {
			continue
		}
		row := &PublicHoldingRow{
			Holding:        h,
			Security:       sec,
			AvailableCount: h.PublicCount - h.ReservedCount,
		}
		if row.AvailableCount < 0 {
			row.AvailableCount = 0
		}
		if listing, err := s.Store.GetListingBySecurityID(ctx, sec.ID); err == nil {
			row.CurrentPrice = listing.Price
		}
		if s.Users != nil {
			if name, err := s.Users.DisplayName(ctx, h.UserID, h.UserKind); err == nil {
				row.SellerDisplayName = name
			}
		}
		out = append(out, row)
	}
	return out, nil
}

// =====================================================================
// Create / counter / withdraw
// =====================================================================

// CreateOTCOfferInput is the validated request payload.
type CreateOTCOfferInput struct {
	SellerHoldingID string
	BuyerAccountID  string
	SellerAccountID string
	Quantity        int32
	PricePerUnit    string
	Premium         string
	SettlementDate  time.Time
}

// CreateOTCOffer opens a new negotiation thread. The caller is the
// buyer; the seller is derived from `seller_holding_id`. Spec p.79
// enforces same-kind counterparties (client↔client OR supervisor↔
// supervisor). Reservation on the seller's holding is bumped by qty.
func (s *Service) CreateOTCOffer(ctx context.Context, in CreateOTCOfferInput) (*domain.OTCOffer, error) {
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
	if !in.SettlementDate.After(s.now()) {
		return nil, apperr.Validation("settlement_date mora biti u budućnosti")
	}

	holding, err := s.Store.GetHoldingByID(ctx, in.SellerHoldingID)
	if err != nil {
		return nil, err
	}
	sec, err := s.Store.GetSecurity(ctx, holding.SecurityID)
	if err != nil {
		return nil, err
	}
	if sec.Type != domain.SecurityStock && sec.Type != domain.SecurityFuture {
		return nil, apperr.Validation("OTC trgovina ne podržava ovaj tip hartije")
	}

	if holding.UserID == p.UserID {
		return nil, apperr.Validation("ne možete da napravite ponudu na sopstveno vlasništvo")
	}
	if err := assertSameKindCounterparties(p, holding); err != nil {
		return nil, err
	}

	buyerKind := principalUserKind(p)

	var out *domain.OTCOffer
	err = s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		// Spec p.68 — lock the holding row inside the tx so concurrent
		// CreateOTCOffer / CounterOfferOTC calls serialize. The pre-tx
		// `holding` read is a stale snapshot; re-check available against
		// the locked one.
		locked, err := s.Store.GetHoldingForUpdate(ctx, tx, holding.ID)
		if err != nil {
			return err
		}
		available := locked.PublicCount - locked.ReservedCount
		if available <= 0 {
			return apperr.FailedPrecondition("hartija više nije dostupna na OTC")
		}
		if in.Quantity > available {
			return apperr.FailedPrecondition("nedovoljno raspoloživih akcija")
		}
		if _, err := s.Store.IncrementReservedHolding(ctx, tx, locked.ID, in.Quantity); err != nil {
			return err
		}
		o := &domain.OTCOffer{
			SecurityID:      locked.SecurityID,
			SellerHoldingID: locked.ID,
			BuyerID:         p.UserID,
			BuyerKind:       buyerKind,
			BuyerAccountID:  in.BuyerAccountID,
			SellerID:        locked.UserID,
			SellerKind:      locked.UserKind,
			SellerAccountID: in.SellerAccountID,
			Quantity:        in.Quantity,
			PricePerUnit:    money.FormatAmount(money.MustParse(in.PricePerUnit)),
			Premium:         money.FormatAmount(money.MustParse(in.Premium)),
			Currency:        sec.Currency,
			SettlementDate:  in.SettlementDate,
			ModifiedBy:      p.UserID,
			Status:          domain.OTCStatusOpen,
		}
		inserted, err := s.Store.InsertOTCOffer(ctx, tx, o)
		if err != nil {
			return err
		}
		out = inserted
		return nil
	})
	if err != nil {
		return nil, err
	}
	if s.OTCNotifier != nil {
		s.OTCNotifier.OnOTCCounterOffer(ctx, out, out.SellerID, out.SellerKind)
	}
	return out, nil
}

// CounterOfferOTCInput is the validated payload.
type CounterOfferOTCInput struct {
	ThreadID       string
	Quantity       int32
	PricePerUnit   string
	Premium        string
	SettlementDate time.Time
}

// CounterOfferOTC appends a new iteration. The prior open row flips to
// `superseded`; the reservation delta is applied so the seller's
// reserved_count tracks the new (largest committed) quantity.
//
// Either party may counter, but only when the prior iteration's
// modified_by is the OTHER party — you can't counter your own open
// proposal (you'd withdraw and re-create instead). Spec p.69 implies
// this turn-taking by the "Aktivne ponude" filter ("Vaš odgovor").
func (s *Service) CounterOfferOTC(ctx context.Context, in CounterOfferOTCInput) (*domain.OTCOffer, error) {
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
	if !in.SettlementDate.After(s.now()) {
		return nil, apperr.Validation("settlement_date mora biti u budućnosti")
	}

	var out *domain.OTCOffer
	err = s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		open, err := s.Store.GetOpenOTCOfferByThread(ctx, tx, in.ThreadID)
		if err != nil {
			return err
		}
		if open.BuyerID != p.UserID && open.SellerID != p.UserID {
			return apperr.PermissionDenied("niste strana u ovoj pregovaračkoj niti")
		}
		if open.ModifiedBy == p.UserID {
			return apperr.FailedPrecondition("čeka se odgovor druge strane")
		}

		// Quantity delta on the seller's holding.
		delta := in.Quantity - open.Quantity
		if delta > 0 {
			if _, err := s.Store.IncrementReservedHolding(ctx, tx, open.SellerHoldingID, delta); err != nil {
				return err
			}
		} else if delta < 0 {
			if _, err := s.Store.DecrementReservedHolding(ctx, tx, open.SellerHoldingID, -delta); err != nil {
				return err
			}
		}

		if err := s.Store.SupersedePriorOTCOffers(ctx, tx, in.ThreadID); err != nil {
			return err
		}
		o := &domain.OTCOffer{
			ThreadID:        open.ThreadID,
			SecurityID:      open.SecurityID,
			SellerHoldingID: open.SellerHoldingID,
			BuyerID:         open.BuyerID,
			BuyerKind:       open.BuyerKind,
			BuyerAccountID:  open.BuyerAccountID,
			SellerID:        open.SellerID,
			SellerKind:      open.SellerKind,
			SellerAccountID: open.SellerAccountID,
			Quantity:        in.Quantity,
			PricePerUnit:    money.FormatAmount(money.MustParse(in.PricePerUnit)),
			Premium:         money.FormatAmount(money.MustParse(in.Premium)),
			Currency:        open.Currency,
			SettlementDate:  in.SettlementDate,
			ModifiedBy:      p.UserID,
			Status:          domain.OTCStatusOpen,
		}
		inserted, err := s.Store.InsertOTCOffer(ctx, tx, o)
		if err != nil {
			return err
		}
		out = inserted
		return nil
	})
	if err != nil {
		return nil, err
	}
	if s.OTCNotifier != nil {
		recipient, kind := otherParty(out, p.UserID)
		s.OTCNotifier.OnOTCCounterOffer(ctx, out, recipient, kind)
	}
	return out, nil
}

// WithdrawOTCOffer pulls a thread out of negotiation. Either party may
// withdraw; the open iteration flips to `withdrawn` and the seller's
// reservation is released. Already-accepted threads are FailedPrecondition.
func (s *Service) WithdrawOTCOffer(ctx context.Context, threadID string) (*domain.OTCOffer, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if err := requireOTCTrader(p); err != nil {
		return nil, err
	}

	var out *domain.OTCOffer
	err = s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		open, err := s.Store.GetOpenOTCOfferByThread(ctx, tx, threadID)
		if err != nil {
			return err
		}
		if open.BuyerID != p.UserID && open.SellerID != p.UserID {
			return apperr.PermissionDenied("niste strana u ovoj pregovaračkoj niti")
		}
		// Release the reservation for this iteration's qty.
		if open.Quantity > 0 {
			if _, err := s.Store.DecrementReservedHolding(ctx, tx, open.SellerHoldingID, open.Quantity); err != nil {
				return err
			}
		}
		updated, err := s.Store.MarkOTCOfferStatus(ctx, tx, open.ID, domain.OTCStatusWithdrawn)
		if err != nil {
			return err
		}
		out = updated
		return nil
	})
	if err != nil {
		return nil, err
	}
	if s.OTCNotifier != nil {
		recipient, kind := otherParty(out, p.UserID)
		s.OTCNotifier.OnOTCWithdrawn(ctx, out, recipient, kind)
	}
	return out, nil
}

// =====================================================================
// Read paths
// =====================================================================

// ListOTCThreadsInput exposes the supervisor filter.
type ListOTCThreadsInput struct {
	PartyUserID   string
	PartyUserKind domain.UserKind
	Status        string // "open" / "any"
}

// ListOTCThreads returns the latest iteration per thread for the caller
// (or a specified party when the caller is supervisor/admin).
func (s *Service) ListOTCThreads(ctx context.Context, in ListOTCThreadsInput) ([]*domain.OTCOffer, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if err := requireOTCTrader(p); err != nil {
		return nil, err
	}
	f := store.OTCThreadFilter{Status: in.Status}
	if !permissions.HasAny(p.Permissions, permissions.Admin, permissions.ActuarySupervisor) {
		f.PartyID = p.UserID
		f.PartyKind = principalUserKind(p)
	} else if in.PartyUserID != "" {
		f.PartyID = in.PartyUserID
		f.PartyKind = in.PartyUserKind
	}
	return s.Store.ListLatestOTCOffers(ctx, f)
}

// GetOTCThreadResult bundles iterations + contract for the FE modal.
type GetOTCThreadResult struct {
	Iterations []*domain.OTCOffer
	Contract   *domain.OTCContract
}

// GetOTCThread returns every iteration in a thread plus the signed
// contract (if any). Caller must be a party or supervisor/admin.
func (s *Service) GetOTCThread(ctx context.Context, threadID string) (*GetOTCThreadResult, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	iters, err := s.Store.ListOTCThread(ctx, threadID)
	if err != nil {
		return nil, err
	}
	if len(iters) == 0 {
		return nil, apperr.NotFound("nit ne postoji")
	}
	first := iters[0]
	supervisor := permissions.HasAny(p.Permissions, permissions.Admin, permissions.ActuarySupervisor)
	if !supervisor && first.BuyerID != p.UserID && first.SellerID != p.UserID {
		return nil, apperr.PermissionDenied("niste strana u niti")
	}
	res := &GetOTCThreadResult{Iterations: iters}
	if c, err := s.Store.GetOTCContractByThread(ctx, threadID); err == nil {
		res.Contract = c
	}
	return res, nil
}

// ListOTCContractsInput exposes the supervisor filter.
type ListOTCContractsInput struct {
	PartyUserID   string
	PartyUserKind domain.UserKind
	Status        string // "active" / "any"
}

// ListOTCContracts returns the caller's contracts (or a specified
// party's when supervisor/admin).
func (s *Service) ListOTCContracts(ctx context.Context, in ListOTCContractsInput) ([]*domain.OTCContract, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if err := requireOTCTrader(p); err != nil {
		return nil, err
	}
	f := store.OTCContractFilter{Status: in.Status}
	if !permissions.HasAny(p.Permissions, permissions.Admin, permissions.ActuarySupervisor) {
		f.PartyID = p.UserID
		f.PartyKind = principalUserKind(p)
	} else if in.PartyUserID != "" {
		f.PartyID = in.PartyUserID
		f.PartyKind = in.PartyUserKind
	}
	return s.Store.ListOTCContracts(ctx, f)
}

// GetOTCContract returns one contract; caller must be a party or
// supervisor/admin.
func (s *Service) GetOTCContract(ctx context.Context, id string) (*domain.OTCContract, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	c, err := s.Store.GetOTCContract(ctx, id)
	if err != nil {
		return nil, err
	}
	supervisor := permissions.HasAny(p.Permissions, permissions.Admin, permissions.ActuarySupervisor)
	if !supervisor && c.BuyerID != p.UserID && c.SellerID != p.UserID {
		return nil, apperr.PermissionDenied("niste strana u ugovoru")
	}
	return c, nil
}

// =====================================================================
// Helpers
// =====================================================================

// requireOTCTrader rejects principals without an OTC capability.
// Per spec p.4, the client-side OTC capability is bundled into
// TradingClient; supervisors use the dedicated OTCTradeSupervisor.
// Admin shadows both.
func requireOTCTrader(p auth.Principal) error {
	if permissions.HasAny(p.Permissions,
		permissions.Admin,
		permissions.OTCTradeSupervisor,
		permissions.TradingClient) {
		return nil
	}
	return apperr.PermissionDenied("nedovoljne permisije za OTC trgovinu")
}

// otcPeerKind returns the user_kind the caller can negotiate with per
// spec p.79: clients ↔ clients, supervisors ↔ supervisors. Admin sees
// everyone (returns "" = no filter).
func otcPeerKind(p auth.Principal) (domain.UserKind, error) {
	if permissions.Has(p.Permissions, permissions.Admin) {
		return "", nil
	}
	switch p.UserKind {
	case auth.KindClient:
		if !permissions.Has(p.Permissions, permissions.TradingClient) {
			return "", apperr.PermissionDenied("nedovoljne permisije za OTC")
		}
		return domain.KindClient, nil
	case auth.KindEmployee:
		if !permissions.Has(p.Permissions, permissions.OTCTradeSupervisor) {
			return "", apperr.PermissionDenied("nedovoljne permisije za OTC")
		}
		return domain.KindEmployee, nil
	}
	return "", apperr.PermissionDenied("nepoznata vrsta korisnika")
}

// assertSameKindCounterparties implements spec p.79 — clients negotiate
// with clients, supervisors with supervisors. Admin is treated as a
// supervisor (employee kind). Mixed-role offers are rejected with a
// FailedPrecondition (Serbian message); no schema constraint catches
// this.
func assertSameKindCounterparties(buyer auth.Principal, sellerHolding *domain.Holding) error {
	buyerKind := principalUserKind(buyer)
	if buyerKind != sellerHolding.UserKind {
		return apperr.FailedPrecondition("OTC ponuda zahteva istovrsne učesnike (klijent-klijent ili supervizor-supervizor)")
	}
	return nil
}

// principalUserKind maps auth.Principal.UserKind to domain.UserKind.
func principalUserKind(p auth.Principal) domain.UserKind {
	switch p.UserKind {
	case auth.KindClient:
		return domain.KindClient
	case auth.KindEmployee:
		return domain.KindEmployee
	}
	return ""
}

// otherParty returns the (user_id, user_kind) of the offer party that
// isn't `me`. Convenience for notification recipients.
func otherParty(o *domain.OTCOffer, me string) (string, domain.UserKind) {
	if o.BuyerID == me {
		return o.SellerID, o.SellerKind
	}
	return o.BuyerID, o.BuyerKind
}

// validateOTCMoneyFields checks the trio of (qty, price, premium). All
// money fields must be non-negative; qty must be > 0.
func validateOTCMoneyFields(qty int32, price, premium string) error {
	if qty <= 0 {
		return apperr.Validation("količina mora biti pozitivna")
	}
	pr, err := money.Parse(price)
	if err != nil || !money.IsNonNegative(pr) {
		return apperr.Validation("price_per_unit nije validan iznos")
	}
	pm, err := money.Parse(premium)
	if err != nil || !money.IsNonNegative(pm) {
		return apperr.Validation("premium nije validan iznos")
	}
	return nil
}
