package service

import (
	"context"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/store"
)

// UpsertListing admin-creates/updates a listing. Validation: the
// referenced security must exist (the FK enforces it; we surface a
// nicer error). For options no listing is needed, but we don't block
// admins from inserting one for completeness.
func (s *Service) UpsertListing(ctx context.Context, in *domain.Listing) (*domain.Listing, error) {
	if err := s.requirePermission(ctx, permissions.Admin); err != nil {
		return nil, err
	}
	if in.SecurityID == "" {
		return nil, apperr.Validation("security_id is required")
	}
	if in.Price == "" || in.Ask == "" || in.Bid == "" {
		return nil, apperr.Validation("price, ask, and bid are required")
	}
	if in.ContractSize == "" {
		in.ContractSize = "1"
	}
	if in.ChangeAmt == "" {
		in.ChangeAmt = "0"
	}
	if _, err := s.Store.GetSecurity(ctx, in.SecurityID); err != nil {
		return nil, err
	}
	return s.Store.UpsertListing(ctx, in)
}

// GetListing returns one listing.
func (s *Service) GetListing(ctx context.Context, id string) (*domain.Listing, error) {
	if _, err := s.requirePrincipal(ctx); err != nil {
		return nil, err
	}
	return s.Store.GetListing(ctx, id)
}

// ListListings returns the catalog rows for the FE's portal.
func (s *Service) ListListings(ctx context.Context, in store.ListingFilter, page, pageSize int) ([]*SecurityWithListing, int64, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, 0, err
	}
	if p.UserKind == "client" {
		// Spec p.58 — clients can't see forex pairs (and there are no
		// option listings). Block forex at the filter level.
		switch in.Type {
		case domain.SecurityForex, domain.SecurityOption:
			return []*SecurityWithListing{}, 0, nil
		}
	}
	rows, total, err := s.Store.ListListings(ctx, in, page, pageSize)
	if err != nil {
		return nil, 0, err
	}
	out := make([]*SecurityWithListing, 0, len(rows))
	for _, r := range rows {
		// Filter out forex/option for client at row time too in case
		// the type filter was empty.
		if p.UserKind == "client" {
			switch r.Security.Type {
			case domain.SecurityForex, domain.SecurityOption:
				continue
			}
		}
		out = append(out, decorateSecurity(r.Security, r.Listing))
	}
	return out, total, nil
}

// GetListingDailyHistory returns a window of historical rows.
func (s *Service) GetListingDailyHistory(ctx context.Context, listingID string, from, to time.Time) ([]*domain.ListingDailyPrice, error) {
	if _, err := s.requirePrincipal(ctx); err != nil {
		return nil, err
	}
	return s.Store.GetListingDailyHistory(ctx, listingID, from, to)
}
