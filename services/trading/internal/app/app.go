// Package app wires the trading service: config → dependencies → servers.
package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/bank/v1"
	exchangepb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/exchange/v1"
	notifpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/notification/v1"
	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/trading/v1"
	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/user/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/clock"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/config"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/email"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/grpcserver"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/otelinit"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/postgres"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/probes"
	pkgredis "github.com/RAF-SI-2025/Banka-3-Backend/pkg/redis"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/shutdown"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/external/alphavantage"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/external/influxmarket"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/external/interbank"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/saga"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/server"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/service"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"golang.org/x/sync/errgroup"
)

// Run blocks until the process is signalled to terminate. Returns the
// first non-nil error from any subsystem.
func Run() error {
	log := logger.New("trading")

	ctx, cancel := shutdown.Context()
	defer cancel()

	prov, err := otelinit.Init(ctx, "trading")
	if err != nil {
		return fmt.Errorf("otelinit: %w", err)
	}
	defer func() { _ = prov.Shutdown(context.Background()) }()

	// OpenPair dials the primary (DATABASE_URL → banka-pg-pooler-rw) and,
	// when set, a hot-standby read pool (DATABASE_READ_URL →
	// banka-pg-pooler-ro). SELECTs marked postgres.WithRead(ctx) (list/
	// get-catalog/report reads) route to the standby; writes,
	// transactions, the SAGA orchestrator, execution-worker, cron and
	// idempotency reads stay on the primary.
	db, err := postgres.OpenPair(ctx, config.MustString("DATABASE_URL"), config.String("DATABASE_READ_URL", ""))
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	defer db.Close()
	if db.RO != nil {
		log.Info("read replica routing enabled")
	}

	rdb, err := pkgredis.Open(ctx, config.MustString("REDIS_ADDR"), config.String("REDIS_PASSWORD", ""))
	if err != nil {
		return fmt.Errorf("redis: %w", err)
	}
	defer rdb.Close()

	belgrade, err := time.LoadLocation("Europe/Belgrade")
	if err != nil {
		log.Warn("Europe/Belgrade timezone unavailable, falling back to UTC", "error", err)
		belgrade = time.UTC
	}

	st := store.New(db)
	svc := service.New(st, service.Config{
		Belgrade:                belgrade,
		FXCommission:            config.String("FX_COMMISSION", "0.005"),
		SagaDebugFaultInjection: config.Bool("SAGA_DEBUG_FAULT_INJECTION", false),
		BankName:                config.String("BANK_NAME", "Banka 3"),
	}, log)

	// QA-adjustable clock (pkg/clock). When CLOCK_DEBUG=true the
	// gateway's POST /api/v1/_debug/clock writes an offset to Redis
	// that this Clock picks up via the StartRefresher goroutine, so
	// the entire service observes shifted business time uniformly
	// (every settlement/expiry/after-hours/cron check on the service
	// layer routes through svc.Clock.Now() via Service.now()).
	// Production leaves CLOCK_DEBUG unset and the Clock is a no-op
	// passthrough to time.Now().UTC().
	adj := clock.NewAdjustable(rdb, config.Bool("CLOCK_DEBUG", false))
	adj.StartRefresher(ctx)
	svc.Clock = adj

	// Exchange-rate client for foreign-currency → RSD conversions used
	// by the agent-limit check and the capital-gains tax math. The
	// service tolerates a nil Rates field on a minimal dev stack.
	if exAddr := config.String("EXCHANGE_GRPC_ADDR", ""); exAddr != "" {
		conn, err := grpc.NewClient(
			exAddr, grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithStatsHandler(prov.GRPCClientHandler()),
		)
		if err != nil {
			return fmt.Errorf("dial exchange: %w", err)
		}
		defer conn.Close()
		svc.Rates = &exchangeAdapter{c: exchangepb.NewExchangeServiceClient(conn)}
	} else {
		log.Warn("EXCHANGE_GRPC_ADDR not set; agent-limit math will use raw notional for foreign trades")
	}

	// User-service resolver for the supervisor tax dashboard
	// (display_name + name filter, spec p.63) and the OTC email
	// notifier's recipient lookup. The service tolerates a nil Users
	// field on a minimal dev stack — display_name then comes back
	// empty and the OTC notifier drops its lookups.
	var userClient userpb.UserServiceClient
	if userAddr := config.String("USER_GRPC_ADDR", ""); userAddr != "" {
		conn, err := grpc.NewClient(
			userAddr, grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithStatsHandler(prov.GRPCClientHandler()),
		)
		if err != nil {
			return fmt.Errorf("dial user: %w", err)
		}
		defer conn.Close()
		userClient = userpb.NewUserServiceClient(conn)
		svc.Users = &userResolverAdapter{c: userClient}
	} else {
		log.Warn("USER_GRPC_ADDR not set; tax dashboard display_name will be empty")
	}

	// OTC email notifier. With NOTIFICATION_GRPC_ADDR set, outbound
	// mail goes through notification-svc; otherwise fall back to
	// pkg/email directly so dev/test setups without notification-svc
	// keep working.
	var emailSender email.Sender
	if notifAddr := config.String("NOTIFICATION_GRPC_ADDR", ""); notifAddr != "" {
		conn, err := grpc.NewClient(
			notifAddr, grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithStatsHandler(prov.GRPCClientHandler()),
		)
		if err != nil {
			return fmt.Errorf("dial notification: %w", err)
		}
		defer conn.Close()
		notifClient := notifpb.NewNotificationServiceClient(conn)
		emailSender = &notifEmailSender{c: notifClient}
		// General-purpose fan-out (email + in-app) for C3 order/price-alert
		// events. Shares the one notification client. In-app requires
		// notification-svc, so it's only wired on this branch.
		svc.Notifier = &notifierAdapter{c: notifClient}
	} else {
		emailSender = email.New(email.Config{
			Host:     config.String("SMTP_HOST", ""),
			Port:     config.Int("SMTP_PORT", 587),
			Username: config.String("SMTP_USERNAME", ""),
			Password: config.String("SMTP_PASSWORD", ""),
			From:     config.String("SMTP_FROM", "banka@example.local"),
			UseTLS:   config.Bool("SMTP_USE_TLS", true),
		}, log)
	}
	svc.OTCNotifier = newOTCEmailNotifier(emailSender, userClient, log)

	// Bank settler — the execution worker dials this on every fill to
	// move money between user account and bank house account. Without
	// it, fills fail. Skip wiring on a dev stack that doesn't run bank.
	if bankAddr := config.String("BANK_GRPC_ADDR", ""); bankAddr != "" {
		conn, err := grpc.NewClient(
			bankAddr, grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithStatsHandler(prov.GRPCClientHandler()),
		)
		if err != nil {
			return fmt.Errorf("dial bank: %w", err)
		}
		defer conn.Close()
		adapter := &bankSettlerAdapter{c: bankpb.NewBankServiceClient(conn)}
		svc.Settler = adapter
		svc.TaxSettler = adapter
		svc.MarginChecker = adapter
		svc.ForexSettler = adapter
		svc.Reservations = adapter
		// Celina 5 — same connection, second service.
		svc.InterbankPayer = &interbankPayerAdapter{c: bankpb.NewInterbankProtocolServiceClient(conn)}
	} else {
		log.Warn("BANK_GRPC_ADDR not set; execution worker will refuse to fill")
	}

	// Celina 5 — outbound cross-bank OTC. Parses INTERBANK_ROUTES; when
	// empty the field stays nil and the service nil-checks at every
	// PartnerOTC.* call site (Create/Counter/Withdraw/Accept/Discover
	// surface a clean FailedPrecondition).
	if raw := config.String("INTERBANK_ROUTES", ""); raw != "" {
		routes := interbank.ParseRoutes(raw)
		if len(routes) > 0 {
			client := interbank.New(interbank.Config{
				Routes:           routes,
				APIKey:           config.String("INTERBANK_API_KEY", ""),
				OwnRoutingNumber: config.String("BANK_ROUTING_NUMBER", "333"),
			}, log)
			svc.PartnerOTC = client
			svc.PartnerPayer = client
			log.Info("interbank client configured", "partners", len(routes))
		}
	}

	// Alpha Vantage market-data feed (spec p.40, p.42). Optional — when
	// the API key is unset, the refresher field stays nil and the
	// market-data cron no-ops.
	// BonusInfluxDB (#285) — optional side-channel that mirrors daily
	// price writes to InfluxDB. NewFromEnv returns a no-op when the
	// INFLUX_* env vars aren't set, so this stays a no-cost wire when
	// the observability/analytics path isn't configured.
	influxStore := influxmarket.NewFromEnv()
	influx := &influxMirror{s: influxStore}
	if influx.Enabled() {
		log.Info("influx market-data mirror enabled", "url", config.String("INFLUX_URL", ""))
	}
	if avKey := config.String("ALPHAVANTAGE_API_KEY", ""); avKey != "" {
		client := alphavantage.New(avKey)
		svc.MarketData = &service.MarketData{
			Store:       st,
			Log:         log,
			Stocks:      &alphaStockAdapter{c: client},
			Forex:       &alphaForexAdapter{c: client},
			History:     &alphaHistoryAdapter{c: client},
			StockSpread: config.Float("STOCK_BIDASK_SPREAD", 0.001),
			Pause:       config.Duration("MARKET_DATA_PAUSE", 13*time.Second),
			Belgrade:    belgrade,
			Influx:      influx,
		}
		log.Info("alphavantage market-data refresh enabled")
	} else {
		log.Warn("ALPHAVANTAGE_API_KEY not set; market-data refresh disabled (catalog price fields stay at seed values)")
	}

	// Spec p.43 Pristup 2 — Black-Scholes option chain generator. No
	// upstream dependency; always wired in production.
	svc.Options = &service.OptionGenerator{
		Store:        st,
		Log:          log,
		RiskFreeRate: config.Float("OPTIONS_RISK_FREE_RATE", 0.05),
		Volatility:   config.Float("OPTIONS_VOLATILITY", 0.30),
		Belgrade:     belgrade,
	}

	// c4 — SAGA orchestrator for OTC + funds intra-bank flows. The
	// registry holds the typed Definition for each saga type the
	// service knows about. Individual definitions (otc_accept,
	// otc_exercise, fund_invest, fund_withdraw, option_exercise) are
	// registered by service.RegisterSagas during construction; the
	// orchestrator and the recovery worker share the same registry.
	svc.SagaStore = st.Sagas()
	sagaReg := saga.NewRegistry()
	svc.SagaOrch = saga.New(svc.SagaStore, sagaReg, log)
	service.RegisterSagas(sagaReg, svc)

	probeSrv := probes.New(fmt.Sprintf(":%d", config.Int("PROBE_PORT", 8081)))
	probeSrv.Register("postgres", func(ctx context.Context) error { return db.Ping(ctx) })
	probeSrv.Register("redis", func(ctx context.Context) error { return pkgredis.Ping(ctx, rdb) })

	grpcAddr := fmt.Sprintf(":%d", config.Int("GRPC_PORT", 50051))
	metricsAddr := fmt.Sprintf(":%d", config.Int("METRICS_PORT", 9090))

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		return probeSrv.ListenAndServe(gctx)
	})
	g.Go(func() error {
		return prov.RunMetricsServer(gctx, metricsAddr)
	})
	g.Go(func() error {
		return grpcserver.Run(gctx, log, grpcAddr, func(s *grpc.Server) {
			srv := server.New(svc)
			tradingpb.RegisterTradingServiceServer(s, srv)
			// Celina 5 — cross-bank OTC RPCs share the same Server.
			tradingpb.RegisterExternalOTCServiceServer(s, srv)
			// Celina 5 — user-initiated cross-bank cash payments.
			tradingpb.RegisterCrossBankPaymentServiceServer(s, srv)
		}, grpcserver.WithStatsHandler(prov.GRPCServerHandler()))
	})

	// Execution cadence interval is read regardless of whether this
	// process runs the in-process worker: the service's per-order cadence
	// math (RunExecutionTick) uses it whether the tick is driven by the
	// in-process loop or by the scheduler's RunExecutionTick RPC.
	tick := config.Duration("EXECUTION_TICK_INTERVAL", 10*time.Second)
	svc.Cfg.ExecutionTickInterval = tick

	// In-process background workers + crons. JOBS_ENABLED (default true)
	// gates the whole set: when the deployment runs the scheduler service,
	// trading is started with JOBS_ENABLED=false and the scheduler drives
	// these via the Run* RPCs instead — trading then becomes a stateless,
	// horizontally-scalable request handler.
	if config.Bool("JOBS_ENABLED", true) {
		// Spec p.38: agents' used_limit resets at 23:59 (Europe/Belgrade).
		g.Go(func() error {
			return runDailyAt(gctx, log, "actuary-used-limit-reset", belgrade, 23, 59, runActuaryDailyReset(svc))
		})

		// Spec p.55-56 partial-fill worker.
		g.Go(func() error {
			return runExecutionWorker(gctx, log, svc, tick)
		})

		// Spec p.62 capital-gains tax — last day of each month at 23:55
		// (Europe/Belgrade). Supervisor-triggered runs via the RunTax RPC
		// share the same code path.
		g.Go(func() error {
			return runMonthlyTaxCron(gctx, log, svc, belgrade)
		})

		// Spec p.43 Black-Scholes option chain refresh. Daily; first tick
		// fires immediately so a fresh container has options without delay.
		optInterval := config.Duration("OPTIONS_REFRESH_INTERVAL", 24*time.Hour)
		g.Go(func() error {
			return runOptionsRefresh(gctx, log, svc, optInterval)
		})

		// Alpha Vantage refresh (spec p.40, p.42). 6h default cadence keeps
		// us inside the free tier's 25/day quota. No-op when MarketData is nil.
		mdInterval := config.Duration("MARKET_DATA_REFRESH_INTERVAL", 6*time.Hour)
		g.Go(func() error {
			return runMarketDataRefresh(gctx, log, svc, mdInterval)
		})

		// One-shot stock-history backfill (spec p.40). When an Alpha
		// Vantage key is configured this pulls real daily history at
		// startup. No-op when MarketData / History is nil.
		g.Go(func() error {
			return runStockHistoryBackfill(gctx, log, svc)
		})

		// c4 SAGA recovery worker — resumes sagas parked by a transient
		// error (or a crashed worker). The orchestrator's advisory lock
		// keeps this from racing foreground saga drives.
		sagaTick := config.Duration("SAGA_RECOVERY_TICK", 30*time.Second)
		g.Go(func() error {
			return runSagaRecoveryWorker(gctx, log, svc, sagaTick)
		})

		// c4 OTC contract expiry sweep (spec p.69). 5min default cadence;
		// idempotent and bounded per tick.
		otcExpiryTick := config.Duration("OTC_EXPIRY_TICK", 5*time.Minute)
		g.Go(func() error {
			return runOTCExpirySweep(gctx, log, svc, otcExpiryTick)
		})

		// Fund performance snapshot cron (spec p.74). One snapshot per
		// active fund per day at 23:50 Europe/Belgrade. Feeds the FE chart.
		g.Go(func() error {
			return runFundPerformanceCron(gctx, log, svc, belgrade)
		})
	} else {
		log.Info("JOBS_ENABLED=false; in-process workers/crons disabled (driven by scheduler service)")
	}

	probeSrv.MarkReady()
	log.Info("trading service ready", "grpc", grpcAddr)

	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
