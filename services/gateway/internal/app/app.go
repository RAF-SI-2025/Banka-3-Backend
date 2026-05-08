// Package app wires the gateway: env config → upstream gRPC clients →
// HTTP server with REST mux + auth middleware + probes.
package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	pkgauth "github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/config"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/probes"
	pkgredis "github.com/RAF-SI-2025/Banka-3-Backend/pkg/redis"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/sessionversion"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/shutdown"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/bank/v1"
	exchangepb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/exchange/v1"
	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/user/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/gateway/internal/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/gateway/internal/clients"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/gateway/internal/router"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/metadata"
)

// Run blocks until shutdown.
func Run() error {
	log := logger.New("gateway")

	ctx, cancel := shutdown.Context()
	defer cancel()

	cs, err := clients.Dial(clients.Addrs{
		User:         config.MustString("USER_GRPC_ADDR"),
		Bank:         config.String("BANK_GRPC_ADDR", ""),
		Trading:      config.String("TRADING_GRPC_ADDR", ""),
		Exchange:     config.String("EXCHANGE_GRPC_ADDR", ""),
		Notification: config.String("NOTIFICATION_GRPC_ADDR", ""),
	})
	if err != nil {
		return fmt.Errorf("dial upstreams: %w", err)
	}
	defer cs.Close()

	rdb, err := pkgredis.Open(ctx, config.MustString("REDIS_ADDR"), config.String("REDIS_PASSWORD", ""))
	if err != nil {
		return fmt.Errorf("redis: %w", err)
	}
	defer rdb.Close()

	sessionCache := &sessionversion.Cache{
		R:   rdb,
		TTL: 30 * time.Second,
	}

	authMW := auth.Middleware(auth.Config{
		JWTKey:         []byte(config.MustString("JWT_SIGNING_KEY")),
		SessionCache:   sessionCache,
		UserClient:     cs.User,
		PublicPrefixes: router.PublicPrefixes(),
	})

	r := &router.Router{
		Users:         cs.User,
		AuthMW:        authMW,
		SecureCookies: config.Bool("SECURE_COOKIES", false),
	}

	// Annotator forwards the authenticated principal (set on the request
	// context by the auth middleware) to gRPC services as outgoing
	// metadata. Without this, grpc-gateway's runtime builds metadata only
	// from HTTP headers and our principal never reaches the service.
	gwMux := runtime.NewServeMux(
		runtime.WithMetadata(func(ctx context.Context, _ *http.Request) metadata.MD {
			p, ok := pkgauth.PrincipalFrom(ctx)
			if !ok {
				return nil
			}
			return metadata.Pairs(
				pkgauth.MDUserID, p.UserID,
				pkgauth.MDUserKind, string(p.UserKind),
				pkgauth.MDPermissions, strings.Join(p.Permissions, ","),
				pkgauth.MDSessionVersion, strconv.FormatInt(p.SessionVersion, 10),
			)
		}),
	)
	registerGW := func(ctx context.Context, mux *runtime.ServeMux) error {
		if err := userpb.RegisterUserServiceHandler(ctx, mux, cs.UserConn); err != nil {
			return err
		}
		if cs.BankConn != nil {
			if err := bankpb.RegisterBankServiceHandler(ctx, mux, cs.BankConn); err != nil {
				return err
			}
		}
		if cs.ExchangeConn != nil {
			if err := exchangepb.RegisterExchangeServiceHandler(ctx, mux, cs.ExchangeConn); err != nil {
				return err
			}
		}
		return nil
	}
	httpHandler, err := r.Mount(ctx, gwMux, registerGW)
	if err != nil {
		return fmt.Errorf("mount router: %w", err)
	}

	probeSrv := probes.New(fmt.Sprintf(":%d", config.Int("PROBE_PORT", 8081)))
	probeSrv.Register("redis", func(ctx context.Context) error { return pkgredis.Ping(ctx, rdb) })

	httpAddr := fmt.Sprintf(":%d", config.Int("HTTP_PORT", 8080))
	httpSrv := &http.Server{
		Addr:              httpAddr,
		Handler:           httpHandler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		return probeSrv.ListenAndServe(gctx)
	})
	g.Go(func() error {
		log.Info("http listening", "addr", httpAddr)
		errCh := make(chan error, 1)
		go func() {
			err := httpSrv.ListenAndServe()
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- err
				return
			}
			errCh <- nil
		}()
		select {
		case <-gctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			return httpSrv.Shutdown(shutdownCtx)
		case err := <-errCh:
			return err
		}
	})

	probeSrv.MarkReady()
	log.Info("gateway ready")

	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

