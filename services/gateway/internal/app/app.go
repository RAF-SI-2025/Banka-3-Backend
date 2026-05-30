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
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/clock"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/config"
	pkgidem "github.com/RAF-SI-2025/Banka-3-Backend/pkg/idempotency"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/observability"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/probes"
	pkgredis "github.com/RAF-SI-2025/Banka-3-Backend/pkg/redis"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/sessionversion"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/shutdown"
	pkgverif "github.com/RAF-SI-2025/Banka-3-Backend/pkg/verification"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/bank/v1"
	exchangepb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/exchange/v1"
	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/trading/v1"
	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/user/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/gateway/internal/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/gateway/internal/clients"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/gateway/internal/idempotency"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/gateway/internal/router"
	gwverif "github.com/RAF-SI-2025/Banka-3-Backend/services/gateway/internal/verification"

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

	// Idempotency cache for the Idempotency-Key middleware. TTL matches
	// the typical e-commerce/payments retry window; tune via env when
	// real traffic patterns inform a better number.
	idemCache := &pkgidem.Cache{
		R:   rdb,
		TTL: config.Duration("IDEMPOTENCY_TTL", 24*time.Hour),
	}
	idemMW := idempotency.Middleware(idemCache, log)

	// Verification: spec p.11 verifikacioni-kod. The web flow still
	// returns the code in the HTTP response so the SPA renders it
	// inline; the mobile app (spec p.84) polls /verification/pending +
	// /verification/history instead. The RecordingVerifier wraps the
	// Redis primitive to durably persist each request + its outcome
	// via the user service (best-effort — a history hiccup never
	// breaks the gate). Middleware gates payments / transfers / limit
	// changes / card issuance + the c4 money-moving OTC/fund routes.
	verifier := &gwverif.RecordingVerifier{
		Inner: &pkgverif.Cache{R: rdb},
		Users: cs.User,
		Log:   log,
	}
	verifMW := gwverif.Middleware(verifier, gwverif.DefaultRules(), log)

	// QA-adjustable clock — only enabled with CLOCK_DEBUG=true. Wired
	// here so the gateway debug endpoint (POST /api/v1/_debug/clock)
	// can update the Redis-persisted offset that trading + bank
	// services observe via their own Adjustable instances.
	gwClock := clock.NewAdjustable(rdb, config.Bool("CLOCK_DEBUG", false))
	gwClock.StartRefresher(ctx)

	r := &router.Router{
		Users:          cs.User,
		AuthMW:         authMW,
		IdempotencyMW:  idemMW,
		VerificationMW: verifMW,
		Verifier:       verifier,
		SecureCookies:  config.Bool("SECURE_COOKIES", false),
		Clock:          gwClock,
	}

	// Celina 5 — wire the partner-facing inbound surface when an API key
	// is set + the trading service is reachable. Both must be set;
	// otherwise the /bank/api/v1/otc/... routes simply aren't registered.
	if apiKey := config.String("INTERBANK_API_KEY", ""); apiKey != "" {
		if cs.ExternalOTC != nil {
			r.PartnerOTC = &router.PartnerOTC{
				APIKey:            apiKey,
				TradingOTC:        cs.ExternalOTC,
				Trading:           cs.Trading,
				BankRoutingNumber: config.String("BANK_ROUTING_NUMBER", "333"),
			}
		}
		if cs.InterbankProtocol != nil {
			r.PartnerPayments = &router.PartnerPayments{
				APIKey:    apiKey,
				Interbank: cs.InterbankProtocol,
			}
		}
		// Banka-2 dialect inbound shim — re-uses the same shared API
		// key. Mounted only when its dependencies are reachable; otherwise
		// each handler self-checks and the surface is partially populated.
		r.PartnerBanka2 = &router.PartnerBanka2{
			APIKey:            apiKey,
			BankRoutingNumber: config.String("BANK_ROUTING_NUMBER", "333"),
			BankDisplayName:   config.String("BANK_DISPLAY_NAME", "Banka 3"),
			Users:             cs.User,
			Trading:           cs.Trading,
			TradingOTC:        cs.ExternalOTC,
			Interbank:         cs.InterbankProtocol,
		}
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
		// Rewrite gRPC Unavailable (a stopped upstream service, an
		// unresolvable hostname like "bank" when the bank container isn't
		// up, etc.) to a clean Serbian 503 instead of leaking the raw
		// dialer text ("name resolver error: produced zero addresses")
		// to the client.
		runtime.WithErrorHandler(unavailableFriendly()),
		// Forward the trading-debug fault-injection headers (X-Saga-*)
		// down to gRPC services as `grpcgateway-x-saga-*` metadata.
		// runtime.DefaultHeaderMatcher only forwards a small permanent
		// allow-list (Authorization, Content-Type, etc.) plus the
		// explicit `Grpc-Metadata-` prefix; without this matcher,
		// FaultsFromMetadata in services/trading/.../saga/saga.go
		// never sees the headers and SAGA_DEBUG_FAULT_INJECTION is a
		// silent no-op. Gated by SAGA_DEBUG_FAULT_INJECTION on the
		// trading side so production traffic is unaffected even with
		// the headers forwarded.
		runtime.WithIncomingHeaderMatcher(func(key string) (string, bool) {
			if strings.HasPrefix(strings.ToLower(key), "x-saga-") {
				return runtime.MetadataPrefix + strings.ToLower(key), true
			}
			return runtime.DefaultHeaderMatcher(key)
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
		if cs.TradingConn != nil {
			if err := tradingpb.RegisterTradingServiceHandler(ctx, mux, cs.TradingConn); err != nil {
				return err
			}
			// Celina 5 — external OTC REST routes ride the same trading conn.
			if err := tradingpb.RegisterExternalOTCServiceHandler(ctx, mux, cs.TradingConn); err != nil {
				return err
			}
			// Celina 5 — user-initiated cross-bank cash payment routes.
			if err := tradingpb.RegisterCrossBankPaymentServiceHandler(ctx, mux, cs.TradingConn); err != nil {
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
	probeSrv.MountMetrics(observability.New("gateway").MetricsHandler())

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
