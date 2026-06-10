package service

import (
	"context"
	"strings"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
)

// CreateWatchlist creates a named watchlist for the caller (todoSpec C3
// S36). A user may have any number of named lists. Name is required and
// trimmed.
func (s *Service) CreateWatchlist(ctx context.Context, name string) (*domain.Watchlist, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, apperr.Validation("naziv liste je obavezan")
	}
	w, err := s.Store.CreateWatchlist(ctx, &domain.Watchlist{
		UserID:   p.UserID,
		UserKind: domain.UserKind(p.UserKind),
		Name:     name,
	})
	if err != nil {
		return nil, err
	}
	w.Items = []*domain.WatchlistItem{}
	return w, nil
}

// ListWatchlists returns the caller's own watchlists, each hydrated with
// its items decorated with security + current-price data (S35/S39).
func (s *Service) ListWatchlists(ctx context.Context) ([]*domain.Watchlist, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	lists, err := s.Store.ListWatchlists(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	for _, w := range lists {
		items, err := s.Store.ListItems(ctx, w.ID)
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			s.decorateWatchlistItem(ctx, it)
		}
		w.Items = items
	}
	return lists, nil
}

// DeleteWatchlist removes one of the caller's watchlists (owner-scoped;
// admin may also delete). Items cascade-delete.
func (s *Service) DeleteWatchlist(ctx context.Context, id string) error {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return err
	}
	w, err := s.Store.GetWatchlist(ctx, id)
	if err != nil {
		return err
	}
	if w.UserID != p.UserID && !permissions.Has(p.Permissions, permissions.Admin) {
		return apperr.PermissionDenied("nedovoljne permisije")
	}
	return s.Store.DeleteWatchlist(ctx, id)
}

// AddToWatchlist adds a security to one of the caller's watchlists (S35).
// The watchlist must belong to the caller; the security must exist. The
// returned item is decorated with security + current-price data so the
// FE can render it immediately. Re-adding an existing security is a
// no-op (the store upserts on the unique constraint).
func (s *Service) AddToWatchlist(ctx context.Context, watchlistID, securityID string) (*domain.WatchlistItem, error) {
	if err := s.ownWatchlist(ctx, watchlistID); err != nil {
		return nil, err
	}
	if securityID == "" {
		return nil, apperr.Validation("security_id je obavezan")
	}
	// Confirm the security exists so a watchlist can never reference a
	// dangling id.
	if _, err := s.Store.GetSecurity(ctx, securityID); err != nil {
		return nil, err
	}
	it, err := s.Store.AddItem(ctx, watchlistID, securityID)
	if err != nil {
		return nil, err
	}
	s.decorateWatchlistItem(ctx, it)
	return it, nil
}

// RemoveFromWatchlist removes a security from one of the caller's
// watchlists (S37). Owner-scoped; idempotent.
func (s *Service) RemoveFromWatchlist(ctx context.Context, watchlistID, securityID string) error {
	if err := s.ownWatchlist(ctx, watchlistID); err != nil {
		return err
	}
	if securityID == "" {
		return apperr.Validation("security_id je obavezan")
	}
	return s.Store.RemoveItem(ctx, watchlistID, securityID)
}

// ownWatchlist loads the watchlist and asserts the caller owns it (or is
// an admin). Returns the typed error from the principal/lookup chain.
func (s *Service) ownWatchlist(ctx context.Context, watchlistID string) error {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return err
	}
	w, err := s.Store.GetWatchlist(ctx, watchlistID)
	if err != nil {
		return err
	}
	if w.UserID != p.UserID && !permissions.Has(p.Permissions, permissions.Admin) {
		return apperr.PermissionDenied("nedovoljne permisije")
	}
	return nil
}

// decorateWatchlistItem hydrates an item with security metadata (ticker,
// name, type, currency — S39 filter) and the listing's current price +
// daily change (S35 header row). Best-effort: a missing security or
// listing leaves the corresponding fields empty rather than erroring.
func (s *Service) decorateWatchlistItem(ctx context.Context, it *domain.WatchlistItem) {
	if it == nil {
		return
	}
	if sec, err := s.Store.GetSecurity(ctx, it.SecurityID); err == nil && sec != nil {
		it.Ticker = sec.Ticker
		it.Name = sec.Name
		it.SecurityType = sec.Type
		it.Currency = sec.Currency
	} else if err != nil {
		s.log().WarnContext(ctx, "watchlist item security lookup failed",
			"err", err, "item_id", it.ID, "security_id", it.SecurityID)
	}
	if listing, err := s.Store.GetListingBySecurityID(ctx, it.SecurityID); err == nil && listing != nil {
		it.Price = listing.Price
		it.DailyChange = listing.ChangeAmt
	} else if err != nil {
		s.log().WarnContext(ctx, "watchlist item listing lookup failed",
			"err", err, "item_id", it.ID, "security_id", it.SecurityID)
	}
}
