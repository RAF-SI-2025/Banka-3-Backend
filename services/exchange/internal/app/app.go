// Package app wires the exchange service: config → dependencies → servers.
package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	exchangepb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/exchange/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/config"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/grpcserver"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/observability"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/postgres"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/probes"
	pkgredis "github.com/RAF-SI-2025/Banka-3-Backend/pkg/redis"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/shutdown"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/exchange/internal/feed"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/exchange/internal/server"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/exchange/internal/service"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/exchange/internal/store"
	"google.golang.org/grpc"

	"golang.org/x/sync/errgroup"
)

// Run blocks until the process is signalled to terminate. Returns the
// first non-nil error from any subsystem.
func Run() error {
	log := logger.New("exchange")

	ctx, cancel := shutdown.Context()
	defer cancel()

	pool, err := postgres.Open(ctx, config.MustString("DATABASE_URL"))
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	defer pool.Close()

	// BonusReadReplicaRouting (#287) — optional hot-standby pool.
	var readPool *pgxpool.Pool
	if readURL := config.String("DATABASE_READ_URL", ""); readURL != "" {
		readPool, err = postgres.Open(ctx, readURL)
		if err != nil {
			return fmt.Errorf("postgres replica: %w", err)
		}
		defer readPool.Close()
		log.Info("read replica routing enabled")
	}

	rdb, err := pkgredis.Open(ctx, config.MustString("REDIS_ADDR"), config.String("REDIS_PASSWORD", ""))
	if err != nil {
		return fmt.Errorf("redis: %w", err)
	}
	defer rdb.Close()

	st := store.New(pool)
	st.ReadPool = readPool
	svc := service.New(st, log)

	probeSrv := probes.New(fmt.Sprintf(":%d", config.Int("PROBE_PORT", 8081)))
	probeSrv.Register("postgres", func(ctx context.Context) error { return postgres.Ping(ctx, pool) })
	probeSrv.Register("redis", func(ctx context.Context) error { return pkgredis.Ping(ctx, rdb) })
	probeSrv.MountMetrics(observability.New("exchange").MetricsHandler())

	grpcAddr := fmt.Sprintf(":%d", config.Int("GRPC_PORT", 50051))

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		return probeSrv.ListenAndServe(gctx)
	})
	g.Go(func() error {
		return grpcserver.Run(gctx, log, grpcAddr, func(s *grpc.Server) {
			exchangepb.RegisterExchangeServiceServer(s, server.New(svc))
		})
	})

	// Background FX feed: periodically pulls public mid rates from the
	// keyless open.er-api.com endpoint and upserts X→RSD pairs. The
	// first tick fires immediately on boot; the 5-minute cadence keeps
	// the menjačnica board fresh without straining the free upstream
	// (it refreshes its own data daily anyway). Disable with
	// FX_FEED_INTERVAL=0.
	feedInterval := config.Duration("FX_FEED_INTERVAL", 5*time.Minute)
	if feedInterval > 0 {
		feeder := &feed.Feeder{
			Fetcher: &feed.OpenERAPI{BaseURL: config.String("FX_FEED_URL", "")},
			Store:   st,
			Log:     log,
			Spread:  config.Float("FX_FEED_SPREAD", 0.01),
		}
		g.Go(func() error {
			return feeder.Run(gctx, feedInterval)
		})
	} else {
		log.Info("fx feed disabled (FX_FEED_INTERVAL=0)")
	}

	probeSrv.MarkReady()
	log.Info("exchange service ready", "grpc", grpcAddr)

	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
