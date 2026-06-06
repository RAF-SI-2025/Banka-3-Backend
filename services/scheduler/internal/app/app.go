// Package app wires the scheduler service: it owns every cron + worker
// schedule that must run once per cluster (not once per replica) and
// drives them by calling the owning services' trigger RPCs. The
// business services run with JOBS_ENABLED=false and become stateless +
// horizontally-scalable; this service is the single non-replicatable
// driver (replicas=1, or leader-elected for HA).
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/bank/v1"
	exchangepb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/exchange/v1"
	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/trading/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/config"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/otelinit"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/probes"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/shutdown"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// App holds the scheduler's dependencies: the service clients it drives
// and a fixed location for calendar jobs.
type App struct {
	log     *slog.Logger
	loc     *time.Location
	bank    bankpb.BankServiceClient
	trading tradingpb.TradingServiceClient
	// crossBank drives celina-5 cross-bank payment jobs (retry queue +
	// scheduled inter-bank payments). Shares the trading connection.
	crossBank tradingpb.CrossBankPaymentServiceClient
	exchange  exchangepb.ExchangeServiceClient
}

// Run blocks until the process is signalled to terminate.
func Run() error {
	log := logger.New("scheduler")

	ctx, cancel := shutdown.Context()
	defer cancel()

	prov, err := otelinit.Init(ctx, "scheduler")
	if err != nil {
		return fmt.Errorf("otelinit: %w", err)
	}
	defer func() { _ = prov.Shutdown(context.Background()) }()

	belgrade, err := time.LoadLocation("Europe/Belgrade")
	if err != nil {
		log.Warn("Europe/Belgrade timezone unavailable, falling back to UTC", "error", err)
		belgrade = time.UTC
	}

	a := &App{log: log, loc: belgrade}

	var closers []func()
	dial := func(name, addr string) *grpc.ClientConn {
		if addr == "" {
			log.Warn("grpc addr not set; jobs for this service disabled", "service", name)
			return nil
		}
		conn, err := grpc.NewClient(
			addr,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithStatsHandler(prov.GRPCClientHandler()),
		)
		if err != nil {
			log.Error("dial failed; jobs for this service disabled", "service", name, "err", err.Error())
			return nil
		}
		closers = append(closers, func() { _ = conn.Close() })
		return conn
	}
	if c := dial("bank", config.String("BANK_GRPC_ADDR", "")); c != nil {
		a.bank = bankpb.NewBankServiceClient(c)
	}
	if c := dial("trading", config.String("TRADING_GRPC_ADDR", "")); c != nil {
		a.trading = tradingpb.NewTradingServiceClient(c)
		a.crossBank = tradingpb.NewCrossBankPaymentServiceClient(c)
	}
	if c := dial("exchange", config.String("EXCHANGE_GRPC_ADDR", "")); c != nil {
		a.exchange = exchangepb.NewExchangeServiceClient(c)
	}
	defer func() {
		for _, c := range closers {
			c()
		}
	}()

	probeSrv := probes.New(fmt.Sprintf(":%d", config.Int("PROBE_PORT", 8081)))
	metricsAddr := fmt.Sprintf(":%d", config.Int("METRICS_PORT", 9090))

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		return probeSrv.ListenAndServe(gctx)
	})
	g.Go(func() error {
		return prov.RunMetricsServer(gctx, metricsAddr)
	})
	g.Go(func() error {
		return a.runWithLeaderElection(gctx)
	})

	probeSrv.MarkReady()
	log.Info("scheduler ready")

	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
