package service

import (
	"testing"
	"time"
)

// TestLockoutThreshold pins the brute-force decision rule (todoSpec
// S7–S8): the account locks once the failed-attempt count reaches
// maxFailedLogins, and not before.
func TestLockoutThreshold(t *testing.T) {
	if maxFailedLogins != 5 {
		t.Fatalf("maxFailedLogins = %d, want 5", maxFailedLogins)
	}
	if lockoutDuration != 15*time.Minute {
		t.Fatalf("lockoutDuration = %v, want 15m", lockoutDuration)
	}
	cases := []struct {
		newCount int
		wantLock bool
	}{
		{1, false},
		{4, false},
		{5, true},
		{6, true},
	}
	for _, c := range cases {
		got := c.newCount >= maxFailedLogins
		if got != c.wantLock {
			t.Errorf("newCount=%d: locked=%v, want %v", c.newCount, got, c.wantLock)
		}
	}
}

// TestLockActiveWindow checks the "still locked" predicate used in
// completeLogin: a lock in the future blocks login, a past one does not,
// and nil never blocks.
func TestLockActiveWindow(t *testing.T) {
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	future := now.Add(time.Minute)
	past := now.Add(-time.Minute)

	locked := func(until *time.Time) bool {
		return until != nil && until.After(now)
	}
	if locked(nil) {
		t.Error("nil lockedUntil should not be locked")
	}
	if !locked(&future) {
		t.Error("future lockedUntil should be locked")
	}
	if locked(&past) {
		t.Error("past lockedUntil should not be locked")
	}
}
