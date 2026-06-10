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
				log.ErrorContext(ctx, "daily job failed", "err", err, "job", name)
			} else {
				log.InfoContext(ctx, "daily job ran", "job", name)
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
				log.ErrorContext(ctx, "monthly tax cron failed", "err", err)
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

// runOptionsRefresh fires the Black-Scholes options generator. Daily
// is fine: the strike grid + expiry ladder are a function of today's
// underlying price and today's date, so re-running more often is just
// noise. The first tick fires immediately so a fresh container has an
// option chain without a full day's wait.
func runOptionsRefresh(ctx context.Context, log *slog.Logger, svc *service.Service, interval time.Duration) error {
	if svc.Options == nil {
		return nil
	}
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	once := func() {
		res, err := svc.Options.RunOnce(ctx)
		if err != nil {
			log.ErrorContext(ctx, "options generator failed", "err", err)
			return
		}
		log.Info("options generator ran",
			"underlyings", res.UnderlyingsProcessed,
			"options", res.OptionsUpserted,
			"skipped", res.Skipped)
	}
	once()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			once()
		}
	}
}

// runMarketDataRefresh fires the upstream-quote refresh once per
// interval. The refresher itself stops cleanly on quota exhaustion,
// so the loop's only job is to call it on a cadence and log the
// summary. The first tick fires immediately so a fresh container
// populates `last_refresh` without a wait.
func runMarketDataRefresh(ctx context.Context, log *slog.Logger, svc *service.Service, interval time.Duration) error {
	if svc.MarketData == nil {
		return nil
	}
	if interval <= 0 {
		interval = time.Hour
	}
	once := func() {
		res, err := svc.MarketData.RunOnce(ctx)
		if err != nil {
			log.ErrorContext(ctx, "market-data refresh failed", "err", err)
			return
		}
		log.Info("market-data refresh ran",
			"stocks", res.StocksUpdated,
			"forex", res.ForexUpdated,
			"skipped", res.Skipped,
			"errors", res.UpstreamErrors,
			"throttled", res.UpstreamThrottled)
	}
	once()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			once()
		}
	}
}

// runStockHistoryBackfill runs the Alpha Vantage daily-history
// backfill exactly once at startup (spec p.40). It is a no-op when no
// AV key is configured (MarketData / History nil), in which case the
// keyless synthetic seed remains the chart's source. A failed pass is
// logged, not fatal — the service still serves whatever history the
// seed planted.
func runStockHistoryBackfill(ctx context.Context, log *slog.Logger, svc *service.Service) error {
	if svc.MarketData == nil || svc.MarketData.History == nil {
		return nil
	}
	res, err := svc.MarketData.BackfillStockHistory(ctx)
	if err != nil {
		// Swallowed by design — the seed history remains usable.
		log.ErrorContext(ctx, "stock-history backfill failed", "err", err)
		return nil
	}
	log.Info("stock-history backfill ran",
		"symbols", res.SymbolsBackfilled,
		"rows", res.RowsWritten,
		"skipped", res.Skipped,
		"errors", res.UpstreamErrors,
		"throttled", res.UpstreamThrottled)
	return nil
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
				log.ErrorContext(ctx, "execution tick failed", "err", err)
				continue
			}
			if fired > 0 {
				log.Info("execution tick", "fired", fired)
			}
		}
	}
}

// runOTCExpirySweep walks active OTC contracts whose settlement_date
// has passed and flips them to `expired` (spec p.69). 5-minute cadence
// per c4-plan; first tick fires immediately so a fresh container
// catches any backlog left from a crashed run. No-op when no contracts
// match.
func runOTCExpirySweep(ctx context.Context, log *slog.Logger, svc *service.Service, interval time.Duration) error {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	once := func() {
		res, err := svc.SweepExpiredOTCContracts(ctx)
		if err != nil {
			log.ErrorContext(ctx, "otc expiry sweep failed", "err", err)
			return
		}
		if res.ContractsExpired > 0 || res.OffersExpired > 0 || res.OffersWarned > 0 {
			log.Info("otc expiry sweep ran",
				"contracts", res.ContractsExpired,
				"shares_released", res.SharesReleased,
				"offers_expired", res.OffersExpired,
				"offers_warned", res.OffersWarned)
		}
	}
	once()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			once()
		}
	}
}

// runSagaRecoveryWorker resumes c4 sagas parked by a transient error
// (or a crashed worker). Every tick it asks the orchestrator's store
// for sagas in `running`/`compensating` whose `next_attempt_at` has
// passed; for each it calls Resume which re-takes the advisory lock,
// re-loads state, and drives the next step (or compensation).
//
// Resume is idempotent — the bank-side RPCs dedupe on op_id, so a
// double-resume after a crash never double-debits. The advisory lock
// is the cross-worker guard so two recovery workers (or a foreground
// + recovery race) can't drive the same saga at the same time.
func runSagaRecoveryWorker(ctx context.Context, log *slog.Logger, svc *service.Service, interval time.Duration) error {
	if svc.SagaOrch == nil {
		log.Warn("saga orchestrator not wired; recovery worker disabled")
		return nil
	}
	if interval <= 0 {
		interval = 30 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	log.Info("saga recovery worker started", "interval", interval)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			// Shares its single-pass body with the RunSagaRecoveryTick RPC
			// so the scheduler service can drive the same logic.
			n, err := svc.RunSagaRecoveryTick(ctx)
			if err != nil {
				log.ErrorContext(ctx, "saga recovery: tick failed", "err", err)
				continue
			}
			if n > 0 {
				log.Info("saga recovery tick", "rows", n)
			}
		}
	}
}

// runFundPerformanceCron writes one snapshot per active fund per day
// at 23:50 Europe/Belgrade. The snapshot captures (liquid_rsd,
// holdings_value_rsd) so the FE chart can render a time series; the
// total_value column is derived by the conversion helper at read time.
func runFundPerformanceCron(ctx context.Context, log *slog.Logger, svc *service.Service, loc *time.Location) error {
	for {
		next := nextDailyOccurrence(time.Now().In(loc), 23, 50)
		wait := time.Until(next)
		log.Info("fund snapshot cron scheduled", "next", next.Format(time.RFC3339))
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil
		case <-t.C:
			n, err := svc.SnapshotAllFunds(ctx, time.Now().In(loc))
			if err != nil {
				log.ErrorContext(ctx, "fund snapshot cron failed", "err", err)
				continue
			}
			log.Info("fund snapshot cron ran", "funds", n)
		}
	}
}
