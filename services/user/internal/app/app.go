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
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/observability"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/postgres"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/probes"
	pkgredis "github.com/RAF-SI-2025/Banka-3-Backend/pkg/redis"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/shutdown"

	notifpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/notification/v1"
	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/trading/v1"
	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/user/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/server"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/service"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/store"

	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
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
	notifier, closeNotif, err := buildNotifier(ctx, log)
	if err != nil {
		return err
	}
	defer closeNotif()

	svc := service.New(st, notifier, rdb, service.Config{
		JWTSigningKey:    []byte(config.MustString("JWT_SIGNING_KEY")),
		AccessTTL:        config.Duration("JWT_ACCESS_TTL", 0),
		RefreshTTL:       config.Duration("JWT_REFRESH_TTL", 0),
		MobileRefreshTTL: config.Duration("JWT_MOBILE_REFRESH_TTL", 0),
		ActivationTTL:    config.Duration("ACTIVATION_TTL", 0),
		ResetTTL:         config.Duration("RESET_TTL", 0),
		WebBaseURL:       config.String("WEB_BASE_URL", "http://localhost:5173"),
	}, log)

	// Optional trading-svc client for the fund-manager reassignment on
	// supervisor demotion. Skip wiring on a minimal dev stack that
	// doesn't run trading (the cascade no-ops with a warning).
	if tradingAddr := config.String("TRADING_GRPC_ADDR", ""); tradingAddr != "" {
		conn, err := grpc.NewClient(tradingAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("dial trading: %w", err)
		}
		defer conn.Close()
		svc.FundReassigner = &fundReassignerAdapter{c: tradingpb.NewTradingServiceClient(conn)}
	} else {
		log.Info("TRADING_GRPC_ADDR not set; supervisor-demotion fund cascade disabled")
	}

	probeSrv := probes.New(fmt.Sprintf(":%d", config.Int("PROBE_PORT", 8081)))
	probeSrv.Register("postgres", func(ctx context.Context) error { return postgres.Ping(ctx, pool) })
	probeSrv.Register("redis", func(ctx context.Context) error { return pkgredis.Ping(ctx, rdb) })
	probeSrv.MountMetrics(observability.New("user").MetricsHandler())

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

// buildNotifier wires the email sender. When NOTIFICATION_GRPC_ADDR is
// set the user service dials the centralized notification-svc;
// otherwise it falls back to pkg/email directly so minimal dev and
// unit tests keep working without the extra service.
func buildNotifier(ctx context.Context, log *slog.Logger) (service.Notifier, func(), error) {
	if addr := config.String("NOTIFICATION_GRPC_ADDR", ""); addr != "" {
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return nil, func() {}, fmt.Errorf("dial notification: %w", err)
		}
		return &notifClientAdapter{c: notifpb.NewNotificationServiceClient(conn), origin: "user"},
			func() { conn.Close() }, nil
	}
	log.Info("NOTIFICATION_GRPC_ADDR not set; falling back to direct pkg/email")
	cfg := email.Config{
		Host:     config.String("SMTP_HOST", ""),
		Port:     config.Int("SMTP_PORT", 587),
		Username: config.String("SMTP_USERNAME", ""),
		Password: config.String("SMTP_PASSWORD", ""),
		From:     config.String("SMTP_FROM", "no-reply@banka.local"),
		UseTLS:   config.Bool("SMTP_TLS", false),
	}
	sender := email.New(cfg, log)
	return notifierAdapter{sender: sender}, func() {}, nil
}

type notifierAdapter struct{ sender email.Sender }

func (n notifierAdapter) Send(ctx context.Context, to, subject, body string, html bool) error {
	return n.sender.Send(ctx, email.Message{To: to, Subject: subject, Body: body, HTML: html})
}

// notifClientAdapter dials notification-svc.SendEmail. Templating
// happens in the caller (user-svc renders the Serbian body itself);
// notification-svc is a thin SMTP-credentials owner.
type notifClientAdapter struct {
	c      notifpb.NotificationServiceClient
	origin string
}

func (n *notifClientAdapter) Send(ctx context.Context, to, subject, body string, html bool) error {
	_, err := n.c.SendEmail(ctx, &notifpb.SendEmailRequest{
		To:            to,
		Subject:       subject,
		Body:          body,
		Html:          html,
		Kind:          notifpb.EmailKind_EMAIL_KIND_GENERIC,
		OriginService: n.origin,
	})
	return err
}
