//go:build integration

package idempotency

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	tOnce sync.Once
	tRDB  *redis.Client
	tSkip string
)

func envOr(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}

func setup(t *testing.T) *Cache {
	t.Helper()
	tOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		c := redis.NewClient(&redis.Options{
			Addr:     envOr("INTEGRATION_REDIS_ADDR", "localhost:6379"),
			Password: envOr("INTEGRATION_REDIS_PASSWORD", "banka"),
		})
		if err := c.Ping(ctx).Err(); err != nil {
			tSkip = "redis unavailable: " + err.Error()
			return
		}
		tRDB = c
	})
	if tSkip != "" {
		t.Skip(tSkip)
	}
	// Wipe any previous test debris.
	_ = tRDB.FlushDB(context.Background()).Err()
	return &Cache{R: tRDB, TTL: time.Minute}
}

func TestCache_GetMissThenSetThenHit(t *testing.T) {
	c := setup(t)
	ctx := context.Background()

	if _, err := c.Get(ctx, "u1", "abc"); err != ErrMiss {
		t.Fatalf("first Get: got %v, want ErrMiss", err)
	}

	want := &Entry{
		Status:  201,
		Headers: map[string][]string{"Content-Type": {"application/json"}},
		Body:    []byte(`{"id":42}`),
	}
	if err := c.Set(ctx, "u1", "abc", want); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := c.Get(ctx, "u1", "abc")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != want.Status {
		t.Errorf("status: %d, want %d", got.Status, want.Status)
	}
	if string(got.Body) != string(want.Body) {
		t.Errorf("body: %q, want %q", got.Body, want.Body)
	}
	if got.Headers["Content-Type"][0] != "application/json" {
		t.Errorf("headers round-trip lost: %+v", got.Headers)
	}
}

func TestCache_PerUserScoped(t *testing.T) {
	c := setup(t)
	ctx := context.Background()

	if err := c.Set(ctx, "userA", "k", &Entry{Status: 200, Body: []byte("A")}); err != nil {
		t.Fatalf("set A: %v", err)
	}
	if _, err := c.Get(ctx, "userB", "k"); err != ErrMiss {
		t.Fatalf("Get for userB: got %v, want ErrMiss (different scope)", err)
	}
}

func TestCache_SetNXFirstWriterWins(t *testing.T) {
	c := setup(t)
	ctx := context.Background()

	if err := c.Set(ctx, "u", "k", &Entry{Status: 200, Body: []byte("first")}); err != nil {
		t.Fatalf("set 1: %v", err)
	}
	// Concurrent retry — different body, same key. Must NOT overwrite.
	if err := c.Set(ctx, "u", "k", &Entry{Status: 200, Body: []byte("second")}); err != nil {
		t.Fatalf("set 2: %v", err)
	}
	got, err := c.Get(ctx, "u", "k")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(got.Body) != "first" {
		t.Errorf("SetNX broken: got %q, want %q", got.Body, "first")
	}
}

func TestCache_TTLExpiry(t *testing.T) {
	c := &Cache{R: setup(t).R, TTL: 200 * time.Millisecond}
	ctx := context.Background()
	if err := c.Set(ctx, "u", "k", &Entry{Status: 200, Body: []byte("x")}); err != nil {
		t.Fatalf("set: %v", err)
	}
	time.Sleep(400 * time.Millisecond)
	if _, err := c.Get(ctx, "u", "k"); err != ErrMiss {
		t.Errorf("entry not expired: got %v, want ErrMiss", err)
	}
}
