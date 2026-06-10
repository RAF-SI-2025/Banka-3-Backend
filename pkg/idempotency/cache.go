// Package idempotency implements the Redis-backed response cache used
// by the gateway to honour the Idempotency-Key HTTP header on
// mutating requests.
//
// The flow:
//
//  1. Client sends a mutating request (POST/PUT/PATCH/DELETE) with
//     `Idempotency-Key: <uuid>`.
//  2. Gateway middleware looks the key up under the calling user.
//  3. Cache hit → middleware replays the recorded status/headers/body
//     and stamps `Idempotent-Replayed: true`. The handler does NOT run.
//  4. Cache miss → handler runs, response is captured, cached on
//     2xx success for the configured TTL, then returned to the client.
//
// Keys are scoped to the user (or "anon" for unauthenticated paths) so
// two clients can't collide on the same UUID.
package idempotency

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// Entry is the serialized snapshot of a successful response. Headers
// are stored as the same map shape as http.Header to round-trip cleanly.
type Entry struct {
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers,omitempty"`
	Body    []byte              `json:"body"`
}

// ErrMiss signals that no entry exists for (userID, key). Callers
// proceed with the real handler and Set the response on success.
var ErrMiss = errors.New("idempotency cache miss")

// Cache holds the Redis client and TTL. TTL controls how long a
// successful response is replayable; 24h matches the typical
// e-commerce/payments idempotency window.
type Cache struct {
	R   *redis.Client
	TTL time.Duration
}

func cacheKey(userID, idemKey string) string {
	if userID == "" {
		userID = "anon"
	}
	return "idem:" + userID + ":" + idemKey
}

// Get returns the cached entry for (userID, idemKey), or ErrMiss.
func (c *Cache) Get(ctx context.Context, userID, idemKey string) (*Entry, error) {
	raw, err := c.R.Get(ctx, cacheKey(userID, idemKey)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrMiss
	}
	if err != nil {
		slog.WarnContext(ctx, "idempotency cache get failed", "err", err, "key", idemKey, "user_id", userID)
		return nil, err
	}
	var e Entry
	if err := json.Unmarshal(raw, &e); err != nil {
		slog.WarnContext(ctx, "idempotency cache entry corrupt", "err", err, "key", idemKey, "user_id", userID)
		return nil, err
	}
	return &e, nil
}

// Set caches `e` under (userID, idemKey) with the cache's TTL. NX
// semantics — first writer wins, so a duplicate concurrent request
// that completes second won't overwrite the first replay value.
func (c *Cache) Set(ctx context.Context, userID, idemKey string, e *Entry) error {
	raw, err := json.Marshal(e)
	if err != nil {
		slog.WarnContext(ctx, "idempotency entry marshal failed", "err", err, "key", idemKey, "user_id", userID)
		return err
	}
	if err := c.R.SetNX(ctx, cacheKey(userID, idemKey), raw, c.TTL).Err(); err != nil {
		slog.WarnContext(ctx, "idempotency cache set failed", "err", err, "key", idemKey, "user_id", userID)
		return err
	}
	return nil
}
