// Package service holds the exchange service's business logic.
package service

import (
	"context"
	"log/slog"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/exchange/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/exchange/internal/store"
)

// RateRefresher does one fetch+upsert pass of the external FX feed.
// Implemented by *feed.Feeder; injected by the app layer so the service
// can expose the refresh as an RPC without importing the feed package.
type RateRefresher interface {
	Once(ctx context.Context) (int, error)
}

// Service is the FX rates aggregate. Slice 1 covers ingestion + lookup;
// later c2 slices add a quote-with-commission helper for menjačnica.
type Service struct {
	Store *store.Store
	Log   *slog.Logger
	// Refresher is the external FX feed pass. Nil on a stack without a
	// feed configured; RefreshRates then returns FailedPrecondition.
	Refresher RateRefresher
}

func New(st *store.Store, log *slog.Logger) *Service {
	return &Service{Store: st, Log: log}
}

// RefreshRates triggers one pass of the external FX feed and returns the
// number of rate rows written. Normally the scheduler service drives
// this; ExchangeWrite/admin-gated so ops can fire it on demand.
func (s *Service) RefreshRates(ctx context.Context) (int, error) {
	if err := s.requirePermission(ctx, permissions.ExchangeWrite); err != nil {
		return 0, err
	}
	if s.Refresher == nil {
		return 0, apperr.FailedPrecondition("fx feed not configured")
	}
	return s.Refresher.Once(ctx)
}

// UpsertRate writes a single rate. Permission: ExchangeWrite.
func (s *Service) UpsertRate(ctx context.Context, r *domain.Rate) (*domain.Rate, error) {
	if err := s.requirePermission(ctx, permissions.ExchangeWrite); err != nil {
		return nil, err
	}
	if err := validateRate(r); err != nil {
		return nil, err
	}
	return s.Store.UpsertRate(ctx, r)
}

// ListRates returns rates, optionally filtered. Authenticated callers
// only — no specific permission required (clients see the menjačnica
// board).
func (s *Service) ListRates(ctx context.Context, from domain.Currency) ([]*domain.Rate, error) {
	if _, ok := auth.PrincipalFrom(ctx); !ok {
		return nil, apperr.Unauthenticated("not authenticated")
	}
	if from != "" && !from.Supported() {
		return nil, apperr.Validation("unsupported currency: " + string(from))
	}
	return s.Store.ListRates(ctx, from)
}

// Quote returns the most recent rate for from→to. Internal: callers
// are other services. Returns NotFound if the pair is missing — there
// is no fallback derivation (e.g. EUR→USD is not derived from
// EUR→RSD and RSD→USD here; the bank service composes the two-leg
// conversion explicitly).
func (s *Service) Quote(ctx context.Context, from, to domain.Currency) (*domain.Rate, error) {
	if !from.Supported() || !to.Supported() {
		return nil, apperr.Validation("unsupported currency")
	}
	if from == to {
		return nil, apperr.Validation("from and to must differ")
	}
	return s.Store.GetRate(ctx, from, to)
}

func (s *Service) requirePermission(ctx context.Context, perm string) error {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return apperr.Unauthenticated("not authenticated")
	}
	if permissions.Has(p.Permissions, perm) || permissions.Has(p.Permissions, permissions.Admin) {
		return nil
	}
	return apperr.PermissionDenied("nedovoljne permisije")
}

func validateRate(r *domain.Rate) error {
	if !r.From.Supported() {
		return apperr.Validation("unsupported from-currency")
	}
	if !r.To.Supported() {
		return apperr.Validation("unsupported to-currency")
	}
	if r.From == r.To {
		return apperr.Validation("from and to must differ")
	}
	if r.Bid == "" || r.Ask == "" {
		return apperr.Validation("bid and ask are required")
	}
	// Numeric validity is enforced by the DB column type + check
	// constraints (positive, ask ≥ bid). We could parse here too, but
	// duplicating the check would just risk drift.
	return nil
}
