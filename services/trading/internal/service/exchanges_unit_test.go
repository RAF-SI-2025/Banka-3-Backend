package service

import (
	"testing"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
)

func TestResolveMarketState(t *testing.T) {
	belgrade, err := time.LoadLocation("Europe/Belgrade")
	if err != nil {
		t.Fatalf("load belgrade: %v", err)
	}
	s := &Service{}

	mkExchange := func(open, close string, override *domain.ExchangeOverrideState) *domain.Exchange {
		return &domain.Exchange{
			MIC:           "XBEL",
			Timezone:      "Europe/Belgrade",
			OpenLocal:     open,
			CloseLocal:    close,
			OverrideState: override,
		}
	}
	overrideOpen := domain.ExchangeOverrideOpen
	overrideClosed := domain.ExchangeOverrideClosed
	overrideAfterHours := domain.ExchangeOverrideAfterHours
	at := func(date string) time.Time {
		v, err := time.ParseInLocation("2006-01-02 15:04", date, belgrade)
		if err != nil {
			t.Fatalf("parse %q: %v", date, err)
		}
		return v
	}

	cases := []struct {
		name           string
		ex             *domain.Exchange
		now            time.Time
		wantOpen       bool
		wantAfterHours bool
	}{
		{"override open", mkExchange("09:00", "17:00", &overrideOpen), at("2026-05-09 22:00"), true, false},
		{"override closed", mkExchange("09:00", "17:00", &overrideClosed), at("2026-05-09 12:00"), false, false},
		// after_hours override forces closed+within-window regardless of wall-clock
		// (weekend at would-be open in this case).
		{"override after-hours weekend", mkExchange("09:30", "16:00", &overrideAfterHours), at("2026-05-09 12:00"), false, true},
		{"override after-hours middle of session", mkExchange("09:30", "16:00", &overrideAfterHours), at("2026-05-11 12:00"), false, true},
		{"weekday before open", mkExchange("09:30", "16:00", nil), at("2026-05-11 09:00"), false, false}, // Mon 09:00
		{"weekday during", mkExchange("09:30", "16:00", nil), at("2026-05-11 12:00"), true, false},
		{"weekday just after close", mkExchange("09:30", "16:00", nil), at("2026-05-11 17:00"), false, true},  // 1h after close
		{"weekday well after close", mkExchange("09:30", "16:00", nil), at("2026-05-11 21:00"), false, false}, // >4h after close
		{"saturday during would-be hours", mkExchange("09:30", "16:00", nil), at("2026-05-09 12:00"), false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ms := s.resolveMarketState(tc.ex, tc.now)
			if ms.IsOpen != tc.wantOpen {
				t.Errorf("is_open: got %v want %v", ms.IsOpen, tc.wantOpen)
			}
			if ms.IsAfterHours != tc.wantAfterHours {
				t.Errorf("after_hours: got %v want %v", ms.IsAfterHours, tc.wantAfterHours)
			}
		})
	}
}

func TestParseHHMM(t *testing.T) {
	cases := []struct {
		in     string
		okWant bool
	}{
		{"09:30", true},
		{"00:00", true},
		{"23:59", true},
		{"24:00", false},
		{"09:60", false},
		{"9:30", true},
		{"abc:30", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			_, err := parseHHMM(tc.in)
			gotOK := err == nil
			if gotOK != tc.okWant {
				t.Fatalf("got ok=%v err=%v, want ok=%v", gotOK, err, tc.okWant)
			}
		})
	}
}
