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

// runMonthlyTaxCron schedules the spec p.62 capital-gains-tax debit at
// 23:55 (Europe/Belgrade) on the last day of every month. We use a
// per-iteration loop instead of cron's day-of-month string because
// "last day" varies across months — easier to compute than to encode.
//
// The fn closure attaches an admin principal to ctx so RunTax's
// requireSupervisor admits the cron without a real user session.
func runMonthlyTaxCron(ctx context.Context, log *slog.Logger, svc *service.Service, loc *time.Location) error {
	for {
		next := nextEndOfMonthAfter(time.Now().In(loc), 23, 55)
		wait := time.Until(next)
		log.Info("monthly tax cron scheduled", "next", next.Format(time.RFC3339))

		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil
		case <-t.C:
			cronCtx := service.TaxCronContext(ctx)
			res, err := svc.RunTax(cronCtx, service.RunTaxInput{})
			if err != nil {
				log.Warn("monthly tax cron failed", "err", err.Error())
				continue
			}
			log.Info("monthly tax cron ran",
				"users_taxed", res.UsersTaxed,
				"total_rsd", res.TotalCollectedRSD)
		}
	}
}

// nextEndOfMonthAfter returns the next instant strictly after `now`
// whose wall-clock is the last day of a month at hour:minute. Lives in
// app/ rather than service/ because it's purely a scheduling helper —
// the service's own copy is exported for unit tests.
func nextEndOfMonthAfter(now time.Time, hour, minute int) time.Time {
	loc := now.Location()
	year, month, _ := now.Date()
	candidate := time.Date(year, month+1, 0, hour, minute, 0, 0, loc)
	if !candidate.After(now) {
		candidate = time.Date(year, month+2, 0, hour, minute, 0, 0, loc)
	}
	return candidate
}

// runExecutionWorker is the spec p.55-56 partial-fill loop. It wakes
// up every interval and asks the service to walk every active order;
// the service decides per-order whether to fire one fill on this tick
// based on price/limit/stop conditions and the cadence formula.
//
// We don't drive timing per-order with goroutines — a single ticker
// keeps the model simple and easy to reason about under restart. The
// service-side cadence math reads listing.volume + remaining_quantity
// to roll a random interval and compares it against time-since-last-
// fill, so the wall-clock interval each order experiences is correct
// even with a coarse 10-second tick.
func runExecutionWorker(ctx context.Context, log *slog.Logger, svc *service.Service, interval time.Duration) error {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	log.Info("execution worker started", "interval", interval)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			fired, err := svc.RunExecutionTick(ctx)
			if err != nil {
				log.Warn("execution tick failed", "err", err.Error())
				continue
			}
			if fired > 0 {
				log.Info("execution tick", "fired", fired)
			}
		}
	}
}
