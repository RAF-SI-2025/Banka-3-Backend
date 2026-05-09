package verification

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/verification"
)

// stubVerifier is the in-memory test double for verification.Verifier.
type stubVerifier struct {
	consume func(id, code string, kind verification.ActionKind) error
}

func (s *stubVerifier) Issue(_ context.Context, _ string, _ verification.ActionKind) (string, string, time.Time, error) {
	return "stub-id", "stub-code", time.Now().Add(time.Minute), nil
}

func (s *stubVerifier) Consume(_ context.Context, id, code string, kind verification.ActionKind) error {
	return s.consume(id, code, kind)
}

func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
}

func TestMiddleware_PassesThroughUnflaggedRoutes(t *testing.T) {
	v := &stubVerifier{consume: func(string, string, verification.ActionKind) error { return errors.New("must not be called") }}
	mw := Middleware(v, DefaultRules(), discardLog())(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status: %d, want 200 (unflagged GET should pass through)", rec.Code)
	}
}

func TestMiddleware_RejectsMissingHeaders(t *testing.T) {
	v := &stubVerifier{consume: func(string, string, verification.ActionKind) error {
		t.Error("Consume should not be called when headers are missing")
		return nil
	}}
	mw := Middleware(v, DefaultRules(), discardLog())(okHandler())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: %d, want 401", rec.Code)
	}
}

func TestMiddleware_PassesOnConsumeOK(t *testing.T) {
	got := struct {
		id, code string
		kind     verification.ActionKind
	}{}
	v := &stubVerifier{consume: func(id, code string, kind verification.ActionKind) error {
		got.id, got.code, got.kind = id, code, kind
		return nil
	}}
	mw := Middleware(v, DefaultRules(), discardLog())(okHandler())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments", nil)
	req.Header.Set(HeaderID, "abc")
	req.Header.Set(HeaderCode, "123456")
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status: %d, want 200", rec.Code)
	}
	if got.id != "abc" || got.code != "123456" || got.kind != verification.ActionPayment {
		t.Errorf("Consume called with %+v, want (abc, 123456, payment)", got)
	}
}

func TestMiddleware_LimitChangePathPattern(t *testing.T) {
	called := false
	v := &stubVerifier{consume: func(_, _ string, kind verification.ActionKind) error {
		called = true
		if kind != verification.ActionLimitChange {
			t.Errorf("kind: %s, want limit_change", kind)
		}
		return nil
	}}
	mw := Middleware(v, DefaultRules(), discardLog())(okHandler())

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/accounts/a-b-c/limits", nil)
	req.Header.Set(HeaderID, "i")
	req.Header.Set(HeaderCode, "c")
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	if !called {
		t.Error("limits PATCH should match the limit_change rule")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status: %d", rec.Code)
	}
}

func TestMiddleware_MapsErrorsToStatusCodes(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"wrong code", verification.ErrWrongCode},
		{"too many", verification.ErrTooMany},
		{"not found", verification.ErrNotFound},
		{"mismatch", verification.ErrMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := &stubVerifier{consume: func(string, string, verification.ActionKind) error { return tc.err }}
			mw := Middleware(v, DefaultRules(), discardLog())(okHandler())
			req := httptest.NewRequest(http.MethodPost, "/api/v1/payments", nil)
			req.Header.Set(HeaderID, "i")
			req.Header.Set(HeaderCode, "c")
			rec := httptest.NewRecorder()
			mw.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status: %d, want 401", rec.Code)
			}
		})
	}
}

func TestMatchRule(t *testing.T) {
	rules := DefaultRules()
	cases := []struct {
		method, path string
		want         verification.ActionKind
		gated        bool
	}{
		{http.MethodPost, "/api/v1/payments", verification.ActionPayment, true},
		{http.MethodPost, "/api/v1/transfers", verification.ActionTransfer, true},
		{http.MethodPost, "/api/v1/cards", verification.ActionCardIssue, true},
		{http.MethodPatch, "/api/v1/accounts/123/limits", verification.ActionLimitChange, true},
		{http.MethodPatch, "/api/v1/cards/123/limit", verification.ActionLimitChange, true},
		{http.MethodPost, "/api/v1/menjacnica/quote", "", false},
		{http.MethodGet, "/api/v1/payments", "", false},
		{http.MethodPost, "/api/v1/payment-recipients", "", false},
		{http.MethodPost, "/api/v1/cards/123/status", "", false},
	}
	for _, tc := range cases {
		got, gated := matchRule(rules, tc.method, tc.path)
		if gated != tc.gated || got != tc.want {
			t.Errorf("%s %s → (%q, %v), want (%q, %v)", tc.method, tc.path, got, gated, tc.want, tc.gated)
		}
	}
}
