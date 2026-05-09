package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/grpc"

	pkgauth "github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/sessionversion"
	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/user/v1"
)

var testKey = []byte("test-signing-key-not-for-prod")

// fakeCache is an in-memory SessionCache that records calls so the
// cache-miss + populate path can be asserted.
type fakeCache struct {
	current map[string]int64 // missing key → ErrNotCached
	setLog  []setCall
	currErr error // forced error for Current; bypasses ErrNotCached path
}

type setCall struct {
	kind, id string
	v        int64
}

func (f *fakeCache) Current(_ context.Context, kind, id string) (int64, error) {
	if f.currErr != nil {
		return 0, f.currErr
	}
	v, ok := f.current[kind+":"+id]
	if !ok {
		return 0, sessionversion.ErrNotCached
	}
	return v, nil
}

func (f *fakeCache) Set(_ context.Context, kind, id string, v int64) error {
	f.setLog = append(f.setLog, setCall{kind, id, v})
	return nil
}

// fakeUserClient stubs the SessionVersionLookup slice of UserServiceClient.
type fakeUserClient struct {
	resp *userpb.GetSessionVersionResponse
	err  error
}

func (f *fakeUserClient) GetSessionVersion(_ context.Context, _ *userpb.GetSessionVersionRequest, _ ...grpc.CallOption) (*userpb.GetSessionVersionResponse, error) {
	return f.resp, f.err
}

// captureNext is a sentinel handler that records the principal it was
// invoked with so tests can assert authn forwarded the right thing.
type captureNext struct {
	called    bool
	principal pkgauth.Principal
}

func (c *captureNext) ServeHTTP(_ http.ResponseWriter, r *http.Request) {
	c.called = true
	if p, ok := pkgauth.PrincipalFrom(r.Context()); ok {
		c.principal = p
	}
}

func mintToken(t *testing.T, c pkgauth.Claims, ttl time.Duration) string {
	t.Helper()
	tok, err := pkgauth.Sign(c, testKey, ttl)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return tok
}

func newReq(method, path, bearer string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	return r
}

func decodeErr(t *testing.T, w *httptest.ResponseRecorder) errBody {
	t.Helper()
	var b errBody
	if err := json.NewDecoder(w.Body).Decode(&b); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	return b
}

// TestMiddleware_PublicPathBypassesAuth proves that paths matching a
// PublicPrefixes entry skip token parsing entirely — no call to either
// dependency.
func TestMiddleware_PublicPathBypassesAuth(t *testing.T) {
	cache := &fakeCache{current: map[string]int64{}}
	uc := &fakeUserClient{}
	next := &captureNext{}

	mw := Middleware(Config{
		JWTKey:         testKey,
		SessionCache:   cache,
		UserClient:     uc,
		PublicPrefixes: []string{"/api/v1/auth/"},
	})

	w := httptest.NewRecorder()
	mw(next).ServeHTTP(w, newReq(http.MethodPost, "/api/v1/auth/login", ""))

	if !next.called {
		t.Fatal("public path: next not invoked")
	}
	if len(cache.setLog) != 0 {
		t.Errorf("public path touched cache: %v", cache.setLog)
	}
	if w.Code != http.StatusOK {
		t.Errorf("status: %d, want 200", w.Code)
	}
}

// TestMiddleware_MissingBearerRejected covers the empty Authorization
// header path. The error body is JSON, status is 401.
func TestMiddleware_MissingBearerRejected(t *testing.T) {
	mw := Middleware(Config{
		JWTKey:       testKey,
		SessionCache: &fakeCache{},
		UserClient:   &fakeUserClient{},
	})
	next := &captureNext{}

	w := httptest.NewRecorder()
	mw(next).ServeHTTP(w, newReq(http.MethodGet, "/api/v1/me", ""))

	if next.called {
		t.Fatal("next was invoked despite missing token")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status: %d, want 401", w.Code)
	}
	if msg := decodeErr(t, w).Message; msg != "missing access token" {
		t.Errorf("body message: %q", msg)
	}
}

// TestMiddleware_GarbageBearerRejected covers a non-JWT string in the
// Authorization header — the JWT library returns a parse error and we
// surface "invalid access token".
func TestMiddleware_GarbageBearerRejected(t *testing.T) {
	mw := Middleware(Config{
		JWTKey:       testKey,
		SessionCache: &fakeCache{},
		UserClient:   &fakeUserClient{},
	})
	next := &captureNext{}

	w := httptest.NewRecorder()
	mw(next).ServeHTTP(w, newReq(http.MethodGet, "/api/v1/me", "not-a-token"))

	if next.called {
		t.Fatal("next was invoked despite garbage token")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status: %d, want 401", w.Code)
	}
	if msg := decodeErr(t, w).Message; msg != "invalid access token" {
		t.Errorf("body message: %q", msg)
	}
}

// TestMiddleware_WrongSigningKeyRejected proves the verify path actually
// validates the signature: a token signed with a different secret is
// rejected as "invalid access token".
func TestMiddleware_WrongSigningKeyRejected(t *testing.T) {
	mw := Middleware(Config{
		JWTKey:       testKey,
		SessionCache: &fakeCache{},
		UserClient:   &fakeUserClient{},
	})
	next := &captureNext{}

	bad, err := pkgauth.Sign(pkgauth.Claims{
		UserID: "user-1", UserKind: pkgauth.KindEmployee, Permissions: []string{"admin"}, SessionVersion: 1,
	}, []byte("a-different-key"), time.Minute)
	if err != nil {
		t.Fatalf("sign with rogue key: %v", err)
	}

	w := httptest.NewRecorder()
	mw(next).ServeHTTP(w, newReq(http.MethodGet, "/api/v1/me", bad))

	if next.called {
		t.Fatal("next was invoked despite wrong-key signature")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status: %d, want 401", w.Code)
	}
}

// TestMiddleware_StaleSessionVersionRejected proves the cache hit path:
// when Redis already has a higher session_version than the JWT carries,
// the request is rejected as "session revoked" without dialling the
// user service.
func TestMiddleware_StaleSessionVersionRejected(t *testing.T) {
	cache := &fakeCache{current: map[string]int64{"employee:user-1": 5}}
	uc := &fakeUserClient{}
	mw := Middleware(Config{JWTKey: testKey, SessionCache: cache, UserClient: uc})
	next := &captureNext{}

	tok := mintToken(t, pkgauth.Claims{
		UserID: "user-1", UserKind: pkgauth.KindEmployee, SessionVersion: 4,
	}, time.Minute)

	w := httptest.NewRecorder()
	mw(next).ServeHTTP(w, newReq(http.MethodGet, "/api/v1/me", tok))

	if next.called {
		t.Fatal("next was invoked despite stale session_version")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status: %d, want 401", w.Code)
	}
	if msg := decodeErr(t, w).Message; msg != "session revoked" {
		t.Errorf("body message: %q", msg)
	}
}

// TestMiddleware_HappyPathAttachesPrincipal walks the full success path
// with a cache hit: the principal is attached to ctx and forwarded as
// outgoing gRPC metadata, and the protected handler is invoked.
func TestMiddleware_HappyPathAttachesPrincipal(t *testing.T) {
	cache := &fakeCache{current: map[string]int64{"employee:user-1": 3}}
	uc := &fakeUserClient{}
	mw := Middleware(Config{JWTKey: testKey, SessionCache: cache, UserClient: uc})
	next := &captureNext{}

	tok := mintToken(t, pkgauth.Claims{
		UserID:         "user-1",
		UserKind:       pkgauth.KindEmployee,
		Permissions:    []string{"admin", "client.read"},
		SessionVersion: 3,
	}, time.Minute)

	w := httptest.NewRecorder()
	mw(next).ServeHTTP(w, newReq(http.MethodGet, "/api/v1/me", tok))

	if !next.called {
		t.Fatal("happy path: next not invoked")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status: %d, want 200", w.Code)
	}
	got := next.principal
	if got.UserID != "user-1" || got.UserKind != pkgauth.KindEmployee || got.SessionVersion != 3 {
		t.Errorf("principal: %+v", got)
	}
	if len(got.Permissions) != 2 || got.Permissions[0] != "admin" {
		t.Errorf("permissions: %v", got.Permissions)
	}
}

// TestMiddleware_CacheMissPopulatesFromUserService walks the cold-cache
// path: ErrNotCached → user-service GetSessionVersion → cache.Set with
// the fetched value → request continues. Pin SV to the exact returned
// value so the comparison succeeds.
func TestMiddleware_CacheMissPopulatesFromUserService(t *testing.T) {
	cache := &fakeCache{current: map[string]int64{}} // empty → ErrNotCached
	uc := &fakeUserClient{
		resp: &userpb.GetSessionVersionResponse{SessionVersion: 7},
	}
	mw := Middleware(Config{JWTKey: testKey, SessionCache: cache, UserClient: uc})
	next := &captureNext{}

	tok := mintToken(t, pkgauth.Claims{
		UserID: "user-1", UserKind: pkgauth.KindClient, SessionVersion: 7,
	}, time.Minute)

	w := httptest.NewRecorder()
	mw(next).ServeHTTP(w, newReq(http.MethodGet, "/api/v1/me", tok))

	if !next.called {
		t.Fatalf("cache-miss path: next not invoked, status=%d", w.Code)
	}
	if len(cache.setLog) != 1 {
		t.Fatalf("expected one cache populate, got %d", len(cache.setLog))
	}
	got := cache.setLog[0]
	if got.kind != "client" || got.id != "user-1" || got.v != 7 {
		t.Errorf("set call: %+v", got)
	}
}

// TestMiddleware_UserServiceFailureSurfaces503 covers the cold-cache
// path when the fallback fails: the middleware translates that into
// 503 (not 401) so the FE can distinguish "your token is bad" from
// "auth backend is down".
func TestMiddleware_UserServiceFailureSurfaces503(t *testing.T) {
	cache := &fakeCache{current: map[string]int64{}} // empty → ErrNotCached
	uc := &fakeUserClient{err: errors.New("rpc unavailable")}
	mw := Middleware(Config{JWTKey: testKey, SessionCache: cache, UserClient: uc})
	next := &captureNext{}

	tok := mintToken(t, pkgauth.Claims{
		UserID: "user-1", UserKind: pkgauth.KindEmployee, SessionVersion: 1,
	}, time.Minute)

	w := httptest.NewRecorder()
	mw(next).ServeHTTP(w, newReq(http.MethodGet, "/api/v1/me", tok))

	if next.called {
		t.Fatal("next invoked despite user-service failure")
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status: %d, want 503", w.Code)
	}
	if msg := decodeErr(t, w).Message; msg != "session check failed" {
		t.Errorf("body message: %q", msg)
	}
}

// TestMiddleware_RedisFailureSurfaces503 covers the path where Redis
// itself errors (not ErrNotCached). The middleware should surface 503
// rather than fall through to the user service — falling through would
// hammer the user service whenever Redis is degraded.
func TestMiddleware_RedisFailureSurfaces503(t *testing.T) {
	cache := &fakeCache{currErr: errors.New("redis: connection refused")}
	uc := &fakeUserClient{}
	mw := Middleware(Config{JWTKey: testKey, SessionCache: cache, UserClient: uc})
	next := &captureNext{}

	tok := mintToken(t, pkgauth.Claims{
		UserID: "user-1", UserKind: pkgauth.KindEmployee, SessionVersion: 1,
	}, time.Minute)

	w := httptest.NewRecorder()
	mw(next).ServeHTTP(w, newReq(http.MethodGet, "/api/v1/me", tok))

	if next.called {
		t.Fatal("next invoked despite Redis error")
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status: %d, want 503", w.Code)
	}
}

// TestMiddleware_ExpiredTokenRejected pins that an expired JWT is
// rejected as invalid, not "session revoked" — the SV check never
// fires because Verify already failed.
func TestMiddleware_ExpiredTokenRejected(t *testing.T) {
	cache := &fakeCache{current: map[string]int64{"employee:user-1": 1}}
	uc := &fakeUserClient{}
	mw := Middleware(Config{JWTKey: testKey, SessionCache: cache, UserClient: uc})
	next := &captureNext{}

	// Negative TTL → token's exp is in the past at the moment of
	// signing.
	tok := mintToken(t, pkgauth.Claims{
		UserID: "user-1", UserKind: pkgauth.KindEmployee, SessionVersion: 1,
	}, -1*time.Second)

	w := httptest.NewRecorder()
	mw(next).ServeHTTP(w, newReq(http.MethodGet, "/api/v1/me", tok))

	if next.called {
		t.Fatal("next invoked despite expired token")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status: %d, want 401", w.Code)
	}
	if msg := decodeErr(t, w).Message; msg != "invalid access token" {
		t.Errorf("body message: %q", msg)
	}
}
