// Package service holds the bank service's business logic.
package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/clock"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/store"
)

// Config carries the bank service knobs that aren't covered by the
// generic infra config.
type Config struct {
	BankCode     string // 3 digits — this bank's prefix in the 18-digit number
	Branch       string // 4 digits — default branch for new accounts
	FXCommission string // "0.005" → 0.5% (default). Empty → default.
	CVVPepper    string // server-side key for HMAC-SHA256 CVV digests
}

type Service struct {
	Store *store.Store
	Cfg   Config
	Log   *slog.Logger
	// Rates is wired by the app layer (gRPC client to exchange). Nil
	// in unit tests that don't exercise FX, in which case FX paths
	// surface "exchange rate provider not configured".
	Rates RateProvider
	// Notifier is used for c2 user-facing notifications (card state
	// changes, loan decisions, missed installments). Nil → events are
	// logged only. Mirrors the user service's Notifier interface.
	Notifier Notifier
	// UserResolver looks up a client's email by ID. Used by Notifier-
	// backed flows so the bank service doesn't have to keep its own
	// copy of the email (cross-schema joins are forbidden).
	UserResolver UserResolver
	// Now is the legacy wall-clock seam — tests still pin it to a
	// deterministic value. Production leaves it nil and now() falls
	// through to Clock, then time.Now as a last resort.
	Now func() time.Time

	// Clock is the QA-adjustable business-time provider (pkg/clock).
	// app/ wires a *clock.Adjustable when CLOCK_DEBUG=true so the
	// gateway debug endpoint can advance time uniformly across
	// services. Nil-safe.
	Clock clock.Clock
}

// now returns the service clock, defaulting to time.Now when no
// override has been wired. Always go through this helper rather than
// calling time.Now directly inside service methods so tests can
// reproduce date-sensitive behaviour deterministically.
func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	if s.Clock != nil {
		return s.Clock.Now()
	}
	return time.Now()
}

// Notifier is the bank service's user-notification surface. The
// signature matches the user service's Notifier so a single email
// adapter can satisfy both.
type Notifier interface {
	Send(ctx context.Context, to, subject, body string, html bool) error
}

// UserResolver resolves cross-schema client/employee details that the
// bank service needs at notification time but doesn't own. The app
// layer wires this to a user-service gRPC client.
type UserResolver interface {
	ClientEmail(ctx context.Context, clientID string) (string, error)
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
