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

func TestListPending(t *testing.T) {
	c := setup(t)
	ctx := context.Background()

	// Unknown user → empty, no error.
	if got, err := c.ListPending(ctx, "nobody"); err != nil || len(got) != 0 {
		t.Fatalf("empty user: got %v, err %v", got, err)
	}

	id1, code1, _, _ := c.Issue(ctx, "u1", ActionPayment)
	_, _, _, _ = c.Issue(ctx, "u1", ActionTransfer)
	_, _, _, _ = c.Issue(ctx, "u2", ActionCardIssue)

	got, err := c.ListPending(ctx, "u1")
	if err != nil {
		t.Fatalf("list u1: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("u1 should have 2 pending, got %d", len(got))
	}
	for _, p := range got {
		if p.Code == "" || p.ID == "" {
			t.Errorf("pending missing code/id: %+v", p)
		}
		if p.Attempts != 0 {
			t.Errorf("fresh code attempts should be 0, got %d", p.Attempts)
		}
		if d := time.Until(p.ExpiresAt); d <= 0 || d > CodeTTL+time.Second {
			t.Errorf("expiry out of range: %v", d)
		}
		if p.ID == id1 && p.Code != code1 {
			t.Errorf("code mismatch for id1: want %s got %s", code1, p.Code)
		}
	}

	// u2's code must not leak into u1's list.
	if u2, _ := c.ListPending(ctx, "u2"); len(u2) != 1 {
		t.Fatalf("u2 should have 1 pending, got %d", len(u2))
	}

	// Consuming retires the record; the stale index entry is pruned.
	if err := c.Consume(ctx, id1, code1, ActionPayment); err != nil {
		t.Fatalf("consume: %v", err)
	}
	after, err := c.ListPending(ctx, "u1")
	if err != nil {
		t.Fatalf("list u1 after consume: %v", err)
	}
	if len(after) != 1 {
		t.Fatalf("u1 should have 1 pending after consume, got %d", len(after))
	}
	if after[0].ID == id1 {
		t.Errorf("consumed id should not appear in pending list")
	}
}

func TestListPendingReflectsAttempts(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	id, _, _, _ := c.Issue(ctx, "u", ActionPayment)
	if err := c.Consume(ctx, id, "000000", ActionPayment); !errors.Is(err, ErrWrongCode) {
		t.Fatalf("want ErrWrongCode, got %v", err)
	}
	got, err := c.ListPending(ctx, "u")
	if err != nil || len(got) != 1 {
		t.Fatalf("list: got %v err %v", got, err)
	}
	if got[0].Attempts != 1 {
		t.Errorf("attempts after one wrong try: want 1, got %d", got[0].Attempts)
	}
}

func TestApproveThenConsumeApproved(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	id, _, _, _ := c.Issue(ctx, "u1", ActionPayment)

	// Un-approved id-only consume is rejected.
	if err := c.ConsumeApproved(ctx, "u1", id, ActionPayment); !errors.Is(err, ErrNotApproved) {
		t.Fatalf("pre-approve consume: want ErrNotApproved, got %v", err)
	}

	// Approve flips the flag and shows up in ListPending.
	if err := c.Approve(ctx, "u1", id); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if err := c.Approve(ctx, "u1", id); err != nil {
		t.Errorf("re-approve should be idempotent, got %v", err)
	}
	got, _ := c.ListPending(ctx, "u1")
	if len(got) != 1 || !got[0].Approved {
		t.Fatalf("pending should report approved=true, got %+v", got)
	}

	// Quick-approve consume succeeds and is one-shot.
	if err := c.ConsumeApproved(ctx, "u1", id, ActionPayment); err != nil {
		t.Fatalf("approved consume: %v", err)
	}
	if err := c.ConsumeApproved(ctx, "u1", id, ActionPayment); !errors.Is(err, ErrNotFound) {
		t.Errorf("second consume: want ErrNotFound, got %v", err)
	}
}

func TestApproveOwnershipScoped(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	id, _, _, _ := c.Issue(ctx, "owner", ActionPayment)

	// A different user can neither approve nor consume-approved the record.
	if err := c.Approve(ctx, "intruder", id); !errors.Is(err, ErrNotFound) {
		t.Errorf("foreign approve: want ErrNotFound, got %v", err)
	}
	if err := c.Approve(ctx, "owner", id); err != nil {
		t.Fatalf("owner approve: %v", err)
	}
	if err := c.ConsumeApproved(ctx, "intruder", id, ActionPayment); !errors.Is(err, ErrNotFound) {
		t.Errorf("foreign consume: want ErrNotFound, got %v", err)
	}
	// Wrong kind even when approved → ErrMismatch.
	if err := c.ConsumeApproved(ctx, "owner", id, ActionTransfer); !errors.Is(err, ErrMismatch) {
		t.Errorf("kind mismatch: want ErrMismatch, got %v", err)
	}
	// Owner + right kind still works.
	if err := c.ConsumeApproved(ctx, "owner", id, ActionPayment); err != nil {
		t.Errorf("owner approved consume: %v", err)
	}
}

func TestConsumeApprovedMissing(t *testing.T) {
	c := setup(t)
	if err := c.ConsumeApproved(context.Background(), "u", "deadbeef", ActionPayment); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing id: want ErrNotFound, got %v", err)
	}
	if err := c.Approve(context.Background(), "u", "deadbeef"); !errors.Is(err, ErrNotFound) {
		t.Errorf("approve missing id: want ErrNotFound, got %v", err)
	}
}

func TestApprovePreservesTTL(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	id, _, _, _ := c.Issue(ctx, "u", ActionPayment)
	if err := c.Approve(ctx, "u", id); err != nil {
		t.Fatalf("approve: %v", err)
	}
	ttl, err := c.R.TTL(ctx, key(id)).Result()
	if err != nil {
		t.Fatalf("ttl: %v", err)
	}
	if ttl <= 0 || ttl > CodeTTL+time.Second {
		t.Errorf("ttl after approve out of range: %v", ttl)
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
