//go:build integration

package verification

import (
	"context"
	"errors"
	"os"
	"strings"
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
	_ = tRDB.FlushDB(context.Background()).Err()
	return &Cache{R: tRDB}
}

func TestIssueRoundTrip(t *testing.T) {
	c := setup(t)
	ctx := context.Background()

	id, code, exp, err := c.Issue(ctx, "user-1", ActionPayment)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if len(id) != 32 {
		t.Errorf("id should be 32 hex chars, got %q", id)
	}
	if len(code) != CodeLength {
		t.Errorf("code length: want %d, got %d", CodeLength, len(code))
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			t.Errorf("code must be digits only: %q", code)
		}
	}
	if d := time.Until(exp); d <= 0 || d > CodeTTL+time.Second {
		t.Errorf("expiry out of range: %v", d)
	}

	if err := c.Consume(ctx, id, code, ActionPayment); err != nil {
		t.Errorf("happy-path consume: %v", err)
	}
	if err := c.Consume(ctx, id, code, ActionPayment); !errors.Is(err, ErrNotFound) {
		t.Errorf("second consume: want ErrNotFound, got %v", err)
	}
}

func TestConsumeWrongCodeIncrementsAttempts(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	id, code, _, _ := c.Issue(ctx, "u", ActionPayment)

	for i := 0; i < 2; i++ {
		if err := c.Consume(ctx, id, "000000", ActionPayment); !errors.Is(err, ErrWrongCode) {
			t.Errorf("attempt %d: want ErrWrongCode, got %v", i, err)
		}
	}
	if err := c.Consume(ctx, id, "000000", ActionPayment); !errors.Is(err, ErrTooMany) {
		t.Errorf("3rd wrong: want ErrTooMany, got %v", err)
	}
	if err := c.Consume(ctx, id, code, ActionPayment); !errors.Is(err, ErrNotFound) {
		t.Errorf("post-burn correct code: want ErrNotFound, got %v", err)
	}
}

func TestConsumeActionKindMismatch(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	id, code, _, _ := c.Issue(ctx, "u", ActionPayment)

	if err := c.Consume(ctx, id, code, ActionTransfer); !errors.Is(err, ErrMismatch) {
		t.Errorf("kind mismatch: want ErrMismatch, got %v", err)
	}
	if err := c.Consume(ctx, id, code, ActionPayment); err != nil {
		t.Errorf("after mismatch the original kind should still succeed: %v", err)
	}
}

func TestConsumeMissing(t *testing.T) {
	c := setup(t)
	if err := c.Consume(context.Background(), "deadbeef", "123456", ActionPayment); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing id: want ErrNotFound, got %v", err)
	}
}

func TestConsumeExpired(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	id, code, _, _ := c.Issue(ctx, "u", ActionPayment)

	// Force-expire by setting TTL to a past instant. miniredis-style
	// fast-forward isn't available here; we PEXPIREAT instead.
	if err := c.R.PExpireAt(ctx, key(id), time.Now().Add(-time.Second)).Err(); err != nil {
		t.Fatalf("force-expire: %v", err)
	}

	if err := c.Consume(ctx, id, code, ActionPayment); !errors.Is(err, ErrNotFound) {
		t.Errorf("expired: want ErrNotFound, got %v", err)
	}
}

func TestIssueProducesUniqueIDs(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	seen := map[string]bool{}
	for i := 0; i < 32; i++ {
		id, _, _, err := c.Issue(ctx, "u", ActionPayment)
		if err != nil {
			t.Fatalf("issue %d: %v", i, err)
		}
		if seen[id] {
			t.Fatalf("duplicate id at iter %d: %s", i, id)
		}
		seen[id] = true
	}
}

func TestPersistsAttemptsAcrossWrongTries(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	id, _, _, _ := c.Issue(ctx, "u", ActionPayment)

	if err := c.Consume(ctx, id, "999999", ActionPayment); !errors.Is(err, ErrWrongCode) {
		t.Fatalf("first wrong: %v", err)
	}
	got, err := c.R.Get(ctx, key(id)).Result()
	if err != nil {
		t.Fatalf("redis get: %v", err)
	}
	if !strings.Contains(got, `"a":1`) {
		t.Errorf("attempts not persisted: %s", got)
	}
}
