package service

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/card"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/cvv"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
)

// cardLifetime is the default expiry — 4 years from creation. Spec
// p.28 leaves it unspecified; we follow industry default.
const cardLifetime = 4 * 365 * 24 * time.Hour

// CreateCardInput is the validated payload for an on-demand card
// creation. The "create card" checkbox during account creation will
// call CreateCard separately.
type CreateCardInput struct {
	AccountID          string
	AuthorizedPersonID string
	Brand              domain.CardBrand
	Name               string
	CardLimit          string
}

// CreateCard mints a new debit card. Spec p.27-29 limits:
//   - lični: max 2 active cards per account
//   - poslovni: max 1 per OvlascenoLice (or per the company-owner self-card)
//
// Returns the newly created card — the CVV is stored hashed; we
// surface it once via the result's Number+ generated CVV side-channel
// in CreateCardResult (the FE displays it once at create time).
func (s *Service) CreateCard(ctx context.Context, in CreateCardInput) (*domain.Card, string, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, "", err
	}

	a, err := s.Store.GetAccountByID(ctx, in.AccountID)
	if err != nil {
		return nil, "", err
	}
	// Permission: clients can request cards for their own accounts;
	// employees with card.write can act on any.
	if p.UserKind == auth.KindClient {
		if a.OwnerClientID != p.UserID {
			return nil, "", apperr.PermissionDenied("nedovoljne permisije")
		}
	} else if err := s.requirePermission(ctx, permissions.CardWrite); err != nil {
		return nil, "", err
	}

	if err := s.enforceCardLimits(ctx, a, in.AuthorizedPersonID); err != nil {
		return nil, "", err
	}

	brand := in.Brand
	if brand == "" {
		brand = domain.BrandVisa
	}
	number, cvvPlain, err := generateCardCredentials(brand)
	if err != nil {
		return nil, "", err
	}
	cvvHash, err := cvv.Hash(cvvPlain, s.Cfg.CVVPepper)
	if err != nil {
		return nil, "", apperr.Internal("hash cvv", err)
	}

	limit := strings.TrimSpace(in.CardLimit)
	if limit == "" {
		return nil, "", apperr.Validation("limit kartice je obavezan i mora biti veći od 0")
	}
	limitRat, err := money.Parse(limit)
	if err != nil {
		return nil, "", apperr.Validation("limit kartice nije validan iznos")
	}
	if !money.IsPositive(limitRat) {
		return nil, "", apperr.Validation("limit kartice mora biti veći od 0")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = "Debit"
	}

	c, err := s.Store.CreateCard(ctx, &domain.Card{
		Number:             number,
		CVVHash:             cvvHash,
		Brand:              brand,
		Name:               name,
		AccountID:          a.ID,
		AuthorizedPersonID: strings.TrimSpace(in.AuthorizedPersonID),
		CardLimit:          limit,
		ExpiresAt:          s.now().Add(cardLifetime),
		Status:             domain.CardActive,
	})
	if err != nil {
		return nil, "", err
	}
	return c, cvvPlain, nil
}

// enforceCardLimits applies spec p.27. For poslovni accounts, the
// caller must specify either an authorized_person_id OR be the
// account-owner client requesting their own card; the limit is one
// active card per "person" (owner or each OvlascenoLice).
func (s *Service) enforceCardLimits(ctx context.Context, a *domain.Account, authorizedPersonID string) error {
	if a.Kind.IsPersonal() {
		if authorizedPersonID != "" {
			return apperr.Validation("personal accounts have no authorized persons")
		}
		n, err := s.Store.CountActiveCards(ctx, a.ID, "")
		if err != nil {
			return err
		}
		if n >= 2 {
			return apperr.FailedPrecondition("maksimalno 2 kartice po ličnom računu")
		}
		return nil
	}
	if !a.Kind.IsBusiness() {
		return apperr.Validation("kartice se ne kreiraju za sistemske račune")
	}
	// Business — verify OvlascenoLice (if given) belongs to the
	// account's company.
	if authorizedPersonID != "" {
		ap, err := s.Store.GetAuthorizedPersonByID(ctx, authorizedPersonID)
		if err != nil {
			return err
		}
		if ap.CompanyID != a.CompanyID {
			return apperr.Validation("ovlašćeno lice ne pripada firmi računa")
		}
	}
	n, err := s.Store.CountActiveCards(ctx, a.ID, authorizedPersonID)
	if err != nil {
		return err
	}
	if n >= 1 {
		who := "vlasnik"
		if authorizedPersonID != "" {
			who = "ovlašćeno lice"
		}
		return apperr.FailedPrecondition(fmt.Sprintf("maksimalno 1 kartica po osobi (%s) na poslovnom računu", who))
	}
	return nil
}

func (s *Service) ListCards(ctx context.Context, accountID string) ([]*domain.Card, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}

	var cards []*domain.Card
	if accountID == "" {
		if p.UserKind == auth.KindClient {
			cards, err = s.Store.ListCardsByOwner(ctx, p.UserID)
		} else {
			if !permissions.HasAny(p.Permissions, permissions.CardRead, permissions.Admin) {
				return nil, apperr.PermissionDenied("nedovoljne permisije")
			}
			cards, err = s.Store.ListAllCards(ctx)
		}
	} else {
		a, aerr := s.Store.GetAccountByID(ctx, accountID)
		if aerr != nil {
			return nil, aerr
		}
		if p.UserKind == auth.KindClient {
			if a.OwnerClientID != p.UserID {
				return nil, apperr.PermissionDenied("nedovoljne permisije")
			}
		} else if !permissions.HasAny(p.Permissions, permissions.CardRead, permissions.Admin) {
			return nil, apperr.PermissionDenied("nedovoljne permisije")
		}
		cards, err = s.Store.ListCardsByAccount(ctx, accountID)
	}
	if err != nil {
		return nil, err
	}
	// Mask card number when caller is a client. Employees with CardWrite
	// see the full number for support purposes.
	if p.UserKind == auth.KindClient {
		for _, c := range cards {
			c.Number = card.Mask(c.Number)
		}
	}
	return cards, nil
}

// SetCardStatus toggles active/blocked/deactivated. Spec p.29:
//   - clients can BLOCK their own cards; only employees can unblock.
//   - deactivation is a one-way employee action (irreversible).
func (s *Service) SetCardStatus(ctx context.Context, id string, status domain.CardStatus) (*domain.Card, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	target, err := s.Store.GetCardByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if target.Status == domain.CardDeactivated {
		return nil, apperr.FailedPrecondition("deaktivirana kartica se ne može menjati")
	}

	if p.UserKind == auth.KindClient {
		// Verify ownership via account.
		a, err := s.Store.GetAccountByID(ctx, target.AccountID)
		if err != nil {
			return nil, err
		}
		if a.OwnerClientID != p.UserID {
			return nil, apperr.PermissionDenied("nedovoljne permisije")
		}
		// Clients can only request blocking.
		if status != domain.CardBlocked {
			return nil, apperr.PermissionDenied("klijent može samo da blokira karticu; deblokiranje obavlja zaposleni")
		}
	} else if err := s.requirePermission(ctx, permissions.CardWrite); err != nil {
		return nil, err
	}

	switch status {
	case domain.CardActive, domain.CardBlocked, domain.CardDeactivated:
	default:
		return nil, apperr.Validation("invalid card status")
	}
	oldStatus := target.Status
	updated, err := s.Store.SetCardStatus(ctx, id, status)
	if err != nil {
		return nil, err
	}
	// Notify the card's owning client. The card itself doesn't carry
	// owner_client_id; resolve via the account.
	if a, err := s.Store.GetAccountByID(ctx, updated.AccountID); err == nil {
		s.notifyCardStatusChanged(ctx, updated, oldStatus, a.OwnerClientID)
	}
	return updated, nil
}

// UpdateCardLimit changes the per-card spending cap. flow.pdf P6
// "Klijent menja limit kartice". Clients can change the limit on
// their own cards; employees with CardWrite can change any. The
// gateway middleware adds the verifikacioni-kod gate (spec p.11).
func (s *Service) UpdateCardLimit(ctx context.Context, id, newLimit string) (*domain.Card, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	target, err := s.Store.GetCardByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if target.Status == domain.CardDeactivated {
		return nil, apperr.FailedPrecondition("deaktivirana kartica se ne može menjati")
	}
	if p.UserKind == auth.KindClient {
		a, err := s.Store.GetAccountByID(ctx, target.AccountID)
		if err != nil {
			return nil, err
		}
		if a.OwnerClientID != p.UserID {
			return nil, apperr.PermissionDenied("nedovoljne permisije")
		}
	} else if err := s.requirePermission(ctx, permissions.CardWrite); err != nil {
		return nil, err
	}

	limit := strings.TrimSpace(newLimit)
	if limit == "" {
		return nil, apperr.Validation("limit kartice je obavezan i mora biti veći od 0")
	}
	rat, err := money.Parse(limit)
	if err != nil {
		return nil, apperr.Validation("limit kartice nije validan iznos")
	}
	if !money.IsPositive(rat) {
		return nil, apperr.Validation("limit kartice mora biti veći od 0")
	}
	return s.Store.UpdateCardLimit(ctx, id, limit)
}

// generateCardCredentials returns a Luhn-clean number for brand and a
// 3-digit CVV.
func generateCardCredentials(brand domain.CardBrand) (string, string, error) {
	number, err := card.Generate(card.Brand(brand))
	if err != nil {
		return "", "", apperr.Internal("generate card", err)
	}
	v, err := rand.Int(rand.Reader, big.NewInt(1000))
	if err != nil {
		return "", "", apperr.Internal("generate cvv", err)
	}
	return number, fmt.Sprintf("%03d", v.Int64()), nil
}
