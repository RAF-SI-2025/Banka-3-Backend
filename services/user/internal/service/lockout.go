package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Login lockout: after MaxFailures wrong-password attempts within
// FailureWindow, the account is locked for LockDuration. Counters are
// keyed by lower-cased email; both counter and lock keys live in Redis
// with TTLs matching their semantics.
const (
	loginMaxFailures   = 3
	loginFailureWindow = 15 * time.Minute
	loginLockDuration  = 15 * time.Minute
)

func loginFailKey(email string) string { return "login:fail:" + strings.ToLower(email) }
func loginLockKey(email string) string { return "login:lock:" + strings.ToLower(email) }

// isLoginLocked reports whether the account is currently locked. Returns
// the remaining lock duration so the caller can surface it.
func (s *Service) isLoginLocked(ctx context.Context, email string) (bool, time.Duration, error) {
	if s.Redis == nil {
		return false, 0, nil
	}
	ttl, err := s.Redis.TTL(ctx, loginLockKey(email)).Result()
	if err != nil {
		return false, 0, err
	}
	// TTL == -2 → key missing; -1 → no expiry (shouldn't happen here).
	if ttl <= 0 {
		return false, 0, nil
	}
	return true, ttl, nil
}

// recordLoginFailure increments the failure counter and engages the lock
// once the threshold is hit. Failures outside the window roll off via
// the counter key's TTL.
func (s *Service) recordLoginFailure(ctx context.Context, email string) {
	if s.Redis == nil {
		return
	}
	key := loginFailKey(email)
	n, err := s.Redis.Incr(ctx, key).Result()
	if err != nil {
		s.Log.Warn("login fail counter incr", "email", email, "error", err)
		return
	}
	if n == 1 {
		// First failure — set the rolling window.
		if err := s.Redis.Expire(ctx, key, loginFailureWindow).Err(); err != nil {
			s.Log.Warn("login fail counter expire", "email", email, "error", err)
		}
	}
	if n >= loginMaxFailures {
		if err := s.Redis.Set(ctx, loginLockKey(email), "1", loginLockDuration).Err(); err != nil {
			s.Log.Warn("login lock set", "email", email, "error", err)
		}
		// Counter has done its job; let it expire on its own.
	}
}

// clearLoginFailures wipes counter + lock on a successful authentication.
func (s *Service) clearLoginFailures(ctx context.Context, email string) {
	if s.Redis == nil {
		return
	}
	if err := s.Redis.Del(ctx, loginFailKey(email), loginLockKey(email)).Err(); err != nil && !errors.Is(err, redis.Nil) {
		s.Log.Warn("login lock clear", "email", email, "error", err)
	}
}

// formatLockMessage returns the spec-style Serbian copy.
func formatLockMessage(retryAfter time.Duration) string {
	mins := int((retryAfter + time.Minute - 1) / time.Minute)
	if mins < 1 {
		mins = 1
	}
	return fmt.Sprintf("Nalog je privremeno zaključan zbog previše neuspešnih pokušaja. Pokušajte ponovo za %d min.", mins)
}
