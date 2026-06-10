// Package bizmetric exposes a tiny set of business-level OTel counters
// emitted from the service layer. They feed the "Banka — Business"
// Grafana dashboard, which is the user-facing answer to "is the bank
// actually banking?" — orthogonal to infra RED metrics.
//
// Instruments are created lazily on first use so the package is safe
// to import from anywhere without an init-order dependency on
// otelinit.Init. Before the global MeterProvider is set, the no-op
// meter returns no-op instruments and Add() calls are silently
// dropped; the first Add() after Init binds against the real provider.
//
// Cardinality is bounded by design: the attribute setters below accept
// only a small, finite set of values (apperr Kind names, currency
// codes, order sides, security types). Callers should pass through
// `apperr.Kind.String()` for failure-status labels and never user-
// controlled input.
package bizmetric

import (
	"context"
	"log/slog"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var (
	once     sync.Once
	payments metric.Int64Counter
	trades   metric.Int64Counter
	logins   metric.Int64Counter
)

func ensure() {
	once.Do(func() {
		m := otel.Meter("github.com/RAF-SI-2025/Banka-3-Backend/pkg/bizmetric")
		var err error
		payments, err = m.Int64Counter(
			"banka_payments_total",
			metric.WithDescription("Number of payment/transfer operations the bank service processed."),
		)
		if err != nil {
			slog.Warn("bizmetric counter registration failed", "err", err, "metric", "banka_payments_total")
		}
		trades, err = m.Int64Counter(
			"banka_trades_total",
			metric.WithDescription("Number of securities trade fills settled by the trading service."),
		)
		if err != nil {
			slog.Warn("bizmetric counter registration failed", "err", err, "metric", "banka_trades_total")
		}
		logins, err = m.Int64Counter(
			"banka_user_logins_total",
			metric.WithDescription("Number of login attempts handled by the user service, by outcome."),
		)
		if err != nil {
			slog.Warn("bizmetric counter registration failed", "err", err, "metric", "banka_user_logins_total")
		}
	})
}

// PaymentCompleted increments banka_payments_total. `kind` is one of
// "payment", "transfer", "exchange". `currency` is the from-side
// currency code (3-letter ISO, or "unknown" if not yet resolved on a
// failure path). `status` is "ok" or an apperr.Kind name.
func PaymentCompleted(ctx context.Context, kind, currency, status string) {
	ensure()
	if payments == nil {
		return
	}
	payments.Add(ctx, 1, metric.WithAttributes(
		attribute.String("kind", kind),
		attribute.String("currency", currency),
		attribute.String("status", status),
	))
}

// TradeCompleted increments banka_trades_total. `side` is "buy" or
// "sell". `securityType` is the domain.Security.Type string (stock,
// bond, future, option, forex, fund). `status` is "ok" or a stable
// failure identifier ("settle_failed", "bank_failed").
func TradeCompleted(ctx context.Context, side, securityType, status string) {
	ensure()
	if trades == nil {
		return
	}
	trades.Add(ctx, 1, metric.WithAttributes(
		attribute.String("side", side),
		attribute.String("security_type", securityType),
		attribute.String("status", status),
	))
}

// UserLogin increments banka_user_logins_total. `result` is one of
// "ok", "bad_password", "not_found", "disabled", "not_activated",
// "validation", "internal".
func UserLogin(ctx context.Context, result string) {
	ensure()
	if logins == nil {
		return
	}
	logins.Add(ctx, 1, metric.WithAttributes(
		attribute.String("result", result),
	))
}
