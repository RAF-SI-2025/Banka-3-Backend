package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/service"
)

// runDailyAt fires fn once per day at hour:minute in `loc`, until ctx
// is cancelled. The first fire is the next occurrence of that wall-
// clock; if we're already past it today, we wait until tomorrow.
//
// Spec p.38: agent used_limit resets at 23:59 in Europe/Belgrade.
// We use this loop instead of a generic interval ticker so the reset
// happens on the right wall-clock moment regardless of when the
// service was started.
func runDailyAt(ctx context.Context, log *slog.Logger, name string, loc *time.Location, hour, minute int, fn func(context.Context) error) error {
	for {
		next := nextDailyOccurrence(time.Now().In(loc), hour, minute)
		wait := time.Until(next)
		log.Info("daily job scheduled", "job", name, "next", next.Format(time.RFC3339))

		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil
		case <-t.C:
			if err := fn(ctx); err != nil {
				log.Warn("daily job failed", "job", name, "error", err)
			} else {
				log.Info("daily job ran", "job", name)
			}
		}
	}
}

// nextDailyOccurrence returns the next instant strictly after `now`
// where the local wall clock equals h:m.
func nextDailyOccurrence(now time.Time, h, m int) time.Time {
	loc := now.Location()
	candidate := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, loc)
	if !candidate.After(now) {
		candidate = candidate.AddDate(0, 0, 1)
	}
	return candidate
}

// runActuaryDailyReset is the bound-form fn passed to runDailyAt. It
// calls Service.RunDailyResetActuaries with a context that has no
// principal so the service treats the call as cron-internal.
func runActuaryDailyReset(svc *service.Service) func(context.Context) error {
	return func(ctx context.Context) error {
		_, err := svc.RunDailyResetActuaries(ctx)
		return err
	}
}
