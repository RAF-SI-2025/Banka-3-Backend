// Package clock abstracts time.Now for testability + QA.
//
// Three implementations of [Clock]:
//
//   - [Real]      — production passthrough (time.Now().UTC()).
//   - [Fixed]     — deterministic for unit tests.
//   - [Adjustable] — shifted by a Redis-persisted offset so QA can
//     advance the business clock at runtime without restarting
//     services. Gated by the CLOCK_DEBUG env so production code
//     can't accidentally drift time.
//
// Cron schedulers (spec p.38 23:59 daily limit reset, monthly tax
// cron, loan installment cron, variable-rate refresh) + every
// expiry / after-hours / settlement-date check route through this
// package so all services observe the same shifted time.
package clock

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// Clock returns the current time. Production code should accept a Clock
// rather than calling time.Now directly so tests can inject deterministic
// time.
type Clock interface {
	Now() time.Time
}

// Real returns the system clock.
type Real struct{}

// Now implements [Clock].
func (Real) Now() time.Time { return time.Now().UTC() }

// Fixed always returns t. Useful in tests.
type Fixed struct{ T time.Time }

// Now implements [Clock].
func (f Fixed) Now() time.Time { return f.T }

// =====================================================================
// Adjustable — QA / E2E driver
// =====================================================================

// RedisKey is where the persisted offset string lives.
const RedisKey = "banka:clock:offset"

// RefreshInterval is how often a running [Adjustable] re-reads its
// offset from Redis. 5 s keeps cypress test waits short without
// burdening Redis.
const RefreshInterval = 5 * time.Second

// Adjustable shifts [Real] by a Redis-persisted offset so QA can
// fast-forward business time (e.g., past the 23:59 daily limit reset
// or the last-day-of-month tax cron). Safe for concurrent use.
//
// Production: construct with debugEnabled=false; SetOffset rejects
// and Now() is a passthrough — no behaviour change vs Real.
//
// QA: with CLOCK_DEBUG=true the gateway's POST /_debug/clock writes
// a duration to Redis; each service's StartRefresher goroutine pulls
// it in within RefreshInterval and every Now() call reflects it.
type Adjustable struct {
	rdb       *redis.Client
	enabled   bool
	cachedNS  atomic.Int64
	refreshIv time.Duration
}

// NewAdjustable constructs an [Adjustable] clock. When debugEnabled
// is true, the constructor loads any persisted offset from Redis so
// a service restart inherits the running adjustment. rdb may be nil;
// in that case the offset lives only in memory.
func NewAdjustable(rdb *redis.Client, debugEnabled bool) *Adjustable {
	c := &Adjustable{rdb: rdb, enabled: debugEnabled, refreshIv: RefreshInterval}
	if debugEnabled {
		c.loadFromRedis(context.Background())
	}
	return c
}

// Now implements [Clock]. Returns time.Now().UTC() shifted by the
// current offset; passthrough when CLOCK_DEBUG is off.
func (c *Adjustable) Now() time.Time {
	n := time.Now().UTC()
	if c == nil || !c.enabled {
		return n
	}
	off := c.cachedNS.Load()
	if off == 0 {
		return n
	}
	return n.Add(time.Duration(off))
}

// Offset returns the currently-applied shift, zero when CLOCK_DEBUG
// is off or no offset has been set.
func (c *Adjustable) Offset() time.Duration {
	if c == nil || !c.enabled {
		return 0
	}
	return time.Duration(c.cachedNS.Load())
}

// Enabled reports whether SetOffset will succeed.
func (c *Adjustable) Enabled() bool {
	return c != nil && c.enabled
}

// SetOffset stores a new offset in Redis + the in-process cache.
// Rejects with an error when CLOCK_DEBUG is off — production code
// must not advance the clock. Cross-service propagation is via the
// per-process [Adjustable.StartRefresher] goroutine.
func (c *Adjustable) SetOffset(ctx context.Context, d time.Duration) error {
	if c == nil || !c.enabled {
		return fmt.Errorf("clock: SetOffset requires CLOCK_DEBUG=true")
	}
	c.cachedNS.Store(int64(d))
	if c.rdb == nil {
		return nil
	}
	return c.rdb.Set(ctx, RedisKey, d.String(), 0).Err()
}

// StartRefresher kicks off a background goroutine that polls Redis
// every [RefreshInterval] and updates the in-process cache. Call
// once per service after the clock is wired. No-op when CLOCK_DEBUG
// is off or rdb is nil.
func (c *Adjustable) StartRefresher(ctx context.Context) {
	if c == nil || !c.enabled || c.rdb == nil {
		return
	}
	go func() {
		t := time.NewTicker(c.refreshIv)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				c.loadFromRedis(ctx)
			}
		}
	}()
}

func (c *Adjustable) loadFromRedis(ctx context.Context) {
	if c.rdb == nil {
		return
	}
	s, err := c.rdb.Get(ctx, RedisKey).Result()
	if err != nil {
		// ErrNil (no key set) is the common case; leave the cache
		// alone. Any other error means redis is flaky and the cache
		// stays stale — fail-open is safer than resetting business
		// time mid-flight.
		if !errors.Is(err, redis.Nil) {
			slog.WarnContext(ctx, "clock offset refresh failed, keeping cached offset", "err", err)
		}
		return
	}
	d, perr := time.ParseDuration(s)
	if perr != nil {
		slog.WarnContext(ctx, "clock offset malformed in redis, keeping cached offset", "err", perr)
		return
	}
	c.cachedNS.Store(int64(d))
}
