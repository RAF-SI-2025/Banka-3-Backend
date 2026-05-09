package app

import (
	"testing"
	"time"
)

func TestNextDailyOccurrence(t *testing.T) {
	belgrade, err := time.LoadLocation("Europe/Belgrade")
	if err != nil {
		t.Fatalf("load belgrade: %v", err)
	}
	cases := []struct {
		name string
		now  time.Time
		h, m int
		want time.Time
	}{
		{
			name: "before today's slot",
			now:  time.Date(2026, 5, 9, 12, 0, 0, 0, belgrade),
			h:    23, m: 59,
			want: time.Date(2026, 5, 9, 23, 59, 0, 0, belgrade),
		},
		{
			name: "after today's slot",
			now:  time.Date(2026, 5, 9, 23, 59, 1, 0, belgrade),
			h:    23, m: 59,
			want: time.Date(2026, 5, 10, 23, 59, 0, 0, belgrade),
		},
		{
			name: "exactly at slot — must move to next day",
			now:  time.Date(2026, 5, 9, 23, 59, 0, 0, belgrade),
			h:    23, m: 59,
			want: time.Date(2026, 5, 10, 23, 59, 0, 0, belgrade),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := nextDailyOccurrence(tc.now, tc.h, tc.m)
			if !got.Equal(tc.want) {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}

func TestNextEndOfMonthAfter(t *testing.T) {
	belgrade, err := time.LoadLocation("Europe/Belgrade")
	if err != nil {
		t.Fatalf("load belgrade: %v", err)
	}
	cases := []struct {
		name string
		now  time.Time
		h, m int
		want time.Time
	}{
		{
			name: "mid-month resolves to last day",
			now:  time.Date(2026, 5, 9, 12, 0, 0, 0, belgrade),
			h:    23, m: 55,
			want: time.Date(2026, 5, 31, 23, 55, 0, 0, belgrade),
		},
		{
			name: "after this month's slot rolls to next month",
			now:  time.Date(2026, 5, 31, 23, 56, 0, 0, belgrade),
			h:    23, m: 55,
			want: time.Date(2026, 6, 30, 23, 55, 0, 0, belgrade),
		},
		{
			name: "exactly at slot rolls forward",
			now:  time.Date(2026, 5, 31, 23, 55, 0, 0, belgrade),
			h:    23, m: 55,
			want: time.Date(2026, 6, 30, 23, 55, 0, 0, belgrade),
		},
		{
			name: "february in non-leap year",
			now:  time.Date(2027, 2, 3, 9, 0, 0, 0, belgrade),
			h:    23, m: 55,
			want: time.Date(2027, 2, 28, 23, 55, 0, 0, belgrade),
		},
		{
			name: "february in leap year",
			now:  time.Date(2028, 2, 3, 9, 0, 0, 0, belgrade),
			h:    23, m: 55,
			want: time.Date(2028, 2, 29, 23, 55, 0, 0, belgrade),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := nextEndOfMonthAfter(tc.now, tc.h, tc.m)
			if !got.Equal(tc.want) {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}
