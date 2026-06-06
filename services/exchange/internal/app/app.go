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
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/otelinit"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/postgres"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/probes"
	pkgredis "github.com/RAF-SI-2025/Banka-3-Backend/pkg/redis"

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

	prov, err := otelinit.Init(ctx, "exchange")
	if err != nil {
		return fmt.Errorf("otelinit: %w", err)
	}
	defer func() { _ = prov.Shutdown(context.Background()) }()

	// OpenPair dials the primary (DATABASE_URL → banka-pg-pooler-rw) and,
	// when set, a hot-standby read pool (DATABASE_READ_URL →
	// banka-pg-pooler-ro). Reads marked postgres.WithRead(ctx) route to
	// the standby; writes and transactions stay on the primary.
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

	st := store.New(db)
	svc := service.New(st, log)

	// The FX feeder is always constructed so the RefreshFXRates RPC works
	// even when the in-process loop is disabled (the scheduler service
	// drives it via RPC then). The loop itself is gated below.
	feeder := &feed.Feeder{
		Fetcher: &feed.OpenERAPI{BaseURL: config.String("FX_FEED_URL", "")},
		Store:   st,
		Log:     log,
		Spread:  config.Float("FX_FEED_SPREAD", 0.01),
	}
	svc.Refresher = feeder

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
			exchangepb.RegisterExchangeServiceServer(s, server.New(svc))
		}, grpcserver.WithStatsHandler(prov.GRPCServerHandler()))
	})

	// Background FX feed: periodically pulls public mid rates from the
	// keyless open.er-api.com endpoint and upserts X→RSD pairs. The
	// first tick fires immediately on boot; the 5-minute cadence keeps
	// the menjačnica board fresh without straining the free upstream.
	//
	// JOBS_ENABLED (default true) gates the in-process loop: when the
	// scheduler service owns the schedule, exchange runs with
	// JOBS_ENABLED=false and the scheduler drives RefreshFXRates via RPC,
	// leaving exchange a stateless, horizontally-scalable handler.
	// Disable entirely with FX_FEED_INTERVAL=0.
	feedInterval := config.Duration("FX_FEED_INTERVAL", 5*time.Minute)
	if config.Bool("JOBS_ENABLED", true) && feedInterval > 0 {
		g.Go(func() error {
			return feeder.Run(gctx, feedInterval)
		})
	} else {
		log.Info("fx feed loop disabled (JOBS_ENABLED=false or FX_FEED_INTERVAL=0); refresh available via RPC")
	}

	probeSrv.MarkReady()
	log.Info("exchange service ready", "grpc", grpcAddr)

	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
