// Package service holds the bank service's business logic.
package service

import (
	"context"
	"log/slog"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/store"
)

// Config carries the bank service knobs that aren't covered by the
// generic infra config.
type Config struct {
	BankCode string // 3 digits — this bank's prefix in the 18-digit number
	Branch   string // 4 digits — default branch for new accounts
}

type Service struct {
	Store *store.Store
	Cfg   Config
	Log   *slog.Logger
}

func New(st *store.Store, cfg Config, log *slog.Logger) *Service {
	return &Service{Store: st, Cfg: cfg, Log: log}
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

// requirePrincipal returns the authenticated principal or an
// Unauthenticated error.
func (s *Service) requirePrincipal(ctx context.Context) (auth.Principal, error) {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return auth.Principal{}, apperr.Unauthenticated("not authenticated")
	}
	return p, nil
}
