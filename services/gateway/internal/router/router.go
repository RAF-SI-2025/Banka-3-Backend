package router

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/user/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/clock"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/verification"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc/status"
)

// Router holds dependencies shared across HTTP handlers.
type Router struct {
	Users          userpb.UserServiceClient
	AuthMW         func(http.Handler) http.Handler
	IdempotencyMW  func(http.Handler) http.Handler
	VerificationMW func(http.Handler) http.Handler
	Verifier       verification.Verifier
	SecureCookies  bool
	// Clock drives the QA debug endpoint POST /api/v1/_debug/clock.
	// Wired only when CLOCK_DEBUG=true; the endpoint isn't registered
	// otherwise so production traffic can't accidentally reach it.
	Clock *clock.Adjustable
	// PartnerOTC enables the celina-5 inbound REST surface at
	// /bank/api/v1/otc/... Nil when INTERBANK_API_KEY isn't configured;
	// the routes simply aren't registered.
	PartnerOTC *PartnerOTC
	// PartnerPayments enables the celina-5 inbound 2PC surface at
	// /bank/api/v1/interbank/transactions/... Same nil-skip behaviour.
	PartnerPayments *PartnerPayments
	// PartnerBanka2 enables the Banka-2 dialect inbound shim — root-
	// mounted /interbank + /negotiations + /public-stock paths plus the
	// /bank/api/v1/interbank/user/{rn}/{id} friendly-name lookup. Same
	// nil-skip behaviour.
	PartnerBanka2 *PartnerBanka2
}

// Mount returns the gateway's top-level handler. Public auth endpoints
// are registered explicitly; everything else is delegated to the
// grpc-gateway runtime (which is wrapped in the auth middleware).
//
// Middleware order on the /api/ path is auth → verification →
// idempotency → gwMux. Auth attaches the principal; verification
// consumes the X-Verification-* headers on routes flagged in the
// rule table (payments, transfers, limit changes, card issuance);
// idempotency replays cached 2xx responses; the grpc-gateway runtime
// dispatches to the upstream service. Login / refresh / logout bypass
// the chain — they must always re-execute (a cached login response
// would replay a stale access token).
func (r *Router) Mount(ctx context.Context, gwMux *runtime.ServeMux, registerGW func(context.Context, *runtime.ServeMux) error) (http.Handler, error) {
	if err := registerGW(ctx, gwMux); err != nil {
		return nil, err
	}

	mux := http.NewServeMux()

	// Public auth endpoints with cookie handling.
	mux.HandleFunc("POST /api/v1/auth/login", r.LoginHandler())
	mux.HandleFunc("POST /api/v1/auth/refresh", r.RefreshHandler())
	mux.HandleFunc("POST /api/v1/auth/logout", r.LogoutHandler())

	// Verification: code-issue endpoint is gated by auth (so we know
	// who's asking) but does not itself need a verification code.
	if r.Verifier != nil {
		mux.Handle("POST /api/v1/verification/request", r.AuthMW(http.HandlerFunc(r.VerificationHandler())))
		// Additive: mobile polls this for the active codes to display
		// (spec p.84). Auth-gated so we know whose codes to list.
		mux.Handle("GET /api/v1/verification/pending", r.AuthMW(http.HandlerFunc(r.VerificationPendingHandler())))
		// Additive: mobile quick-approve (todoSpec S12). Marks the
		// caller's own pending record approved so the next gated request
		// passes with X-Verification-Id only. Auth-gated.
		mux.Handle("POST /api/v1/verification/{id}/approve", r.AuthMW(http.HandlerFunc(r.VerificationApproveHandler())))
		// Additive: mobile "Ignore" action (spec p.84 mode 2). Retires the
		// caller's own pending record so the gated web action fails
		// verification; records the request as unsuccessful. Auth-gated.
		mux.Handle("POST /api/v1/verification/{id}/reject", r.AuthMW(http.HandlerFunc(r.VerificationRejectHandler())))
		// Additive: web poll-mode dialog reads pending|approved|expired
		// for a single id to know when the phone approved (todoSpec S12).
		mux.Handle("GET /api/v1/verification/{id}/status", r.AuthMW(http.HandlerFunc(r.VerificationStatusHandler())))
		// Additive: mobile "Verifikacija" screen request history, each
		// row marked successful/unsuccessful (spec p.84). Durable —
		// backed by the user service, survives the Redis code TTL.
		mux.Handle("GET /api/v1/verification/history", r.AuthMW(http.HandlerFunc(r.VerificationHistoryHandler())))
	}

	// Everything else (activation, password reset, employees, clients,
	// /me, etc.) goes through grpc-gateway under auth + verification +
	// idempotency.
	apiHandler := http.Handler(gwMux)
	if r.IdempotencyMW != nil {
		apiHandler = r.IdempotencyMW(apiHandler)
	}
	if r.VerificationMW != nil {
		apiHandler = r.VerificationMW(apiHandler)
	}
	mux.Handle("/api/", r.AuthMW(apiHandler))

	// Plain ping so dev can sanity-check the gateway.
	mux.HandleFunc("GET /api/v1/ping", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// Celina 5 — partner-facing inbound REST. Lives under /bank/, not
	// /api/, and is auth'd by X-Api-Key inside the handlers (no JWT).
	r.PartnerOTC.MountPartnerOTC(mux)
	r.PartnerPayments.MountPartnerPayments(mux)
	r.PartnerBanka2.MountPartnerBanka2(mux)

	// QA-only clock-offset endpoint (spec edge: S7 23:59 daily reset,
	// monthly tax, loan installments — all driven by cron schedules
	// that QA can't wait real seconds for). Registered only when
	// CLOCK_DEBUG=true so production traffic can't reach it. Admin-
	// gated inside the handler; the AuthMW upstream attaches the
	// principal. Cross-service propagation is via the Redis key the
	// pkg/clock.Adjustable.StartRefresher goroutine polls.
	if r.Clock != nil && r.Clock.Enabled() {
		mux.Handle("POST /api/v1/_debug/clock", r.AuthMW(http.HandlerFunc(r.DebugClockSetHandler())))
		mux.Handle("GET /api/v1/_debug/clock", r.AuthMW(http.HandlerFunc(r.DebugClockGetHandler())))
	}

	return withCORS(mux), nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		// Almost always a client that hung up mid-response; no request
		// context here, so the default (service) logger carries it.
		slog.Default().Warn("response write failed", "err", err, "status", status)
	}
}

// DebugClockSetHandler handles POST /api/v1/_debug/clock with body
// {"offset":"24h"} (any time.ParseDuration string). Admin-only. Writes
// to Redis via the Adjustable; other services pick it up within
// pkg/clock.RefreshInterval.
func (r *Router) DebugClockSetHandler() http.HandlerFunc {
	type req struct {
		Offset string `json:"offset"`
	}
	return func(w http.ResponseWriter, httpReq *http.Request) {
		ctx := httpReq.Context()
		log := logger.From(ctx)
		p, ok := auth.PrincipalFrom(ctx)
		if !ok || !permissions.Has(p.Permissions, permissions.Admin) {
			log.WarnContext(ctx, "debug clock set rejected: admin only",
				"path", httpReq.URL.Path)
			writeJSON(w, http.StatusForbidden, errBody{Code: http.StatusForbidden, Message: "admin only"})
			return
		}
		var in req
		if err := json.NewDecoder(httpReq.Body).Decode(&in); err != nil {
			log.WarnContext(ctx, "debug clock set rejected: invalid body", "err", err)
			writeJSON(w, http.StatusBadRequest, errBody{Code: http.StatusBadRequest, Message: "invalid body"})
			return
		}
		d, err := time.ParseDuration(in.Offset)
		if err != nil {
			log.WarnContext(ctx, "debug clock set rejected: bad offset", "err", err)
			writeJSON(w, http.StatusBadRequest, errBody{Code: http.StatusBadRequest, Message: "offset: " + err.Error()})
			return
		}
		if err := r.Clock.SetOffset(ctx, d); err != nil {
			log.ErrorContext(ctx, "debug clock offset persist failed",
				"err", err, "offset", d.String())
			writeJSON(w, http.StatusInternalServerError, errBody{Code: http.StatusInternalServerError, Message: err.Error()})
			return
		}
		log.InfoContext(ctx, "debug clock offset set",
			"offset", d.String(), "user_id", p.UserID)
		writeJSON(w, http.StatusOK, map[string]any{
			"offset": d.String(),
			"now":    r.Clock.Now().Format(time.RFC3339),
		})
	}
}

// DebugClockGetHandler returns the current offset + adjusted now.
// Auth-gated (anyone authenticated can read; the offset isn't a
// secret), but only registered when CLOCK_DEBUG=true.
func (r *Router) DebugClockGetHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"offset": r.Clock.Offset().String(),
			"now":    r.Clock.Now().Format(time.RFC3339),
		})
	}
}

type errBody struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, errBody{Code: code, Message: msg})
}

// writeGRPCError translates a gRPC status to an HTTP error JSON body.
// It is the router package's gRPC→HTTP translation choke point, so it
// also emits the structured log line for the failure: Error for 5xx
// (upstream/internal), Warn for client-class 4xx.
func writeGRPCError(w http.ResponseWriter, r *http.Request, err error) {
	ctx := r.Context()
	log := logger.From(ctx)
	st, ok := status.FromError(err)
	if !ok || st == nil {
		log.ErrorContext(ctx, "upstream call failed",
			"err", err, "method", r.Method, "path", r.URL.Path)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	httpCode := runtime.HTTPStatusFromCode(st.Code())
	if httpCode >= 500 {
		log.ErrorContext(ctx, "upstream call failed",
			"err", err, "code", st.Code().String(), "status", httpCode,
			"method", r.Method, "path", r.URL.Path)
	} else {
		log.WarnContext(ctx, "upstream call rejected",
			"err", err, "code", st.Code().String(), "status", httpCode,
			"method", r.Method, "path", r.URL.Path)
	}
	writeError(w, httpCode, st.Message())
}

// PublicPrefixes lists request-path prefixes that bypass the auth
// middleware. Everything outside this list requires a valid bearer
// token.
func PublicPrefixes() []string {
	return []string{
		"/api/v1/auth/login",
		"/api/v1/auth/refresh",
		"/api/v1/auth/logout",
		"/api/v1/auth/activate",
		"/api/v1/auth/password-reset",
		"/api/v1/ping",
		// Celina 5 — partner-facing REST. JWT does not apply; the
		// handlers run X-Api-Key inside.
		"/bank/",
	}
}

// withCORS handles preflight and adds permissive CORS headers for dev.
// Tighten in prod via env config.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Vary", "Origin")
		origin := r.Header.Get("Origin")
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			// traceparent / tracestate / baggage are the W3C Trace
			// Context headers Faro's TracingInstrumentation sets on
			// every XHR; without them in the allow-list the browser
			// strips them on cross-origin requests and frontend spans
			// orphan from the backend trace in Tempo.
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key, X-Verification-Id, X-Verification-Code, traceparent, tracestate, baggage")
			w.Header().Set("Access-Control-Max-Age", "300")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
