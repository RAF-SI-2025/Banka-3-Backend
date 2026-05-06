package trading

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// polityHolidays lists fixed 2026 market holidays per polity. The spec (p.40)
// lets us simplify by treating every exchange in the same polity as sharing a
// calendar, so we key by polity rather than MIC. Hardcoded (not a table) — we
// only need a handful of dates to exercise the open-check logic.
var polityHolidays = map[string]map[string]struct{}{
	"United States": {
		"2026-01-01": {}, // New Year's Day
		"2026-01-19": {}, // MLK Day
		"2026-02-16": {}, // Presidents' Day
		"2026-04-03": {}, // Good Friday
		"2026-05-25": {}, // Memorial Day
		"2026-06-19": {}, // Juneteenth
		"2026-07-03": {}, // Independence Day (observed)
		"2026-09-07": {}, // Labor Day
		"2026-11-26": {}, // Thanksgiving
		"2026-12-25": {}, // Christmas
	},
	"United Kingdom": {
		"2026-01-01": {},
		"2026-04-03": {}, // Good Friday
		"2026-04-06": {}, // Easter Monday
		"2026-05-04": {}, // Early May Bank Holiday
		"2026-05-25": {}, // Spring Bank Holiday
		"2026-08-31": {}, // Summer Bank Holiday
		"2026-12-25": {},
		"2026-12-28": {}, // Boxing Day (observed)
	},
	"Japan": {
		"2026-01-01": {},
		"2026-01-02": {},
		"2026-01-03": {},
		"2026-12-31": {},
	},
}

// parseTZOffset turns "±HH:MM" into a fixed-offset *time.Location. Exchanges
// store a static offset (no DST), so FixedZone is sufficient. On malformed
// input we fall back to UTC so IsOpen stays a best-effort "closed" rather
// than crashing the caller.
func parseTZOffset(s string) *time.Location {
	s = strings.TrimSpace(s)
	if len(s) < 3 {
		return time.UTC
	}
	sign := 1
	switch s[0] {
	case '+':
		s = s[1:]
	case '-':
		sign = -1
		s = s[1:]
	}
	parts := strings.SplitN(s, ":", 2)
	h, err := strconv.Atoi(parts[0])
	if err != nil {
		return time.UTC
	}
	m := 0
	if len(parts) == 2 {
		m, err = strconv.Atoi(parts[1])
		if err != nil {
			return time.UTC
		}
	}
	return time.FixedZone(s, sign*(h*3600+m*60))
}

// parseClockHM parses "HH:MM" or "HH:MM:SS" into minute-of-day.
func parseClockHM(s string) (int, error) {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, ":")
	if len(parts) < 2 {
		return 0, fmt.Errorf("invalid time %q", s)
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, err
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, err
	}
	return h*60 + m, nil
}

// withinClockWindow returns (open, closes, stillTrading) where stillTrading
// is true iff the local time is inside the [open, close) window, weekend and
// holidays excluded. open_override is NOT consulted here — callers handle it.
func withinClockWindow(ex Exchange, t time.Time) (openMin, closeMin, nowMin int, trading bool) {
	loc := parseTZOffset(ex.TimeZoneOffset)
	local := t.In(loc)
	if local.Weekday() == time.Saturday || local.Weekday() == time.Sunday {
		return 0, 0, 0, false
	}
	if _, hit := polityHolidays[ex.Polity][local.Format("2006-01-02")]; hit {
		return 0, 0, 0, false
	}
	openMin, err := parseClockHM(ex.OpenTime)
	if err != nil {
		return 0, 0, 0, false
	}
	closeMin, err = parseClockHM(ex.CloseTime)
	if err != nil {
		return 0, 0, 0, false
	}
	nowMin = local.Hour()*60 + local.Minute()
	trading = nowMin >= openMin && nowMin < closeMin
	return openMin, closeMin, nowMin, trading
}

// IsOpen reports whether the exchange accepts orders at instant t.
// closed_override wins: when set, the exchange is force-closed regardless of
// open_override or the wall clock (spec #46 / cypress force-closed flow).
// open_override is the inverse supervisor toggle for testing outside market
// hours. Without either override, we check TZ, weekends, holidays, and
// working hours.
func IsOpen(ex Exchange, t time.Time) bool {
	if ex.ClosedOverride {
		return false
	}
	if ex.OpenOverride {
		return true
	}
	_, _, _, trading := withinClockWindow(ex, t)
	return trading
}

// IsAfterHours reports whether an order placed at t should be flagged as
// after-hours — i.e. fills should carry the 30-min executor delay bonus.
// Three triggers:
//  1. closed_override set — force-closed; the order will queue until the
//     supervisor reopens the exchange and should fill slowly when it does
//     (spec #46).
//  2. exchange is naturally closed (weekend, holiday, outside working hours)
//     and no override is in play. Same reasoning as #1.
//  3. order placed during open hours within the last 4h before close
//     (spec p.50 strict definition).
//
// open_override suppresses after-hours: it's the "treat as a fresh open day"
// toggle, and the 30-min penalty presumes a real close is approaching, which
// is precisely what the override negates. Without this, suite runs become
// wall-clock-dependent — a 13:00 NY run with override-on would flag every
// fresh order as after-hours and time out the executor specs.
func IsAfterHours(ex Exchange, t time.Time) bool {
	if ex.ClosedOverride {
		return true
	}
	if ex.OpenOverride {
		return false
	}
	_, closeMin, nowMin, trading := withinClockWindow(ex, t)
	if !trading {
		return true
	}
	return closeMin-nowMin < 4*60
}
