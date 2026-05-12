// Package app wires the bank service: config → dependencies → servers.
package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/bank/v1"
	exchangepb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/exchange/v1"
	notifpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/notification/v1"
	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/user/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/config"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/email"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/grpcserver"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/postgres"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/probes"
	pkgredis "github.com/RAF-SI-2025/Banka-3-Backend/pkg/redis"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/shutdown"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/server"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/service"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"golang.org/x/sync/errgroup"
)

// Run blocks until the process is signalled to terminate. Returns the
// first non-nil error from any subsystem.
func Run() error {
	log := logger.New("bank")

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
	svc := service.New(st, service.Config{
		BankCode:     config.String("BANK_CODE", "333"),
		Branch:       config.String("BANK_BRANCH", "0001"),
		FXCommission: config.String("BANK_FX_COMMISSION", "0.005"),
		// MustString fails fast in prod if the operator forgot to set
		// the pepper; .env.example carries a placeholder for dev.
		CVVPepper: config.MustString("BANK_CVV_PEPPER"),
	}, log)

	if exAddr := config.String("EXCHANGE_GRPC_ADDR", ""); exAddr != "" {
		conn, err := grpc.NewClient(exAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("dial exchange: %w", err)
		}
		defer conn.Close()
		svc.Rates = &exchangeAdapter{c: exchangepb.NewExchangeServiceClient(conn)}
	} else {
		log.Warn("EXCHANGE_GRPC_ADDR not set; FX/menjačnica paths will return error")
	}

	// User service client — needed for notification email lookups. If
	// USER_GRPC_ADDR isn't set the bank still boots; notifications are
	// no-ops (logged only).
	if userAddr := config.String("USER_GRPC_ADDR", ""); userAddr != "" {
		conn, err := grpc.NewClient(userAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("dial user: %w", err)
		}
		defer conn.Close()
		svc.UserResolver = &userResolverAdapter{c: userpb.NewUserServiceClient(conn)}
	} else {
		log.Warn("USER_GRPC_ADDR not set; client-email lookup disabled (notifications will be skipped)")
	}

	// Notifier wires to notification-svc when NOTIFICATION_GRPC_ADDR is
	// set (c4 PR4 NOTIFY-1 centralization); otherwise pkg/email is used
	// directly so slice-1 dev and unit tests keep working. Templating
	// continues to live in bank-svc — notification-svc is currently a
	// thin SMTP-credentials owner; typed event RPCs will migrate
	// templates later.
	if notifAddr := config.String("NOTIFICATION_GRPC_ADDR", ""); notifAddr != "" {
		conn, err := grpc.NewClient(notifAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("dial notification: %w", err)
		}
		defer conn.Close()
		svc.Notifier = &notifClientAdapter{c: notifpb.NewNotificationServiceClient(conn)}
	} else {
		emailCfg := email.Config{
			Host:     config.String("SMTP_HOST", ""),
			Port:     config.Int("SMTP_PORT", 587),
			Username: config.String("SMTP_USERNAME", ""),
			Password: config.String("SMTP_PASSWORD", ""),
			From:     config.String("SMTP_FROM", "no-reply@banka.local"),
			UseTLS:   config.Bool("SMTP_TLS", false),
		}
		svc.Notifier = bankNotifierAdapter{sender: email.New(emailCfg, log)}
	}

	// Bring up the bank-owned house accounts before serving traffic so
	// the FX flow can always look them up. Idempotent — only inserts
	// missing currencies.
	if err := svc.EnsureSystemAccounts(ctx); err != nil {
		return fmt.Errorf("seed system accounts: %w", err)
	}

	installmentInterval := config.Duration("INSTALLMENT_JOB_INTERVAL", 24*time.Hour)
	variableRateInterval := config.Duration("VARIABLE_RATE_JOB_INTERVAL", 30*24*time.Hour)
	maintenanceFeeInterval := config.Duration("MAINTENANCE_FEE_JOB_INTERVAL", 24*time.Hour)
	// Spent counters roll over by calendar day/month; the SQL is
	// idempotent so an hourly tick gives us at-most-1h of staleness
	// after midnight without burning DB on the no-op runs.
	spentResetInterval := config.Duration("SPENT_RESET_JOB_INTERVAL", time.Hour)

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
			bankpb.RegisterBankServiceServer(s, server.New(svc))
		})
	})

	// Background jobs: daily installment collection + monthly variable-
	// rate refresh. Both are bypassed (interval == 0) by default in
	// tests; configure via INSTALLMENT_JOB_INTERVAL / VARIABLE_RATE_JOB_INTERVAL.
	if installmentInterval > 0 {
		g.Go(func() error { return runJobLoop(gctx, log, "installments", installmentInterval, svc.RunInstallmentJobAuto) })
	}
	if variableRateInterval > 0 {
		g.Go(func() error { return runJobLoop(gctx, log, "variable-rate", variableRateInterval, svc.RunVariableRateJobAuto) })
	}
	if maintenanceFeeInterval > 0 {
		g.Go(func() error { return runJobLoop(gctx, log, "maintenance-fee", maintenanceFeeInterval, svc.RunMaintenanceFeeJobAuto) })
	}
	if spentResetInterval > 0 {
		g.Go(func() error { return runJobLoop(gctx, log, "spent-reset", spentResetInterval, svc.RunSpentResetJobAuto) })
	}

	probeSrv.MarkReady()
	log.Info("bank service ready", "grpc", grpcAddr)

	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
