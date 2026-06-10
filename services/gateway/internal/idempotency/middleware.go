// Package idempotency hosts the gateway's HTTP middleware that honours
// the Idempotency-Key header on mutating requests. Cache logic lives
// in pkg/idempotency; this package adapts it to net/http.
package idempotency

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	pkgauth "github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	pkgidem "github.com/RAF-SI-2025/Banka-3-Backend/pkg/idempotency"
)

// Cache is the subset of pkg/idempotency.Cache the middleware needs;
// declared as an interface so tests can plug in a fake.
type Cache interface {
	Get(ctx context.Context, userID, key string) (*pkgidem.Entry, error)
	Set(ctx context.Context, userID, key string, e *pkgidem.Entry) error
}

// HeaderName is the request header carrying the idempotency key. The
// FE's axios interceptor stamps a fresh UUID v4 on every mutation
// (per the per-repo CLAUDE.md), so the gateway can rely on it.
const HeaderName = "Idempotency-Key"

// ReplayedHeaderName is set on replayed responses so the client can
// tell a fresh response from a cached one. Useful for debugging and
// analytics; ignore for normal logic.
const ReplayedHeaderName = "Idempotent-Replayed"

// MaxKeyLen rejects absurdly long keys outright. UUID v4 is 36 bytes;
// 200 leaves room for any reasonable schema.
const MaxKeyLen = 200

// replayedHeaders is the allowlist of response headers we copy onto
// replayed responses. Restricted to representational metadata; we
// deliberately drop Set-Cookie, Authorization, etc.
var replayedHeaders = []string{
	"Content-Type",
	"Content-Disposition",
	"Cache-Control",
}

// Middleware returns an HTTP middleware that:
//
//   - For non-mutating requests (GET/HEAD/OPTIONS): pass through.
//   - For mutating requests with no Idempotency-Key: pass through.
//   - For mutating requests with a key: check the cache. On hit,
//     replay the recorded response. On miss, run the handler, capture
//     the response, and on 2xx success cache it under the key.
//
// The cache key is scoped to (userID, key) so two clients can't
// collide; userID is the principal already attached by the auth
// middleware, or "anon" for public paths (activate / password-reset).
func Middleware(cache Cache, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isMutating(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			key := strings.TrimSpace(r.Header.Get(HeaderName))
			if key == "" {
				next.ServeHTTP(w, r)
				return
			}
			if len(key) > MaxKeyLen {
				log.WarnContext(r.Context(), "idempotency key rejected: too long",
					"key_len", len(key), "method", r.Method, "path", r.URL.Path)
				http.Error(w, "Idempotency-Key too long", http.StatusBadRequest)
				return
			}

			userID := principalUserID(r.Context())

			// Cache lookup. A redis-down failure shouldn't block the
			// request — log and proceed with the live handler.
			if entry, err := cache.Get(r.Context(), userID, key); err == nil {
				replay(w, entry)
				return
			} else if !errors.Is(err, pkgidem.ErrMiss) {
				log.WarnContext(r.Context(), "idempotency: cache get failed; bypassing",
					"err", err, "user", userID, "method", r.Method, "path", r.URL.Path)
			}

			// Capture the response, run the handler, and cache on 2xx.
			cw := &captureWriter{ResponseWriter: w}
			next.ServeHTTP(cw, r)

			if cw.status >= 200 && cw.status < 300 {
				e := &pkgidem.Entry{
					Status:  cw.status,
					Headers: cloneAllowed(cw.Header()),
					Body:    cw.body.Bytes(),
				}
				if err := cache.Set(r.Context(), userID, key, e); err != nil {
					log.WarnContext(r.Context(), "idempotency: cache set failed",
						"err", err, "user", userID, "method", r.Method, "path", r.URL.Path)
				}
			}
		})
	}
}

func isMutating(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

func principalUserID(ctx context.Context) string {
	if p, ok := pkgauth.PrincipalFrom(ctx); ok && p.UserID != "" {
		return p.UserID
	}
	return "anon"
}

// captureWriter wraps http.ResponseWriter to mirror the response into
// an in-memory buffer. The actual bytes still flow through to the
// client — caching is a side-effect.
type captureWriter struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (c *captureWriter) WriteHeader(s int) {
	if c.status == 0 {
		c.status = s
	}
	c.ResponseWriter.WriteHeader(s)
}

func (c *captureWriter) Write(b []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	c.body.Write(b)
	return c.ResponseWriter.Write(b)
}

// cloneAllowed copies the allowlisted headers out of h into a fresh
// map suitable for JSON-serialising into the cache.
func cloneAllowed(h http.Header) map[string][]string {
	out := make(map[string][]string, len(replayedHeaders))
	for _, name := range replayedHeaders {
		if vs := h.Values(name); len(vs) > 0 {
			out[name] = append([]string(nil), vs...)
		}
	}
	return out
}

func replay(w http.ResponseWriter, e *pkgidem.Entry) {
	for k, vs := range e.Headers {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set(ReplayedHeaderName, "true")
	status := e.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(e.Body)
}
