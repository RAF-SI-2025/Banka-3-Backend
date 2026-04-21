package trading

import (
	"testing"
	"time"
)

// nyse mirrors the seeded NYSE row: 09:30–16:00 local, UTC-05:00, US calendar.
func nyse() Exchange {
	return Exchange{
		MICCode:        "XNYS",
		Polity:         "United States",
		TimeZoneOffset: "-05:00",
		OpenTime:       "09:30",
		CloseTime:      "16:00",
	}
}

// tse mirrors Tokyo: 09:00–15:00 local, UTC+09:00, JP calendar. Included so
// we cover a positive offset too — easy to get signs wrong.
func tse() Exchange {
	return Exchange{
		MICCode:        "XTKS",
		Polity:         "Japan",
		TimeZoneOffset: "+09:00",
		OpenTime:       "09:00",
		CloseTime:      "15:00",
	}
}

func TestIsOpen_WithinHours(t *testing.T) {
	ex := nyse()
	// Wed 2026-04-22, 14:00 New York → 19:00 UTC
	now := time.Date(2026, 4, 22, 19, 0, 0, 0, time.UTC)
	if !IsOpen(ex, now) {
		t.Fatalf("expected open at NY 14:00 Wed")
	}
}

func TestIsOpen_BeforeOpen(t *testing.T) {
	ex := nyse()
	// Wed 2026-04-22, 09:29 New York → 13:29 UTC (one minute before open)
	now := time.Date(2026, 4, 22, 13, 29, 0, 0, time.UTC)
	if IsOpen(ex, now) {
		t.Fatalf("expected closed at NY 09:29")
	}
}

func TestIsOpen_AtCloseIsClosed(t *testing.T) {
	ex := nyse()
	// Window is [open, close), so 16:00 is already closed
	now := time.Date(2026, 4, 22, 21, 0, 0, 0, time.UTC) // NY 16:00
	if IsOpen(ex, now) {
		t.Fatalf("expected closed at NY 16:00 (exclusive)")
	}
}

func TestIsOpen_Weekend(t *testing.T) {
	ex := nyse()
	// Sat 2026-04-25, mid-day NY
	now := time.Date(2026, 4, 25, 18, 0, 0, 0, time.UTC)
	if IsOpen(ex, now) {
		t.Fatalf("expected closed on Saturday")
	}
}

func TestIsOpen_Holiday(t *testing.T) {
	ex := nyse()
	// Christmas 2026-12-25 is a Friday, 14:00 NY
	now := time.Date(2026, 12, 25, 19, 0, 0, 0, time.UTC)
	if IsOpen(ex, now) {
		t.Fatalf("expected closed on Christmas")
	}
}

func TestIsOpen_Override(t *testing.T) {
	ex := nyse()
	ex.OpenOverride = true
	// Sat 3am NY — normally closed hard, but override wins
	now := time.Date(2026, 4, 25, 8, 0, 0, 0, time.UTC)
	if !IsOpen(ex, now) {
		t.Fatalf("open_override must force open")
	}
}

func TestIsOpen_TimezonePositiveOffset(t *testing.T) {
	ex := tse()
	// Wed 2026-04-22, 10:00 Tokyo → 01:00 UTC same day
	now := time.Date(2026, 4, 22, 1, 0, 0, 0, time.UTC)
	if !IsOpen(ex, now) {
		t.Fatalf("expected open at Tokyo 10:00 Wed")
	}
	// 16:00 Tokyo → 07:00 UTC — closed (window is 09–15)
	closed := time.Date(2026, 4, 22, 7, 0, 0, 0, time.UTC)
	if IsOpen(ex, closed) {
		t.Fatalf("expected closed at Tokyo 16:00")
	}
}

func TestIsAfterHours_WithinLastFourHours(t *testing.T) {
	ex := nyse()
	// Wed 13:00 NY → 2h before 15:00? Actually close is 16:00, so 13:00 is 3h
	// before close → after-hours.
	now := time.Date(2026, 4, 22, 18, 0, 0, 0, time.UTC) // NY 13:00
	if !IsAfterHours(ex, now) {
		t.Fatalf("expected after-hours at NY 13:00 (3h to close)")
	}
}

func TestIsAfterHours_NotYetInWindow(t *testing.T) {
	ex := nyse()
	// NY 10:00 — 6h to close → not after-hours
	now := time.Date(2026, 4, 22, 15, 0, 0, 0, time.UTC)
	if IsAfterHours(ex, now) {
		t.Fatalf("expected false at NY 10:00 (6h to close)")
	}
}

func TestIsAfterHours_OutsideOpen(t *testing.T) {
	ex := nyse()
	// NY 17:00 — market closed
	now := time.Date(2026, 4, 22, 22, 0, 0, 0, time.UTC)
	if IsAfterHours(ex, now) {
		t.Fatalf("after_hours only applies during the open window")
	}
}

func TestIsAfterHours_BoundaryExactlyFourHours(t *testing.T) {
	ex := nyse()
	// Close 16:00, so 12:00 NY is exactly 4h to close — spec says "less than
	// 4h", so 4h exactly is NOT after-hours.
	now := time.Date(2026, 4, 22, 17, 0, 0, 0, time.UTC)
	if IsAfterHours(ex, now) {
		t.Fatalf("exactly 4h to close should not count as after-hours")
	}
	// 12:01 NY → 3h59m to close → after-hours
	now = time.Date(2026, 4, 22, 17, 1, 0, 0, time.UTC)
	if !IsAfterHours(ex, now) {
		t.Fatalf("3h59m to close should be after-hours")
	}
}

func TestIsAfterHours_OverrideDoesNotApply(t *testing.T) {
	ex := nyse()
	ex.OpenOverride = true
	// Sat 3am NY, override is on — IsOpen=true but IsAfterHours=false because
	// we're not in the clock window at all.
	now := time.Date(2026, 4, 25, 8, 0, 0, 0, time.UTC)
	if IsAfterHours(ex, now) {
		t.Fatalf("after_hours is clock-based, not override-based")
	}
}

func TestParseTZOffset(t *testing.T) {
	cases := []struct {
		in   string
		secs int
	}{
		{"+00:00", 0},
		{"-05:00", -5 * 3600},
		{"+09:00", 9 * 3600},
		{"+05:30", 5*3600 + 30*60},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			loc := parseTZOffset(c.in)
			_, off := time.Now().In(loc).Zone()
			if off != c.secs {
				t.Errorf("offset %s: got %d, want %d", c.in, off, c.secs)
			}
		})
	}
}
