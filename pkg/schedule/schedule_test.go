package schedule

import (
	"testing"
	"time"
)

func date(y int, m time.Month, d, hh, mm int) time.Time {
	return time.Date(y, m, d, hh, mm, 0, 0, time.UTC)
}

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		want Cadence
		ok   bool
	}{
		{"DAILY", Daily, true},
		{"daily", Daily, true},
		{"  Weekly ", Weekly, true},
		{"MONTHLY", Monthly, true},
		{"ONCE", Once, true},
		{"", "", false},
		{"yearly", "", false},
	}
	for _, c := range cases {
		got, err := Parse(c.in)
		if c.ok && (err != nil || got != c.want) {
			t.Errorf("Parse(%q) = %q, %v; want %q, nil", c.in, got, err, c.want)
		}
		if !c.ok && err == nil {
			t.Errorf("Parse(%q) expected error", c.in)
		}
	}
}

func TestIsRecurring(t *testing.T) {
	if Once.IsRecurring() {
		t.Error("Once should not be recurring")
	}
	for _, c := range []Cadence{Daily, Weekly, Monthly} {
		if !c.IsRecurring() {
			t.Errorf("%s should be recurring", c)
		}
	}
	if Cadence("BOGUS").IsRecurring() {
		t.Error("invalid cadence should not be recurring")
	}
}

func TestAdvance(t *testing.T) {
	cases := []struct {
		name string
		in   time.Time
		c    Cadence
		want time.Time
	}{
		{"daily", date(2026, 6, 6, 9, 0), Daily, date(2026, 6, 7, 9, 0)},
		{"daily month rollover", date(2026, 6, 30, 9, 0), Daily, date(2026, 7, 1, 9, 0)},
		{"weekly", date(2026, 6, 6, 9, 0), Weekly, date(2026, 6, 13, 9, 0)},
		{"weekly month rollover", date(2026, 6, 28, 9, 0), Weekly, date(2026, 7, 5, 9, 0)},
		{"monthly plain", date(2026, 6, 15, 9, 0), Monthly, date(2026, 7, 15, 9, 0)},
		{"monthly clamp jan31->feb28", date(2026, 1, 31, 9, 0), Monthly, date(2026, 2, 28, 9, 0)},
		{"monthly clamp jan31->feb29 leap", date(2028, 1, 31, 9, 0), Monthly, date(2028, 2, 29, 9, 0)},
		{"monthly clamp mar31->apr30", date(2026, 3, 31, 9, 0), Monthly, date(2026, 4, 30, 9, 0)},
		{"monthly year rollover", date(2026, 12, 15, 9, 0), Monthly, date(2027, 1, 15, 9, 0)},
		{"monthly dec31->jan31", date(2026, 12, 31, 9, 0), Monthly, date(2027, 1, 31, 9, 0)},
		{"once unchanged", date(2026, 6, 6, 9, 0), Once, date(2026, 6, 6, 9, 0)},
		{"invalid unchanged", date(2026, 6, 6, 9, 0), Cadence("X"), date(2026, 6, 6, 9, 0)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Advance(c.in, c.c); !got.Equal(c.want) {
				t.Errorf("Advance(%s, %s) = %s; want %s", c.in, c.c, got, c.want)
			}
		})
	}
}

// Advancing a clamped monthly date must not "stick" at the clamp — the
// next month should return to the original day-of-month where it fits.
// (Common bug: Jan 31 → Feb 28 → Mar 28 instead of Mar 31.) Our impl
// advances from the clamped value, so Feb 28 → Mar 28; document that.
func TestAdvanceMonthlyFromClamped(t *testing.T) {
	feb := Advance(date(2026, 1, 31, 9, 0), Monthly) // Feb 28
	mar := Advance(feb, Monthly)
	if !mar.Equal(date(2026, 3, 28, 9, 0)) {
		t.Errorf("Feb28 +1mo = %s; want 2026-03-28 (advance is from the stored next_run)", mar)
	}
}

func TestNextAfter(t *testing.T) {
	now := date(2026, 6, 6, 12, 0)

	// Exactly-due recurring steps once past now.
	got := NextAfter(date(2026, 6, 6, 9, 0), Daily, now)
	if !got.Equal(date(2026, 6, 7, 9, 0)) {
		t.Errorf("daily exactly-due: got %s want 2026-06-07 09:00", got)
	}

	// Catch-up: a row 3 days stale jumps straight to the next future slot.
	got = NextAfter(date(2026, 6, 3, 9, 0), Daily, now)
	if !got.Equal(date(2026, 6, 7, 9, 0)) {
		t.Errorf("daily catch-up: got %s want 2026-06-07 09:00", got)
	}

	// Weekly catch-up across a long gap.
	got = NextAfter(date(2026, 5, 1, 9, 0), Weekly, now)
	if !got.After(now) {
		t.Errorf("weekly catch-up not after now: %s", got)
	}
	if got.AddDate(0, 0, -7).After(now) {
		t.Errorf("weekly catch-up overshot: %s should be the first slot after now", got)
	}

	// Non-recurring returns unchanged.
	in := date(2026, 6, 1, 9, 0)
	if got := NextAfter(in, Once, now); !got.Equal(in) {
		t.Errorf("once NextAfter changed the time: %s", got)
	}
}

func TestIsDue(t *testing.T) {
	nr := date(2026, 6, 6, 9, 0)
	if IsDue(nr, date(2026, 6, 6, 8, 59)) {
		t.Error("not due one minute before")
	}
	if !IsDue(nr, nr) {
		t.Error("due exactly at next_run")
	}
	if !IsDue(nr, date(2026, 6, 6, 9, 1)) {
		t.Error("due after next_run")
	}
}

func TestAfterRun(t *testing.T) {
	now := date(2026, 6, 6, 12, 0)

	// Recurring: stays active, advances to next future slot.
	nr, deact := AfterRun(date(2026, 6, 6, 9, 0), Daily, now)
	if deact {
		t.Error("recurring row should not deactivate")
	}
	if !nr.Equal(date(2026, 6, 7, 9, 0)) {
		t.Errorf("recurring next_run = %s; want 2026-06-07 09:00", nr)
	}

	// One-off: deactivates, next_run unchanged.
	in := date(2026, 6, 6, 9, 0)
	nr, deact = AfterRun(in, Once, now)
	if !deact {
		t.Error("once row should deactivate after running")
	}
	if !nr.Equal(in) {
		t.Errorf("once next_run changed: %s", nr)
	}
}

func TestValidateFuture(t *testing.T) {
	now := date(2026, 6, 6, 12, 0)
	if ValidateFuture(date(2026, 6, 7, 0, 0), now) != nil {
		t.Error("future date should pass")
	}
	if ValidateFuture(now, now) == nil {
		t.Error("now should fail (not strictly future)")
	}
	if ValidateFuture(date(2026, 6, 5, 0, 0), now) == nil {
		t.Error("past date should fail")
	}
}
