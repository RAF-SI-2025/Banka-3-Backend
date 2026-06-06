package service

import (
	"context"
	"fmt"
	"math/big"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
)

// CreatePriceAlertInput is the create surface (todoSpec C3 S26).
type CreatePriceAlertInput struct {
	SecurityID string
	Threshold  string
	Condition  domain.PriceAlertCondition
}

// CreatePriceAlert registers a one-shot price alert for the caller on a
// security. Threshold must be positive and condition recognised (S26).
func (s *Service) CreatePriceAlert(ctx context.Context, in CreatePriceAlertInput) (*domain.PriceAlert, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if in.SecurityID == "" {
		return nil, apperr.Validation("security_id je obavezan")
	}
	if !in.Condition.Valid() {
		return nil, apperr.Validation("uslov mora biti ABOVE ili BELOW")
	}
	threshold, err := money.Parse(in.Threshold)
	if err != nil {
		return nil, apperr.Validation("neispravan prag")
	}
	if !money.IsPositive(threshold) {
		return nil, apperr.Validation("prag mora biti veći od nule")
	}
	// Confirm the security exists so an alert can never reference a
	// dangling id (the sweep would otherwise log a lookup miss forever).
	if _, err := s.Store.GetSecurity(ctx, in.SecurityID); err != nil {
		return nil, err
	}
	return s.Store.InsertPriceAlert(ctx, &domain.PriceAlert{
		UserID:     p.UserID,
		UserKind:   domain.UserKind(p.UserKind),
		SecurityID: in.SecurityID,
		Threshold:  money.FormatAmount(threshold),
		Condition:  in.Condition,
	})
}

// ListPriceAlerts returns the caller's own alerts (active + past).
func (s *Service) ListPriceAlerts(ctx context.Context) ([]*domain.PriceAlert, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	return s.Store.ListPriceAlertsByUser(ctx, p.UserID)
}

// DeletePriceAlert deactivates one of the caller's alerts. Owner-scoped:
// only the owner (or an admin) may retire it. We deactivate rather than
// hard-delete so the row stays auditable.
func (s *Service) DeletePriceAlert(ctx context.Context, id string) error {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return err
	}
	a, err := s.Store.GetPriceAlert(ctx, id)
	if err != nil {
		return err
	}
	if a.UserID != p.UserID && !permissions.Has(p.Permissions, permissions.Admin) {
		return apperr.PermissionDenied("nedovoljne permisije")
	}
	return s.Store.DeactivatePriceAlert(ctx, id, s.now())
}

// RunPriceAlertSweep walks every active alert, checks whether the
// security's current price has crossed the threshold, and for each
// crossing sends a Serbian notification and deactivates the alert
// (todoSpec C3 S27/S28). Alerts that have not crossed stay active and
// silent (S29). Returns the number triggered.
//
// Notification is best-effort: a delivery failure never blocks the
// deactivation (the alert is one-shot regardless). Email is sent only
// when an address is resolvable; on this stack the trading service has
// no user-email resolver wired, so the sweep delivers the in-app
// notification and skips email (see report).
func (s *Service) RunPriceAlertSweep(ctx context.Context) (int, error) {
	alerts, err := s.Store.ListActivePriceAlerts(ctx)
	if err != nil {
		return 0, err
	}
	triggered := 0
	for _, a := range alerts {
		listing, err := s.Store.GetListingBySecurityID(ctx, a.SecurityID)
		if err != nil {
			// No listing (price) for this security yet — leave the alert
			// active and move on.
			s.Log.Warn("price alert: listing lookup failed",
				"alert_id", a.ID, "security_id", a.SecurityID, "err", err.Error())
			continue
		}
		price, err := money.Parse(listing.Price)
		if err != nil {
			s.Log.Warn("price alert: unparseable price",
				"alert_id", a.ID, "price", listing.Price)
			continue
		}
		threshold, err := money.Parse(a.Threshold)
		if err != nil {
			s.Log.Warn("price alert: unparseable threshold",
				"alert_id", a.ID, "threshold", a.Threshold)
			continue
		}
		if !priceAlertCrossed(a.Condition, price, threshold) {
			continue // S29 — not crossed, stays active.
		}
		s.firePriceAlert(ctx, a, listing.Price)
		if err := s.Store.DeactivatePriceAlert(ctx, a.ID, s.now()); err != nil {
			s.Log.Warn("price alert: deactivate failed", "alert_id", a.ID, "err", err.Error())
			continue
		}
		triggered++
	}
	return triggered, nil
}

// priceAlertCrossed reports whether the current price has reached the
// threshold for the alert's condition (ABOVE: price >= threshold;
// BELOW: price <= threshold).
func priceAlertCrossed(cond domain.PriceAlertCondition, price, threshold *big.Rat) bool {
	switch cond {
	case domain.PriceAlertAbove:
		return money.Cmp(price, threshold) >= 0
	case domain.PriceAlertBelow:
		return money.Cmp(price, threshold) <= 0
	}
	return false
}

// firePriceAlert delivers the one-shot notification for a crossed alert.
// Best-effort; never returns an error.
func (s *Service) firePriceAlert(ctx context.Context, a *domain.PriceAlert, currentPrice string) {
	if s.Notifier == nil {
		return
	}
	ticker := a.SecurityID
	if sec, err := s.Store.GetSecurity(ctx, a.SecurityID); err == nil && sec.Ticker != "" {
		ticker = sec.Ticker
	}
	dir := "prešla iznad"
	if a.Condition == domain.PriceAlertBelow {
		dir = "pala ispod"
	}
	title := "Obaveštenje o ceni"
	body := fmt.Sprintf("Cena hartije %s je %s praga %s (trenutna cena: %s).",
		ticker, dir, a.Threshold, currentPrice)
	if err := s.Notifier.InApp(ctx, a.UserID, a.UserKind, "price_alert", title, body); err != nil {
		s.Log.Warn("price alert: in-app notify failed", "alert_id", a.ID, "err", err.Error())
	}
}
