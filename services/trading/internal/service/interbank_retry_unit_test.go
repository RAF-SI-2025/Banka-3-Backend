package service

import (
	"testing"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/schedule"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/saga"
)

// TestRetryConstants pins the spec's 5s re-attempt cadence and 30s
// give-up window so a refactor can't silently change the SLA.
func TestRetryConstants(t *testing.T) {
	if interbankRetryInterval != 5*time.Second {
		t.Fatalf("retry interval = %v, want 5s", interbankRetryInterval)
	}
	if interbankRetryDeadline != 30*time.Second {
		t.Fatalf("retry deadline = %v, want 30s", interbankRetryDeadline)
	}
}

// TestDecideRetryAction exercises the 5s/30s retry state machine across
// every saga status and deadline position.
func TestDecideRetryAction(t *testing.T) {
	base := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	// Entry created at base; deadline = base + 30s.
	deadline := base.Add(interbankRetryDeadline)

	cases := []struct {
		name   string
		status saga.Status
		now    time.Time
		want   retryAction
	}{
		{
			name:   "completed → succeed",
			status: saga.StatusCompleted,
			now:    base.Add(5 * time.Second),
			want:   retrySucceed,
		},
		{
			name:   "completed after deadline still succeeds",
			status: saga.StatusCompleted,
			now:    base.Add(40 * time.Second),
			want:   retrySucceed,
		},
		{
			name:   "saga failed → expire (give up)",
			status: saga.StatusFailed,
			now:    base.Add(5 * time.Second),
			want:   retryExpire,
		},
		{
			name:   "saga compensated → expire",
			status: saga.StatusCompensated,
			now:    base.Add(5 * time.Second),
			want:   retryExpire,
		},
		{
			name:   "still running within window → reschedule",
			status: saga.StatusRunning,
			now:    base.Add(5 * time.Second),
			want:   retryReschedule,
		},
		{
			name:   "still running just before deadline → reschedule",
			status: saga.StatusRunning,
			now:    base.Add(29 * time.Second),
			want:   retryReschedule,
		},
		{
			name:   "still running exactly at deadline → expire",
			status: saga.StatusRunning,
			now:    deadline,
			want:   retryExpire,
		},
		{
			name:   "still running past deadline → expire",
			status: saga.StatusRunning,
			now:    base.Add(31 * time.Second),
			want:   retryExpire,
		},
		{
			name:   "unreadable saga (empty status) within window → reschedule",
			status: saga.Status(""),
			now:    base.Add(10 * time.Second),
			want:   retryReschedule,
		},
		{
			name:   "unreadable saga past deadline → expire",
			status: saga.Status(""),
			now:    base.Add(35 * time.Second),
			want:   retryExpire,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := decideRetryAction(tc.status, deadline, tc.now); got != tc.want {
				t.Fatalf("decideRetryAction(%q, deadline, now=+%v) = %v, want %v",
					tc.status, tc.now.Sub(base), got, tc.want)
			}
		})
	}
}

// TestRetryRescheduleAdvancesByInterval verifies that a rescheduled
// entry's next_retry_at lands exactly one 5s interval ahead of now —
// the cadence the worker re-arms with.
func TestRetryRescheduleAdvancesByInterval(t *testing.T) {
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	next := now.Add(interbankRetryInterval)
	if got := next.Sub(now); got != 5*time.Second {
		t.Fatalf("reschedule delta = %v, want 5s", got)
	}
}

// TestScheduledInterbankCadenceAdvance pins the periodic-payment cadence
// advance: ONCE deactivates without recurring, the recurring cadences
// advance to the next future slot and stay active. This is the decision
// RunDueInterbankPayments makes per row via schedule.AfterRun.
func TestScheduledInterbankCadenceAdvance(t *testing.T) {
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	due := now // a row exactly due

	t.Run("once deactivates", func(t *testing.T) {
		next, deactivate := schedule.AfterRun(due, schedule.Once, now)
		if !deactivate {
			t.Fatal("ONCE must deactivate after running")
		}
		if !next.Equal(due) {
			t.Fatalf("ONCE next_run should be unchanged, got %v", next)
		}
	})

	recurring := []struct {
		name    string
		cadence schedule.Cadence
		wantGap time.Duration
	}{
		{"daily", schedule.Daily, 24 * time.Hour},
		{"weekly", schedule.Weekly, 7 * 24 * time.Hour},
	}
	for _, tc := range recurring {
		t.Run(tc.name+" advances + stays active", func(t *testing.T) {
			next, deactivate := schedule.AfterRun(due, tc.cadence, now)
			if deactivate {
				t.Fatalf("%s must stay active", tc.cadence)
			}
			if !next.After(now) {
				t.Fatalf("%s next_run must be in the future, got %v", tc.cadence, next)
			}
			if got := next.Sub(due); got != tc.wantGap {
				t.Fatalf("%s gap = %v, want %v", tc.cadence, got, tc.wantGap)
			}
		})
	}

	t.Run("monthly advances one calendar month", func(t *testing.T) {
		next, deactivate := schedule.AfterRun(due, schedule.Monthly, now)
		if deactivate {
			t.Fatal("MONTHLY must stay active")
		}
		if next.Month() != now.Month()+1 {
			t.Fatalf("MONTHLY next month = %v, want %v", next.Month(), now.Month()+1)
		}
	})
}
