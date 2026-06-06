package app

import "time"

// nextDailyOccurrence returns the next instant strictly after `now`
// where the local wall clock equals h:m. Mirrors the helper the
// trading service used for its in-process daily crons so the scheduler
// fires at the same wall-clock moments.
func nextDailyOccurrence(now time.Time, h, m int) time.Time {
	loc := now.Location()
	candidate := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, loc)
	if !candidate.After(now) {
		candidate = candidate.AddDate(0, 0, 1)
	}
	return candidate
}

// nextEndOfMonthAfter returns the next instant strictly after `now`
// whose wall-clock is the last day of a month at hour:minute. Used for
// the monthly capital-gains-tax run (spec p.62).
func nextEndOfMonthAfter(now time.Time, hour, minute int) time.Time {
	loc := now.Location()
	year, month, _ := now.Date()
	candidate := time.Date(year, month+1, 0, hour, minute, 0, 0, loc)
	if !candidate.After(now) {
		candidate = time.Date(year, month+2, 0, hour, minute, 0, 0, loc)
	}
	return candidate
}
