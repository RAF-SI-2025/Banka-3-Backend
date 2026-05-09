// Package app wires the trading service: config → dependencies → servers.
package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	exchangepb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/exchange/v1"
	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/trading/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/config"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/grpcserver"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/postgres"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/probes"
	pkgredis "github.com/RAF-SI-2025/Banka-3-Backend/pkg/redis"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/shutdown"
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

	pool, err := postgres.Open(ctx, config.MustString("DATABASE_URL"))
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	defer pool.Close()

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

	st := store.New(pool)
	svc := service.New(st, service.Config{
		Belgrade:     belgrade,
		FXCommission: config.String("FX_COMMISSION", "0.005"),
	}, log)

	// Exchange-rate client for foreign-currency → RSD conversions used
	// by the agent-limit check and the capital-gains tax math. The
	// service tolerates a nil Rates field on a minimal dev stack.
	if exAddr := config.String("EXCHANGE_GRPC_ADDR", ""); exAddr != "" {
		conn, err := grpc.NewClient(exAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("dial exchange: %w", err)
		}
		defer conn.Close()
		svc.Rates = &exchangeAdapter{c: exchangepb.NewExchangeServiceClient(conn)}
	} else {
		log.Warn("EXCHANGE_GRPC_ADDR not set; agent-limit math will use raw notional for foreign trades")
	}

	probeSrv := probes.New(fmt.Sprintf(":%d", config.Int("PROBE_PORT", 8081)))
	probeSrv.Register("postgres", func(ctx context.Context) error { return postgres.Ping(ctx, pool) })
	probeSrv.Register("redis", func(ctx context.Context) error { return pkgredis.Ping(ctx, rdb) })

	grpcAddr := fmt.Sprintf(":%d", config.Int("GRPC_PORT", 50051))

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		return probeSrv.ListenAndServe(gctx)
	})
	g.Go(func() error {
		return grpcserver.Run(gctx, log, grpcAddr, func(s *grpc.Server) {
			tradingpb.RegisterTradingServiceServer(s, server.New(svc))
		})
	})

	// Spec p.38: agents' used_limit resets at 23:59 (Europe/Belgrade).
	// The supervisor RPC exposes the same operation for manual reruns
	// during dev; this loop fires it daily.
	g.Go(func() error {
		return runDailyAt(gctx, log, "actuary-used-limit-reset", belgrade, 23, 59, runActuaryDailyReset(svc))
	})

	probeSrv.MarkReady()
	log.Info("trading service ready", "grpc", grpcAddr)

	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
