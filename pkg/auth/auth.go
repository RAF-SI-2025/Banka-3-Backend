// Package auth signs and verifies JWTs and exposes the request-scoped
// principal. Access tokens are signed HS256 with HMAC; refresh tokens
// are not JWTs (opaque random bytes; storage and validation are the
// user service's job).
package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// UserKind discriminates the two top-level user types in the system.
type UserKind string

const (
	KindEmployee UserKind = "employee"
	KindClient   UserKind = "client"
)

// Claims is the access-token payload carried in the JWT. Permissions are
// the canonical permission strings (see pkg/permissions). SessionVersion
// is the user's current value at issue time; the gateway compares it to
// the live value in Redis on each request.
type Claims struct {
	UserID         string   `json:"sub"`
	UserKind       UserKind `json:"kind"`
	Permissions    []string `json:"perms"`
	SessionVersion int64    `json:"sv"`
	jwt.RegisteredClaims
}

// Sign returns a signed access token for c, valid for ttl from now.
func Sign(c Claims, key []byte, ttl time.Duration) (string, error) {
	now := time.Now().UTC()
	c.RegisteredClaims = jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		NotBefore: jwt.NewNumericDate(now),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
	signed, err := tok.SignedString(key)
	if err != nil {
		return "", fmt.Errorf("sign jwt: %w", err)
	}
	return signed, nil
}

// Verify parses and verifies an access token. It does NOT check
// session_version against Redis — that's the gateway middleware's job.
func Verify(token string, key []byte) (Claims, error) {
	var c Claims
	parsed, err := jwt.ParseWithClaims(token, &c, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return key, nil
	})
	if err != nil {
		return Claims{}, err
	}
	if !parsed.Valid {
		return Claims{}, errors.New("invalid token")
	}
	return c, nil
}

// Principal is the authenticated user attached to a request context by
// the gateway middleware. Services receive it via gRPC metadata and read
// it through [PrincipalFrom].
type Principal struct {
	UserID         string
	UserKind       UserKind
	Permissions    []string
	SessionVersion int64
}

type principalCtxKey struct{}

// WithPrincipal returns ctx with p stored on it.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalCtxKey{}, p)
}

// PrincipalFrom returns the principal from ctx, or false if absent.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalCtxKey{}).(Principal)
	return p, ok
}
