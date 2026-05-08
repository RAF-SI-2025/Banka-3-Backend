// Package service is the user service's business logic. It depends on
// store for persistence and Notifier for email delivery; both are
// injected.
package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/clock"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/store"
)

// Notifier sends an email message. Wired by the app to either a
// notification gRPC client or pkg/email directly.
type Notifier interface {
	Send(ctx context.Context, to, subject, body string, html bool) error
}

// Config bundles the time-and-secret knobs the service needs.
type Config struct {
	JWTSigningKey []byte
	AccessTTL     time.Duration // default 15m
	RefreshTTL    time.Duration // default 168h (7 days)
	ActivationTTL time.Duration // default 24h
	ResetTTL      time.Duration // default 15m (per spec)

	// WebBaseURL is the public URL of the SPA used in email links.
	WebBaseURL string
}

// Service is the union of all c1 user-service operations. Methods are
// split across files in this package by feature area.
type Service struct {
	Store    *store.Store
	Notifier Notifier
	Cfg      Config
	Log      *slog.Logger
	Clock    clock.Clock
}

// New returns a Service with sensible defaults applied to cfg.
func New(s *store.Store, n Notifier, cfg Config, log *slog.Logger) *Service {
	if cfg.AccessTTL == 0 {
		cfg.AccessTTL = 15 * time.Minute
	}
	if cfg.RefreshTTL == 0 {
		cfg.RefreshTTL = 7 * 24 * time.Hour
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
		Cfg:      cfg,
		Log:      log,
		Clock:    clock.Real{},
	}
}
