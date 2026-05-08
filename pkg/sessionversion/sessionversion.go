// Package sessionversion implements the Redis-cached session_version
// lookup used by the gateway to revoke access tokens immediately on
// admin actions (e.g. employee deactivation).
//
// Each user has a session_version int in the user service. The JWT
// access token carries the value at issue time. On every request, the
// gateway calls [Checker.Current] to fetch the live value from Redis;
// if absent, [Checker.Refresh] populates it from the user service.
// A token whose `sv` claim is below the current value is rejected.
package sessionversion

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// Cache holds the Redis client and key TTL for session_version lookups.
type Cache struct {
	R   *redis.Client
	TTL time.Duration
}

// ErrNotCached signals that the user has no entry in Redis. The caller
// is expected to fetch from the user service and call [Cache.Set].
var ErrNotCached = errors.New("session version not cached")

func key(kind, id string) string {
	return fmt.Sprintf("usv:%s:%s", kind, id)
}

// Current returns the cached session_version for the user, or
// [ErrNotCached] if no entry exists.
func (c *Cache) Current(ctx context.Context, kind, id string) (int64, error) {
	s, err := c.R.Get(ctx, key(kind, id)).Result()
	if errors.Is(err, redis.Nil) {
		return 0, ErrNotCached
	}
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(s, 10, 64)
}

// Set caches the user's current session_version with the cache's TTL.
func (c *Cache) Set(ctx context.Context, kind, id string, v int64) error {
	return c.R.Set(ctx, key(kind, id), v, c.TTL).Err()
}

// Invalidate removes the cached entry. Called on deactivation or any
// other action that should immediately invalidate live tokens.
func (c *Cache) Invalidate(ctx context.Context, kind, id string) error {
	return c.R.Del(ctx, key(kind, id)).Err()
}
