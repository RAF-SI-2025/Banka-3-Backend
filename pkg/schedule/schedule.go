// Package schedule holds the per-row scheduling math shared by the
// recurring/scheduled features (C2 scheduled payments, C3 DCA recurring
// orders, C5 recurring inter-bank payments). It is distinct from the
// cluster scheduler service: that service fires wall-clock cron triggers
// (e.g. "run the sweep every minute"); this package computes when an
// individual scheduled row is next due and advances it.
//
// Pure functions over time.Time — no I/O, no globals. Callers pass their
// own "now" (typically pkg/clock) so behaviour is deterministic in tests.
package schedule

import (
	"fmt"
	"strings"
	"time"
)

// Cadence is how often a scheduled row repeats.
type Cadence string

const (
	// Once is a single execution on its scheduled date; it does not
	// recur (used by one-off scheduled payments).
	Once Cadence = "ONCE"
	// Daily repeats every calendar day.
	Daily Cadence = "DAILY"
	// Weekly repeats every 7 days.
	Weekly Cadence = "WEEKLY"
	// Monthly repeats on the same day-of-month, clamped to the last day
	// for short months (Jan 31 → Feb 28/29 → Mar 31 …).
	Monthly Cadence = "MONTHLY"
)

// Valid reports whether c is a recognised cadence.
func (c Cadence) Valid() bool {
	switch c {
	case Once, Daily, Weekly, Monthly:
		return true
	default:
		return false
	}
}

// IsRecurring reports whether c repeats. Once does not; the others do.
// A sweep deactivates a Once row after firing rather than advancing it.
func (c Cadence) IsRecurring() bool {
	return c.Valid() && c != Once
}

// Parse normalises a cadence string (case-insensitive). Returns an error
// for an unrecognised value so callers can reject bad input at the edge.
func Parse(s string) (Cadence, error) {
	c := Cadence(strings.ToUpper(strings.TrimSpace(s)))
	if !c.Valid() {
		return "", fmt.Errorf("nepoznata učestalost: %q", s)
	}
	return c, nil
}

// Advance returns t moved forward by exactly one cadence interval:
// Daily +1 day, Weekly +7 days, Monthly +1 calendar month (with
// end-of-month clamping). Once (and any invalid cadence) returns t
// unchanged — callers must not advance a non-recurring row.
func Advance(t time.Time, c Cadence) time.Time {
	switch c {
	case Daily:
		return t.AddDate(0, 0, 1)
	case Weekly:
		return t.AddDate(0, 0, 7)
	case Monthly:
		return addMonthClamped(t, 1)
	default:
		return t
	}
}

// NextAfter advances t by whole cadence intervals until it is strictly
// after `now`, returning the first such instant. It always steps at
// least once for a recurring cadence (so a row exactly due is moved to
// its next slot). This is the catch-up form: if the sweep missed
// several intervals (downtime), the row jumps straight to the next
// future slot instead of firing once per missed interval.
//
// For a non-recurring cadence it returns t unchanged.
func NextAfter(t time.Time, c Cadence, now time.Time) time.Time {
	if !c.IsRecurring() {
		return t
	}
	next := Advance(t, c)
	// Guard against a pathological zero-step (shouldn't happen for the
	// recurring cadences) so the loop always terminates.
	for !next.After(now) {
		stepped := Advance(next, c)
		if !stepped.After(next) {
			break
		}
		next = stepped
	}
	return next
}

// IsDue reports whether a row whose next run is nextRun should fire at
// `now` (i.e. now is at or past nextRun).
func IsDue(nextRun, now time.Time) bool {
	return !now.Before(nextRun)
}

// AfterRun computes a scheduled row's state immediately after it fires.
// It returns the new next_run and whether the row should be deactivated.
// A non-recurring (Once) row is deactivated with its next_run unchanged;
// a recurring row stays active and advances to its next future slot
// (catch-up semantics via NextAfter). This is the one decision every
// domain sweep makes after processing a due row, so they share it
// instead of re-deriving it.
func AfterRun(nextRun time.Time, c Cadence, now time.Time) (newNextRun time.Time, deactivate bool) {
	if !c.IsRecurring() {
		return nextRun, true
	}
	return NextAfter(nextRun, c, now), false
}

// ValidateFuture returns an error when t is not strictly after now.
// Used when scheduling a payment/order for a future date (spec:
// "Sistem proverava da li je datum u budućnosti").
func ValidateFuture(t, now time.Time) error {
	if !t.After(now) {
		return fmt.Errorf("datum mora biti u budućnosti")
	}
	return nil
}

// addMonthClamped adds n calendar months to t, clamping the day to the
// last valid day of the target month. We compute on the first of the
// month (which never overflows) then re-attach the clamped day, avoiding
// Go's AddDate normalisation that would turn Jan 31 + 1mo into Mar 3.
func addMonthClamped(t time.Time, n int) time.Time {
	y, m, d := t.Date()
	first := time.Date(y, m, 1, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
	first = first.AddDate(0, n, 0)
	if last := daysInMonth(first.Year(), first.Month()); d > last {
		d = last
	}
	return time.Date(first.Year(), first.Month(), d, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
}

// daysInMonth returns the number of days in the given month. Day 0 of
// the next month is the last day of this month; .Day() reads it back.
func daysInMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}
