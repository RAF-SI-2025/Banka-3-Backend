package router

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/user/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/gateway/internal/auth"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc/status"
)

// Router holds dependencies shared across HTTP handlers.
type Router struct {
	Users         userpb.UserServiceClient
	AuthMW        func(http.Handler) http.Handler
	SecureCookies bool
}

// Mount returns the gateway's top-level handler. Public auth endpoints
// are registered explicitly; everything else is delegated to the
// grpc-gateway runtime (which is wrapped in the auth middleware).
func (r *Router) Mount(ctx context.Context, gwMux *runtime.ServeMux, registerGW func(context.Context, *runtime.ServeMux) error) (http.Handler, error) {
	if err := registerGW(ctx, gwMux); err != nil {
		return nil, err
	}

	mux := http.NewServeMux()

	// Public auth endpoints with cookie handling.
	mux.HandleFunc("POST /api/v1/auth/login", r.LoginHandler())
	mux.HandleFunc("POST /api/v1/auth/refresh", r.RefreshHandler())
	mux.HandleFunc("POST /api/v1/auth/logout", r.LogoutHandler())

	// Everything else (activation, password reset, employees, clients,
	// /me, etc.) goes through grpc-gateway under the auth middleware.
	mux.Handle("/api/", r.AuthMW(gwMux))

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
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key")
			w.Header().Set("Access-Control-Max-Age", "300")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Compile-time check we use the auth import even when middleware is the
// only consumer.
var _ = auth.Middleware

// Compile-time check that errors is referenced (used in writeGRPCError
// fallback in a future revision).
var _ = errors.New
