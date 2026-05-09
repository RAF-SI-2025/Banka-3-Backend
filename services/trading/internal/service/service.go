// Package service holds the trading service's business logic.
package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/store"
)

// Config carries trading-service knobs not covered by infra config.
type Config struct {
	// Belgrade is the wall-clock timezone used to anchor "after-hours"
	// computations and the daily limit-reset cron. Defaults to
	// Europe/Belgrade.
	Belgrade *time.Location

	// FXCommission is the menjačnica fee rate as a decimal string
	// ("0.005" = 0.5%). Used when bank-side conversions of trade-RSD
	// equivalents go through the menjačnica formula. The trading
	// service does not collect it directly — the bank service does on
	// the FX leg — but we mirror the value so unit tests pin behaviour.
	FXCommission string
}

// Service is the trading aggregate. Sub-aggregates are split per file
// (actuaries, exchanges, securities, listings, orders, portfolio,
// tax) but share this struct so cross-aggregate methods (e.g. order
// approval bumping the actuary's used_limit) stay in-package.
type Service struct {
	Store *store.Store
	Cfg   Config
	Log   *slog.Logger
	// Now is the wall-clock used by every time-dependent path. Tests
	// pin it; production leaves it nil and falls through to time.Now.
	Now func() time.Time
}

// New constructs a Service with sane defaults. The app layer fills in
// gRPC clients and other dependencies via direct field assignment.
func New(st *store.Store, cfg Config, log *slog.Logger) *Service {
	if cfg.Belgrade == nil {
		loc, err := time.LoadLocation("Europe/Belgrade")
		if err != nil {
			loc = time.UTC
		}
		cfg.Belgrade = loc
	}
	return &Service{Store: st, Cfg: cfg, Log: log}
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// requirePrincipal returns the request's authenticated principal or
// Unauthenticated.
func (s *Service) requirePrincipal(ctx context.Context) (auth.Principal, error) {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return auth.Principal{}, apperr.Unauthenticated("not authenticated")
	}
	return p, nil
}

// requirePermission errors unless principal has perm or admin.
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

// requireSupervisor errors unless principal is admin or actuary
// supervisor. Spec p.38: every admin is implicitly a supervisor.
func (s *Service) requireSupervisor(ctx context.Context) (auth.Principal, error) {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return auth.Principal{}, apperr.Unauthenticated("not authenticated")
	}
	if permissions.HasAny(p.Permissions, permissions.Admin, permissions.ActuarySupervisor) {
		return p, nil
	}
	return auth.Principal{}, apperr.PermissionDenied("nedovoljne permisije")
}
