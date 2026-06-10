package app

import (
	"context"
	"time"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/bank/v1"
	exchangepb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/exchange/v1"
	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/trading/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/config"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"google.golang.org/protobuf/types/known/emptypb"
)

// adminSentinel is the internal admin principal the scheduler presents
// on every trigger call so each target service's requireAdmin /
// cron-internal check admits it. Same sentinel UUID the trading→bank
// service-to-service calls already use.
const adminSentinel = "00000000-0000-0000-0000-00000000fffe"

func (a *App) adminCtx(ctx context.Context) context.Context {
	return auth.AttachToOutgoing(ctx, auth.Principal{
		UserID:      adminSentinel,
		UserKind:    auth.KindEmployee,
		Permissions: []string{permissions.Admin},
	})
}

// job is a single scheduled unit: run blocks until ctx is cancelled
// (shutdown or lost leadership), firing its trigger on its own cadence.
type job struct {
	name string
	run  func(ctx context.Context)
}

// buildJobs assembles the registry from whichever service clients are
// wired. A nil client (its *_GRPC_ADDR unset) drops that domain's jobs.
func (a *App) buildJobs() []job {
	var jobs []job
	if a.bank != nil {
		jobs = append(
			jobs,
			a.interval("bank-installments", config.Duration("INSTALLMENT_JOB_INTERVAL", 24*time.Hour), false, a.bankInstallments),
			a.interval("bank-variable-rate", config.Duration("VARIABLE_RATE_JOB_INTERVAL", 30*24*time.Hour), false, a.bankVariableRate),
			a.interval("bank-maintenance-fee", config.Duration("MAINTENANCE_FEE_JOB_INTERVAL", 24*time.Hour), false, a.bankMaintenance),
			a.interval("bank-spent-reset", config.Duration("SPENT_RESET_JOB_INTERVAL", time.Hour), false, a.bankSpentReset),
			a.interval("bank-scheduled-payments", config.Duration("SCHEDULED_PAYMENT_TICK", 5*time.Minute), true, a.bankScheduledPayments),
			// Forex forwards settle on their settlement date — sweep daily.
			a.daily("bank-forex-forward-settlement", 0, 10, a.bankForexForwardSettlement),
		)
	}
	if a.trading != nil {
		jobs = append(
			jobs,
			a.interval("trading-execution", config.Duration("EXECUTION_TICK_INTERVAL", 10*time.Second), true, a.tradingExecution),
			a.interval("trading-saga-recovery", config.Duration("SAGA_RECOVERY_TICK", 30*time.Second), false, a.tradingSagaRecovery),
			a.interval("trading-otc-expiry", config.Duration("OTC_EXPIRY_TICK", 5*time.Minute), true, a.tradingOTCExpiry),
			a.interval("trading-options-refresh", config.Duration("OPTIONS_REFRESH_INTERVAL", 24*time.Hour), true, a.tradingOptionsRefresh),
			a.interval("trading-market-data", config.Duration("MARKET_DATA_REFRESH_INTERVAL", 6*time.Hour), true, a.tradingMarketData),
			a.once("trading-stock-backfill", a.tradingStockBackfill),
			a.daily("trading-actuary-reset", 23, 59, a.tradingActuaryReset),
			a.monthlyEnd("trading-tax", 23, 55, a.tradingTax),
			a.daily("trading-fund-perf", 23, 50, a.tradingFundPerf),
			a.interval("trading-price-alerts", config.Duration("PRICE_ALERT_TICK", time.Minute), true, a.tradingPriceAlerts),
			a.daily("trading-dca", 0, 5, a.tradingDCA),
			// Fired daily; RunDividendPayout no-ops unless the call lands
			// on the last business day of the quarter (todoSpec C3 S54).
			a.daily("trading-dividends", 23, 45, a.tradingDividends),
			// Celina 5 — inter-bank retry queue: re-drive parked
			// cross-bank payments every 5s (spec: retry every 5s, abort
			// after 30s). fireNow so the worker picks up entries promptly
			// on leader acquisition.
			a.interval("trading-interbank-retry", config.Duration("INTERBANK_RETRY_TICK", 5*time.Second), true, a.tradingInterbankRetry),
			// Celina 5 — scheduled/periodic inter-bank payments: submit
			// every due scheduled cross-bank payment. Daily sweep (the
			// finest cadence is DAILY).
			a.daily("trading-scheduled-interbank", 0, 15, a.tradingScheduledInterbank),
		)
	}
	if a.exchange != nil {
		jobs = append(
			jobs,
			a.interval("exchange-fx-refresh", config.Duration("FX_FEED_INTERVAL", 5*time.Minute), true, a.exchangeFXRefresh),
		)
	}
	return jobs
}

// interval fires fn on a fixed cadence, awaiting each call before the
// next (single-flight, so a slow tick never overlaps itself). fireNow
// triggers an immediate first run.
func (a *App) interval(name string, d time.Duration, fireNow bool, fn func(context.Context) error) job {
	return job{name: name, run: func(ctx context.Context) {
		if d <= 0 {
			a.log.InfoContext(ctx, "job disabled (interval<=0)", "job", name)
			return
		}
		a.log.InfoContext(ctx, "interval job started", "job", name, "interval", d.String())
		if fireNow {
			a.fire(ctx, name, fn)
		}
		t := time.NewTicker(d)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				a.fire(ctx, name, fn)
			}
		}
	}}
}

// daily fires fn once per day at h:m in the scheduler's location.
func (a *App) daily(name string, h, m int, fn func(context.Context) error) job {
	return job{name: name, run: func(ctx context.Context) {
		for {
			next := nextDailyOccurrence(time.Now().In(a.loc), h, m)
			a.log.InfoContext(ctx, "daily job scheduled", "job", name, "next", next.Format(time.RFC3339))
			t := time.NewTimer(time.Until(next))
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C:
				a.fire(ctx, name, fn)
			}
		}
	}}
}

// monthlyEnd fires fn at h:m on the last day of each month.
func (a *App) monthlyEnd(name string, h, m int, fn func(context.Context) error) job {
	return job{name: name, run: func(ctx context.Context) {
		for {
			next := nextEndOfMonthAfter(time.Now().In(a.loc), h, m)
			a.log.InfoContext(ctx, "monthly job scheduled", "job", name, "next", next.Format(time.RFC3339))
			t := time.NewTimer(time.Until(next))
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C:
				a.fire(ctx, name, fn)
			}
		}
	}}
}

// once fires fn a single time (e.g. on leader acquisition) then returns.
func (a *App) once(name string, fn func(context.Context) error) job {
	return job{name: name, run: func(ctx context.Context) {
		a.fire(ctx, name, fn)
	}}
}

// fire invokes one trigger with the admin context, logging failures
// (but swallowing them so one bad tick never kills the job loop).
func (a *App) fire(ctx context.Context, name string, fn func(context.Context) error) {
	a.log.InfoContext(ctx, "job triggered", "job", name)
	start := time.Now()
	if err := fn(a.adminCtx(ctx)); err != nil {
		if ctx.Err() != nil {
			return // shutting down / lost leadership
		}
		a.log.ErrorContext(ctx, "job failed",
			"err", err.Error(), "job", name, "duration", time.Since(start).String())
		return
	}
	a.log.InfoContext(ctx, "job completed", "job", name, "duration", time.Since(start).String())
}

// --- Triggers: one gRPC call each ---

func (a *App) bankInstallments(ctx context.Context) error {
	r, err := a.bank.RunInstallmentJob(ctx, &bankpb.RunInstallmentJobRequest{})
	if err != nil {
		return err
	}
	if r.GetProcessed() > 0 {
		a.log.InfoContext(ctx, "installments ran", "processed", r.GetProcessed(), "paid", r.GetPaid(), "overdue", r.GetOverdue())
	}
	return nil
}

func (a *App) bankVariableRate(ctx context.Context) error {
	r, err := a.bank.RunVariableRateJob(ctx, &bankpb.RunVariableRateJobRequest{})
	if err != nil {
		return err
	}
	if r.GetUpdated() > 0 {
		a.log.InfoContext(ctx, "variable-rate ran", "updated", r.GetUpdated())
	}
	return nil
}

func (a *App) bankMaintenance(ctx context.Context) error {
	r, err := a.bank.RunMaintenanceFeeJob(ctx, &bankpb.RunMaintenanceFeeJobRequest{})
	if err != nil {
		return err
	}
	if r.GetProcessed() > 0 {
		a.log.InfoContext(ctx, "maintenance-fee ran", "processed", r.GetProcessed(), "charged", r.GetCharged(), "skipped", r.GetSkipped())
	}
	return nil
}

func (a *App) bankSpentReset(ctx context.Context) error {
	r, err := a.bank.RunSpentResetJob(ctx, &bankpb.RunSpentResetJobRequest{})
	if err != nil {
		return err
	}
	if r.GetDaily() > 0 || r.GetMonthly() > 0 {
		a.log.InfoContext(ctx, "spent-reset ran", "daily", r.GetDaily(), "monthly", r.GetMonthly())
	}
	return nil
}

func (a *App) bankScheduledPayments(ctx context.Context) error {
	r, err := a.bank.RunDueScheduledPayments(ctx, &bankpb.RunDueScheduledPaymentsRequest{})
	if err != nil {
		return err
	}
	if r.GetProcessed() > 0 {
		a.log.InfoContext(ctx, "scheduled payments ran", "processed", r.GetProcessed(), "succeeded", r.GetSucceeded(), "failed", r.GetFailed())
	}
	return nil
}

func (a *App) bankForexForwardSettlement(ctx context.Context) error {
	r, err := a.bank.RunForexForwardSettlement(ctx, &bankpb.RunForexForwardSettlementRequest{})
	if err != nil {
		return err
	}
	if r.GetProcessed() > 0 {
		a.log.InfoContext(ctx, "forex forward settlement ran", "processed", r.GetProcessed(), "settled", r.GetSettled(), "failed", r.GetFailed())
	}
	return nil
}

func (a *App) tradingExecution(ctx context.Context) error {
	r, err := a.trading.RunExecutionTick(ctx, &tradingpb.RunExecutionTickRequest{})
	if err != nil {
		return err
	}
	if r.GetFired() > 0 {
		a.log.InfoContext(ctx, "execution tick", "fired", r.GetFired())
	}
	return nil
}

func (a *App) tradingSagaRecovery(ctx context.Context) error {
	r, err := a.trading.RunSagaRecoveryTick(ctx, &tradingpb.RunSagaRecoveryTickRequest{})
	if err != nil {
		return err
	}
	if r.GetResumed() > 0 {
		a.log.InfoContext(ctx, "saga recovery tick", "resumed", r.GetResumed())
	}
	return nil
}

func (a *App) tradingOTCExpiry(ctx context.Context) error {
	r, err := a.trading.RunOTCExpirySweep(ctx, &tradingpb.RunOTCExpirySweepRequest{})
	if err != nil {
		return err
	}
	if r.GetContractsExpired() > 0 {
		a.log.InfoContext(ctx, "otc expiry sweep", "contracts", r.GetContractsExpired(), "shares_released", r.GetSharesReleased())
	}
	return nil
}

func (a *App) tradingOptionsRefresh(ctx context.Context) error {
	r, err := a.trading.RunOptionsRefresh(ctx, &tradingpb.RunOptionsRefreshRequest{})
	if err != nil {
		return err
	}
	a.log.InfoContext(ctx, "options refresh ran", "underlyings", r.GetUnderlyingsProcessed(), "options", r.GetOptionsUpserted(), "skipped", r.GetSkipped())
	return nil
}

func (a *App) tradingMarketData(ctx context.Context) error {
	r, err := a.trading.RunMarketDataRefresh(ctx, &tradingpb.RunMarketDataRefreshRequest{})
	if err != nil {
		return err
	}
	a.log.InfoContext(ctx, "market-data refresh ran", "stocks", r.GetStocksUpdated(), "forex", r.GetForexUpdated(),
		"skipped", r.GetSkipped(), "errors", r.GetUpstreamErrors(), "throttled", r.GetUpstreamThrottled())
	return nil
}

func (a *App) tradingStockBackfill(ctx context.Context) error {
	r, err := a.trading.RunStockHistoryBackfill(ctx, &tradingpb.RunStockHistoryBackfillRequest{})
	if err != nil {
		return err
	}
	a.log.InfoContext(ctx, "stock-history backfill ran", "symbols", r.GetSymbolsBackfilled(), "rows", r.GetRowsWritten(),
		"skipped", r.GetSkipped(), "errors", r.GetUpstreamErrors(), "throttled", r.GetUpstreamThrottled())
	return nil
}

func (a *App) tradingActuaryReset(ctx context.Context) error {
	r, err := a.trading.RunDailyResetActuaries(ctx, &emptypb.Empty{})
	if err != nil {
		return err
	}
	a.log.InfoContext(ctx, "actuary used-limit reset ran", "affected", r.GetAffected())
	return nil
}

func (a *App) tradingTax(ctx context.Context) error {
	r, err := a.trading.RunTax(ctx, &tradingpb.RunTaxRequest{})
	if err != nil {
		return err
	}
	a.log.InfoContext(ctx, "monthly tax ran", "users_taxed", r.GetUsersTaxed(), "total_rsd", r.GetTotalCollectedRsd())
	return nil
}

func (a *App) tradingDividends(ctx context.Context) error {
	r, err := a.trading.RunDividendPayout(ctx, &tradingpb.RunDividendPayoutRequest{})
	if err != nil {
		return err
	}
	if r.GetRan() {
		a.log.InfoContext(ctx, "quarterly dividend payout ran", "paid", r.GetPaid(), "skipped", r.GetSkipped(), "total_rsd", r.GetTotalRsd())
	}
	return nil
}

func (a *App) tradingFundPerf(ctx context.Context) error {
	r, err := a.trading.RunFundPerformanceSnapshot(ctx, &tradingpb.RunFundPerformanceSnapshotRequest{})
	if err != nil {
		return err
	}
	a.log.InfoContext(ctx, "fund performance snapshot ran", "funds", r.GetFunds())
	return nil
}

func (a *App) tradingPriceAlerts(ctx context.Context) error {
	r, err := a.trading.RunPriceAlertSweep(ctx, &tradingpb.RunPriceAlertSweepRequest{})
	if err != nil {
		return err
	}
	if r.GetTriggered() > 0 {
		a.log.InfoContext(ctx, "price alert sweep ran", "triggered", r.GetTriggered())
	}
	return nil
}

func (a *App) tradingDCA(ctx context.Context) error {
	r, err := a.trading.RunRecurringOrders(ctx, &tradingpb.RunRecurringOrdersRequest{})
	if err != nil {
		return err
	}
	if r.GetCreated() > 0 {
		a.log.InfoContext(ctx, "dca recurring orders ran", "created", r.GetCreated())
	}
	return nil
}

func (a *App) tradingInterbankRetry(ctx context.Context) error {
	r, err := a.crossBank.RunInterbankRetryTick(ctx, &tradingpb.RunInterbankRetryTickRequest{})
	if err != nil {
		return err
	}
	if r.GetSettled() > 0 {
		a.log.InfoContext(ctx, "interbank retry tick", "settled", r.GetSettled())
	}
	return nil
}

func (a *App) tradingScheduledInterbank(ctx context.Context) error {
	r, err := a.crossBank.RunDueInterbankPayments(ctx, &tradingpb.RunDueInterbankPaymentsRequest{})
	if err != nil {
		return err
	}
	if r.GetSubmitted() > 0 {
		a.log.InfoContext(ctx, "scheduled interbank payments ran", "submitted", r.GetSubmitted())
	}
	return nil
}

func (a *App) exchangeFXRefresh(ctx context.Context) error {
	r, err := a.exchange.RefreshFXRates(ctx, &exchangepb.RefreshFXRatesRequest{})
	if err != nil {
		return err
	}
	if r.GetWritten() > 0 {
		a.log.InfoContext(ctx, "fx refresh ran", "written", r.GetWritten())
	}
	return nil
}
