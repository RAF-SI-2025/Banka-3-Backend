// Package auth implements the gateway's HTTP authentication middleware.
//
// On every request:
//
//  1. If the path is in the public allowlist, pass through.
//  2. Read the access token from `Authorization: Bearer <token>`.
//  3. Verify the JWT signature and expiry.
//  4. Look up the user's current session_version in Redis (falling back
//     to the user gRPC service on cache miss).
//  5. Reject if the JWT's `sv` claim is below the current value.
//  6. Attach the principal to the request context for downstream handlers,
//     and forward it as outgoing gRPC metadata via [pkgauth.AttachToOutgoing].
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"google.golang.org/grpc"

	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/user/v1"
	pkgauth "github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/sessionversion"
)

// SessionCache is the read+write surface the middleware needs from the
// session-version cache. *sessionversion.Cache satisfies it; tests stub
// it directly so the middleware can run without Redis.
type SessionCache interface {
	Current(ctx context.Context, kind, id string) (int64, error)
	Set(ctx context.Context, kind, id string, v int64) error
}

// SessionVersionLookup is the slice of UserServiceClient the middleware
// actually uses. The full generated client satisfies it; tests stub it
// without implementing the other ~18 RPCs.
type SessionVersionLookup interface {
	GetSessionVersion(ctx context.Context, in *userpb.GetSessionVersionRequest, opts ...grpc.CallOption) (*userpb.GetSessionVersionResponse, error)
}

// Config holds dependencies for the middleware.
type Config struct {
	JWTKey         []byte
	SessionCache   SessionCache
	UserClient     SessionVersionLookup
	PublicPrefixes []string // request paths that bypass auth
}

// Middleware wraps next with the auth checks described in the package
// doc.
func Middleware(cfg Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isPublic(r.URL.Path, cfg.PublicPrefixes) {
				next.ServeHTTP(w, r)
				return
			}

			tok := bearer(r)
			if tok == "" {
				writeAuthError(w, http.StatusUnauthorized, "missing access token")
				return
			}
			claims, err := pkgauth.Verify(tok, cfg.JWTKey)
			if err != nil {
				writeAuthError(w, http.StatusUnauthorized, "invalid access token")
				return
			}

			currentSV, err := currentSessionVersion(r.Context(), cfg, claims.UserKind, claims.UserID)
			if err != nil {
				writeAuthError(w, http.StatusServiceUnavailable, "session check failed")
				return
			}
			if claims.SessionVersion < currentSV {
				writeAuthError(w, http.StatusUnauthorized, "session revoked")
				return
			}

			p := pkgauth.Principal{
				UserID:         claims.UserID,
				UserKind:       claims.UserKind,
				Permissions:    claims.Permissions,
				SessionVersion: claims.SessionVersion,
			}
			ctx := pkgauth.WithPrincipal(r.Context(), p)
			ctx = pkgauth.AttachToOutgoing(ctx, p)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func isPublic(path string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(h[len("Bearer "):])
}

func currentSessionVersion(ctx context.Context, cfg Config, kind pkgauth.UserKind, userID string) (int64, error) {
	v, err := cfg.SessionCache.Current(ctx, string(kind), userID)
	if err == nil {
		return v, nil
	}
	if !errors.Is(err, sessionversion.ErrNotCached) {
		return 0, err
	}
	resp, err := cfg.UserClient.GetSessionVersion(ctx, &userpb.GetSessionVersionRequest{
		UserKind: userKindToProto(kind),
		UserId:   userID,
	})
	if err != nil {
		return 0, err
	}
	_ = cfg.SessionCache.Set(ctx, string(kind), userID, resp.GetSessionVersion())
	return resp.GetSessionVersion(), nil
}

func userKindToProto(k pkgauth.UserKind) userpb.UserKind {
	switch k {
	case pkgauth.KindEmployee:
		return userpb.UserKind_USER_KIND_EMPLOYEE
	case pkgauth.KindClient:
		return userpb.UserKind_USER_KIND_CLIENT
	}
	return userpb.UserKind_USER_KIND_UNSPECIFIED
}

type errBody struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func writeAuthError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errBody{Code: status, Message: msg})
}
