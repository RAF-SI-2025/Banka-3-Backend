// Package app wires the bank service: config → dependencies → servers.
package app

import (
	"context"
	"errors"
	"fmt"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/bank/v1"
	exchangepb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/exchange/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/config"
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
		BankCode:     config.String("BANK_CODE", "265"),
		Branch:       config.String("BANK_BRANCH", "0001"),
		FXCommission: config.String("BANK_FX_COMMISSION", "0.005"),
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

	// Bring up the bank-owned house accounts before serving traffic so
	// the FX flow can always look them up. Idempotent — only inserts
	// missing currencies.
	if err := svc.EnsureSystemAccounts(ctx); err != nil {
		return fmt.Errorf("seed system accounts: %w", err)
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
			bankpb.RegisterBankServiceServer(s, server.New(svc))
		})
	})

	probeSrv.MarkReady()
	log.Info("bank service ready", "grpc", grpcAddr)

	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
