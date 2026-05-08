// Package app wires the user service: config → dependencies → servers.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/config"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/email"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/grpcserver"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/postgres"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/probes"
	pkgredis "github.com/RAF-SI-2025/Banka-3-Backend/pkg/redis"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/shutdown"

	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/user/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/server"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/service"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/store"

	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
)

// Run blocks until the process is signalled to terminate.
func Run() error {
	log := logger.New("user")

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

	st := store.New(pool)
	notifier := buildNotifier(log)

	svc := service.New(st, notifier, service.Config{
		JWTSigningKey: []byte(config.MustString("JWT_SIGNING_KEY")),
		AccessTTL:     config.Duration("JWT_ACCESS_TTL", 0),
		RefreshTTL:    config.Duration("JWT_REFRESH_TTL", 0),
		ActivationTTL: config.Duration("ACTIVATION_TTL", 0),
		ResetTTL:      config.Duration("RESET_TTL", 0),
		WebBaseURL:    config.String("WEB_BASE_URL", "http://localhost:5173"),
	}, log)

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
			userpb.RegisterUserServiceServer(s, server.New(svc))
		})
	})

	probeSrv.MarkReady()
	log.Info("user service ready", "grpc", grpcAddr)

	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// buildNotifier wires the email sender. For c1 the user service uses
// pkg/email directly. Once notification gRPC is wired (c1 next step),
// this swaps to a notification client.
func buildNotifier(log *slog.Logger) service.Notifier {
	cfg := email.Config{
		Host:     config.String("SMTP_HOST", ""),
		Port:     config.Int("SMTP_PORT", 587),
		Username: config.String("SMTP_USERNAME", ""),
		Password: config.String("SMTP_PASSWORD", ""),
		From:     config.String("SMTP_FROM", "no-reply@banka.local"),
		UseTLS:   config.Bool("SMTP_TLS", false),
	}
	sender := email.New(cfg, log)
	return notifierAdapter{sender: sender}
}

type notifierAdapter struct{ sender email.Sender }

func (n notifierAdapter) Send(ctx context.Context, to, subject, body string, html bool) error {
	return n.sender.Send(ctx, email.Message{To: to, Subject: subject, Body: body, HTML: html})
}
