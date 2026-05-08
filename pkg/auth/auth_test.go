package auth

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var testKey = []byte("test-key-do-not-use-in-prod")

func TestSignVerifyRoundTrip(t *testing.T) {
	in := Claims{
		UserID:         "u-1",
		UserKind:       KindEmployee,
		Permissions:    []string{"admin", "employee.read"},
		SessionVersion: 7,
	}
	tok, err := Sign(in, testKey, time.Minute)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	out, err := Verify(tok, testKey)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if out.UserID != in.UserID || out.UserKind != in.UserKind || out.SessionVersion != in.SessionVersion {
		t.Fatalf("claims roundtrip mismatch: in=%+v out=%+v", in, out)
	}
	if len(out.Permissions) != len(in.Permissions) || out.Permissions[0] != in.Permissions[0] {
		t.Fatalf("permissions lost: in=%v out=%v", in.Permissions, out.Permissions)
	}
}

func TestVerifyRejectsExpired(t *testing.T) {
	tok, err := Sign(Claims{UserID: "u-1", UserKind: KindEmployee}, testKey, -time.Minute)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	_, err = Verify(tok, testKey)
	if err == nil {
		t.Fatal("expected error for expired token")
	}
	if !errors.Is(err, jwt.ErrTokenExpired) {
		t.Fatalf("expected jwt.ErrTokenExpired, got %v", err)
	}
}

func TestVerifyRejectsWrongKey(t *testing.T) {
	tok, _ := Sign(Claims{UserID: "u-1", UserKind: KindEmployee}, testKey, time.Minute)
	_, err := Verify(tok, []byte("different-key"))
	if err == nil {
		t.Fatal("expected error for wrong key")
	}
}

func TestVerifyRejectsMalformed(t *testing.T) {
	cases := []string{"", "not.a.jwt", "abc.def"}
	for _, c := range cases {
		c := c
		t.Run(c, func(t *testing.T) {
			if _, err := Verify(c, testKey); err == nil {
				t.Fatal("expected error for malformed token")
			}
		})
	}
}

func TestVerifyRejectsAlgNone(t *testing.T) {
	// An attacker hand-crafts a JWT with alg=none. Verify must refuse.
	noneTok := jwt.NewWithClaims(jwt.SigningMethodNone, &Claims{
		UserID: "u-1", UserKind: KindEmployee,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	})
	signed, err := noneTok.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign none: %v", err)
	}
	if _, err := Verify(signed, testKey); err == nil {
		t.Fatal("Verify must reject alg=none tokens")
	} else if !strings.Contains(err.Error(), "unexpected signing method") &&
		!strings.Contains(err.Error(), "signature is invalid") &&
		!errors.Is(err, jwt.ErrTokenSignatureInvalid) {
		// jwt-go returns various errors for this; just ensure something rejected it.
		t.Logf("rejection error (acceptable): %v", err)
	}
}

func TestPrincipalContext(t *testing.T) {
	ctx := context.Background()
	if _, ok := PrincipalFrom(ctx); ok {
		t.Fatal("empty ctx should have no principal")
	}
	p := Principal{UserID: "u-1", UserKind: KindEmployee, Permissions: []string{"admin"}, SessionVersion: 3}
	ctx = WithPrincipal(ctx, p)
	got, ok := PrincipalFrom(ctx)
	if !ok {
		t.Fatal("PrincipalFrom should succeed after WithPrincipal")
	}
	if !reflect.DeepEqual(got, p) {
		t.Fatalf("principal mismatch: got %+v want %+v", got, p)
	}
}
