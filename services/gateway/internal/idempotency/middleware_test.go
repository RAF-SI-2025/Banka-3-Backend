package idempotency

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	pkgauth "github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	pkgidem "github.com/RAF-SI-2025/Banka-3-Backend/pkg/idempotency"
)

// fakeCache is an in-memory Cache for the middleware tests. It's not
// goroutine-safe — none of the tests run concurrent requests through
// it.
type fakeCache struct {
	store map[string]*pkgidem.Entry
	gets  atomic.Int64
	sets  atomic.Int64
	// failNext, when true, makes the next Get return a non-Miss error
	// (simulating Redis going away). Reset to false after the call.
	failNext bool
}

func newFakeCache() *fakeCache { return &fakeCache{store: map[string]*pkgidem.Entry{}} }

func (f *fakeCache) key(u, k string) string { return u + ":" + k }

func (f *fakeCache) Get(_ context.Context, u, k string) (*pkgidem.Entry, error) {
	f.gets.Add(1)
	if f.failNext {
		f.failNext = false
		return nil, errors.New("redis exploded")
	}
	if e, ok := f.store[f.key(u, k)]; ok {
		return e, nil
	}
	return nil, pkgidem.ErrMiss
}

func (f *fakeCache) Set(_ context.Context, u, k string, e *pkgidem.Entry) error {
	f.sets.Add(1)
	if _, exists := f.store[f.key(u, k)]; exists {
		return nil // SetNX semantics
	}
	f.store[f.key(u, k)] = e
	return nil
}

// silentLogger discards everything; we don't want test output cluttered
// with "cache get failed" lines.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// withPrincipal returns a request whose context carries an authenticated
// principal — what the auth middleware would have attached.
func withPrincipal(req *http.Request, userID string) *http.Request {
	ctx := pkgauth.WithPrincipal(req.Context(), pkgauth.Principal{UserID: userID, UserKind: pkgauth.KindClient})
	return req.WithContext(ctx)
}

func TestMiddleware_GETPassesThrough(t *testing.T) {
	cache := newFakeCache()
	mw := Middleware(cache, silentLogger())

	calls := 0
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/api/v1/x", nil)
	req.Header.Set(HeaderName, "abc")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if calls != 1 {
		t.Errorf("calls: %d, want 1", calls)
	}
	if cache.gets.Load() != 0 {
		t.Errorf("Get called on GET: %d", cache.gets.Load())
	}
}

func TestMiddleware_NoKeyPassesThrough(t *testing.T) {
	cache := newFakeCache()
	mw := Middleware(cache, silentLogger())

	calls := 0
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(201)
	}))

	req := httptest.NewRequest("POST", "/api/v1/x", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if calls != 1 || cache.gets.Load() != 0 {
		t.Errorf("expected straight-through; calls=%d gets=%d", calls, cache.gets.Load())
	}
}

func TestMiddleware_FirstHitCaches_SecondHitReplays(t *testing.T) {
	cache := newFakeCache()
	mw := Middleware(cache, silentLogger())

	calls := atomic.Int64{}
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"id":1}`))
	}))

	// First request — handler runs, response cached.
	req1 := withPrincipal(httptest.NewRequest("POST", "/api/v1/payments", strings.NewReader(`{"amount":1}`)), "user-1")
	req1.Header.Set(HeaderName, "key-A")
	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, req1)

	if calls.Load() != 1 {
		t.Fatalf("first call: handler invocations = %d, want 1", calls.Load())
	}
	if rec1.Code != 201 {
		t.Errorf("first status: %d", rec1.Code)
	}
	if got := rec1.Header().Get(ReplayedHeaderName); got != "" {
		t.Errorf("first response should not be flagged replayed: %q", got)
	}

	// Second request — same user, same key. Handler must NOT run.
	req2 := withPrincipal(httptest.NewRequest("POST", "/api/v1/payments", strings.NewReader(`{"amount":1}`)), "user-1")
	req2.Header.Set(HeaderName, "key-A")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)

	if calls.Load() != 1 {
		t.Fatalf("second call: handler invocations = %d, want still 1 (replay)", calls.Load())
	}
	if rec2.Code != 201 {
		t.Errorf("replay status: %d, want 201", rec2.Code)
	}
	if rec2.Body.String() != `{"id":1}` {
		t.Errorf("replay body: %q", rec2.Body.String())
	}
	if rec2.Header().Get(ReplayedHeaderName) != "true" {
		t.Errorf("Idempotent-Replayed header missing on replay")
	}
	if rec2.Header().Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type lost on replay: %q", rec2.Header().Get("Content-Type"))
	}
}

func TestMiddleware_PerUserScoping(t *testing.T) {
	cache := newFakeCache()
	mw := Middleware(cache, silentLogger())

	calls := atomic.Int64{}
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(200)
	}))

	for _, user := range []string{"alice", "bob"} {
		req := withPrincipal(httptest.NewRequest("POST", "/api/v1/x", nil), user)
		req.Header.Set(HeaderName, "shared-key")
		h.ServeHTTP(httptest.NewRecorder(), req)
	}

	if calls.Load() != 2 {
		t.Errorf("expected per-user isolation: handler ran %d times, want 2", calls.Load())
	}
}

func TestMiddleware_NonSuccessNotCached(t *testing.T) {
	cache := newFakeCache()
	mw := Middleware(cache, silentLogger())

	calls := atomic.Int64{}
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "boom", http.StatusInternalServerError)
	}))

	for i := 0; i < 2; i++ {
		req := withPrincipal(httptest.NewRequest("POST", "/api/v1/x", nil), "u")
		req.Header.Set(HeaderName, "k")
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	if calls.Load() != 2 {
		t.Errorf("5xx must not be cached: handler ran %d times, want 2", calls.Load())
	}
}

func TestMiddleware_TooLongKey(t *testing.T) {
	cache := newFakeCache()
	mw := Middleware(cache, silentLogger())
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	req := withPrincipal(httptest.NewRequest("POST", "/api/v1/x", nil), "u")
	req.Header.Set(HeaderName, strings.Repeat("a", MaxKeyLen+1))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: %d, want 400", rec.Code)
	}
}

func TestMiddleware_CacheGetErrorFallsThrough(t *testing.T) {
	cache := newFakeCache()
	cache.failNext = true
	mw := Middleware(cache, silentLogger())

	calls := atomic.Int64{}
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(201)
	}))
	req := withPrincipal(httptest.NewRequest("POST", "/api/v1/x", nil), "u")
	req.Header.Set(HeaderName, "k")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if calls.Load() != 1 || rec.Code != 201 {
		t.Errorf("Redis-down should not block request: calls=%d code=%d", calls.Load(), rec.Code)
	}
}
