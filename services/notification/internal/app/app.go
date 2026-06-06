// Package app wires the notification service: config → dependencies → servers.
package app

import (
	"context"
	"errors"
	"fmt"

	notifpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/notification/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/config"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/email"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/grpcserver"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/otelinit"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/postgres"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/probes"
	pkgredis "github.com/RAF-SI-2025/Banka-3-Backend/pkg/redis"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/shutdown"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/notification/internal/server"
	"google.golang.org/grpc"

	"golang.org/x/sync/errgroup"
)

// Run blocks until the process is signalled to terminate. Returns the
// first non-nil error from any subsystem.
func Run() error {
	log := logger.New("notification")

	ctx, cancel := shutdown.Context()
	defer cancel()

	prov, err := otelinit.Init(ctx, "notification")
	if err != nil {
		return fmt.Errorf("otelinit: %w", err)
	}
	defer func() { _ = prov.Shutdown(context.Background()) }()

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

	probeSrv := probes.New(fmt.Sprintf(":%d", config.Int("PROBE_PORT", 8081)))
	probeSrv.Register("postgres", func(ctx context.Context) error { return postgres.Ping(ctx, pool) })
	probeSrv.Register("redis", func(ctx context.Context) error { return pkgredis.Ping(ctx, rdb) })

	grpcAddr := fmt.Sprintf(":%d", config.Int("GRPC_PORT", 50051))
	metricsAddr := fmt.Sprintf(":%d", config.Int("METRICS_PORT", 9090))

	// Outbound email sender. SMTP_HOST empty falls through to the
	// log-only sender (same convention as user/bank/trading direct
	// pkg/email use before centralization).
	sender := email.New(email.Config{
		Host:     config.String("SMTP_HOST", ""),
		Port:     config.Int("SMTP_PORT", 587),
		Username: config.String("SMTP_USERNAME", ""),
		Password: config.String("SMTP_PASSWORD", ""),
		From:     config.String("SMTP_FROM", "no-reply@banka.local"),
		UseTLS:   config.Bool("SMTP_USE_TLS", false),
	}, log)
	notifSrv := server.New(sender, log)

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		return probeSrv.ListenAndServe(gctx)
	})
	g.Go(func() error {
		return prov.RunMetricsServer(gctx, metricsAddr)
	})
	g.Go(func() error {
		return grpcserver.Run(gctx, log, grpcAddr, func(s *grpc.Server) {
			notifpb.RegisterNotificationServiceServer(s, notifSrv)
		}, grpcserver.WithStatsHandler(prov.GRPCServerHandler()))
	})

	probeSrv.MarkReady()
	log.Info("notification service ready", "grpc", grpcAddr)

	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
