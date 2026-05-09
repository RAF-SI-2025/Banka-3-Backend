package router

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/user/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/verification"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/gateway/internal/auth"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc/status"
)

// Router holds dependencies shared across HTTP handlers.
type Router struct {
	Users           userpb.UserServiceClient
	AuthMW          func(http.Handler) http.Handler
	IdempotencyMW   func(http.Handler) http.Handler
	VerificationMW  func(http.Handler) http.Handler
	Verifier        verification.Verifier
	SecureCookies   bool
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

	return withCORS(mux), nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

type errBody struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, errBody{Code: code, Message: msg})
}

// writeGRPCError translates a gRPC status to an HTTP error JSON body.
func writeGRPCError(w http.ResponseWriter, err error) {
	st, ok := status.FromError(err)
	if !ok || st == nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	httpCode := runtime.HTTPStatusFromCode(st.Code())
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
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key, X-Verification-Id, X-Verification-Code")
			w.Header().Set("Access-Control-Max-Age", "300")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// auth and errors imports are referenced indirectly (auth via the
// outer app wiring; errors by writeGRPCError-adjacent code below).
// Surface them so an unused-import lint doesn't bite if a refactor
// drops their last visible call site.
var (
	_ = auth.Middleware
	_ = errors.New
)
