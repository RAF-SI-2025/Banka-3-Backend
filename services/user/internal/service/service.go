// Package service is the user service's business logic. It depends on
// store for persistence and Notifier for email delivery; both are
// injected.
package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/clock"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/store"
)

// Notifier sends an email message. Wired by the app to either a
// notification gRPC client or pkg/email directly.
type Notifier interface {
	Send(ctx context.Context, to, subject, body string, html bool) error
}

// FundReassigner is the supervisor-demotion hook into the trading
// service. When SetEmployeePermissions revokes funds.manage.supervisor,
// user-svc calls Reassign(from=demoted_user, to=acting_admin) before
// persisting the permission write, so no fund is ever left orphaned.
// Wired by the app to the trading gRPC client; may be nil on a minimal
// dev stack (no trading container running). In that case the cascade
// is skipped with a warning — fine for user-only tests.
type FundReassigner interface {
	Reassign(ctx context.Context, fromUserID, toUserID string) (int64, error)
}

// Config bundles the time-and-secret knobs the service needs.
type Config struct {
	JWTSigningKey []byte
	AccessTTL     time.Duration // default 15m
	RefreshTTL    time.Duration // default 168h (7 days)
	// MobileRefreshTTL is the refresh-token lifetime for mobile logins
	// (LoginOption LongLived). Spec p.84 — the mobile app has no
	// session interval; default ~1y so it effectively never expires in
	// the dev sim while still being a real, revocable server-side TTL.
	MobileRefreshTTL time.Duration // default 8760h (365 days)
	ActivationTTL    time.Duration // default 24h
	ResetTTL         time.Duration // default 15m (per spec)

	// WebBaseURL is the public URL of the SPA used in email links.
	WebBaseURL string
}

// Service is the union of all c1 user-service operations. Methods are
// split across files in this package by feature area.
type Service struct {
	Store    *store.Store
	Notifier Notifier
	Redis    *redis.Client
	Cfg      Config
	Log      *slog.Logger
	Clock    clock.Clock
	// FundReassigner is the trading-service hook for the supervisor-
	// demotion cascade. Optional.
	FundReassigner FundReassigner
}

// New returns a Service with sensible defaults applied to cfg.
func New(s *store.Store, n Notifier, r *redis.Client, cfg Config, log *slog.Logger) *Service {
	if cfg.AccessTTL == 0 {
		cfg.AccessTTL = 15 * time.Minute
	}
	if cfg.RefreshTTL == 0 {
		cfg.RefreshTTL = 7 * 24 * time.Hour
	}
	if cfg.MobileRefreshTTL == 0 {
		cfg.MobileRefreshTTL = 365 * 24 * time.Hour
	}
	if cfg.ActivationTTL == 0 {
		cfg.ActivationTTL = 24 * time.Hour
	}
	if cfg.ResetTTL == 0 {
		cfg.ResetTTL = 15 * time.Minute
	}
	return &Service{
		Store:    s,
		Notifier: n,
		Redis:    r,
		Cfg:      cfg,
		Log:      log,
		Clock:    clock.Real{},
	}
}
