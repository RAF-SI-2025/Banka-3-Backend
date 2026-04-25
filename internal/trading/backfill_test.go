package trading

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/RAF-SI-2025/Banka-3-Backend/internal/trading/pricing"
)

// dailyHistoryFake stubs the AV daily-history client so backfiller tests don't
// need httptest. The real provider is covered separately.
type dailyHistoryFake struct {
	bars  map[string][]pricing.DailyBar
	errs  map[string]error
	calls []string
}

func (f *dailyHistoryFake) GetDailyHistory(_ context.Context, ticker string) ([]pricing.DailyBar, error) {
	f.calls = append(f.calls, ticker)
	if err, ok := f.errs[ticker]; ok {
		return nil, err
	}
	if b, ok := f.bars[ticker]; ok {
		return b, nil
	}
	return nil, pricing.ErrNotFound
}

func loadTargetsQuery() string {
	return regexp.QuoteMeta(
		`SELECT l.id AS listing_id, s.ticker AS ticker FROM listings AS l JOIN stocks s ON s.id = l.stock_id WHERE l.stock_id IS NOT NULL ORDER BY l.id`,
	)
}

func TestBackfiller_RunOnce_UpsertsLastWindow(t *testing.T) {
	gdb, mock, raw := newRefresherTestDB(t)
	defer func() { _ = raw.Close() }()

	rows := sqlmock.NewRows([]string{"listing_id", "ticker"}).
		AddRow(int64(1), "AAPL")
	mock.ExpectQuery(loadTargetsQuery()).WillReturnRows(rows)

	// Two-day window covered by three returned bars — oldest must be dropped.
	d24 := time.Date(2026, 4, 24, 0, 0, 0, 0, time.UTC)
	d23 := time.Date(2026, 4, 23, 0, 0, 0, 0, time.UTC)
	d22 := time.Date(2026, 4, 22, 0, 0, 0, 0, time.UTC)

	mock.ExpectBegin()
	// Newest-first: 04-24 first.
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO listing_daily_price_info`)).
		WithArgs(int64(1), d24, int64(41710), int64(41710), int64(41710), int64(41710-41200)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO listing_daily_price_info`)).
		WithArgs(int64(1), d23, int64(41245), int64(41245), int64(41245), int64(41245-41000)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	r := &Backfiller{
		DB: gdb,
		Client: &dailyHistoryFake{bars: map[string][]pricing.DailyBar{
			"AAPL": {
				{Date: d24, OpenCents: 41200, HighCents: 41800, LowCents: 41150, CloseCents: 41710},
				{Date: d23, OpenCents: 41000, HighCents: 41500, LowCents: 40800, CloseCents: 41245},
				{Date: d22, OpenCents: 40800, HighCents: 41200, LowCents: 40550, CloseCents: 41000},
			},
		}},
		Interval:     time.Hour,
		PerCallDelay: 0,
		WindowDays:   2,
		Now:          func() time.Time { return d24 },
	}
	r.runOnce(context.Background())

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestBackfiller_RunOnce_SkipsNotFound(t *testing.T) {
	gdb, mock, raw := newRefresherTestDB(t)
	defer func() { _ = raw.Close() }()

	rows := sqlmock.NewRows([]string{"listing_id", "ticker"}).
		AddRow(int64(1), "RAFA"). // dummy, no AV data
		AddRow(int64(2), "AAPL")
	mock.ExpectQuery(loadTargetsQuery()).WillReturnRows(rows)

	d24 := time.Date(2026, 4, 24, 0, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO listing_daily_price_info`)).
		WithArgs(int64(2), d24, int64(17421), int64(17421), int64(17421), int64(17421-17400)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	r := &Backfiller{
		DB: gdb,
		Client: &dailyHistoryFake{bars: map[string][]pricing.DailyBar{
			"AAPL": {
				{Date: d24, OpenCents: 17400, HighCents: 17500, LowCents: 17300, CloseCents: 17421},
			},
		}},
		Interval:     time.Hour,
		PerCallDelay: 0,
		WindowDays:   30,
		Now:          func() time.Time { return d24 },
	}
	r.runOnce(context.Background())

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestBackfiller_RunOnce_AbortsOnRateLimit(t *testing.T) {
	gdb, mock, raw := newRefresherTestDB(t)
	defer func() { _ = raw.Close() }()

	rows := sqlmock.NewRows([]string{"listing_id", "ticker"}).
		AddRow(int64(1), "AAPL").
		AddRow(int64(2), "MSFT").
		AddRow(int64(3), "GOOG")
	mock.ExpectQuery(loadTargetsQuery()).WillReturnRows(rows)

	d24 := time.Date(2026, 4, 24, 0, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO listing_daily_price_info`)).
		WithArgs(int64(1), d24, int64(17421), int64(17421), int64(17421), int64(17421-17400)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	fake := &dailyHistoryFake{
		bars: map[string][]pricing.DailyBar{
			"AAPL": {{Date: d24, OpenCents: 17400, CloseCents: 17421, HighCents: 17500, LowCents: 17300}},
		},
		errs: map[string]error{"MSFT": pricing.ErrRateLimited},
	}
	r := &Backfiller{
		DB:           gdb,
		Client:       fake,
		Interval:     time.Hour,
		PerCallDelay: 0,
		WindowDays:   30,
		Now:          func() time.Time { return d24 },
	}
	r.runOnce(context.Background())

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
	for _, c := range fake.calls {
		if c == "GOOG" {
			t.Errorf("GOOG queried after rate-limit; calls=%v", fake.calls)
		}
	}
}

func TestBackfiller_RunOnce_TransientErrorContinues(t *testing.T) {
	gdb, mock, raw := newRefresherTestDB(t)
	defer func() { _ = raw.Close() }()

	rows := sqlmock.NewRows([]string{"listing_id", "ticker"}).
		AddRow(int64(1), "AAPL").
		AddRow(int64(2), "MSFT")
	mock.ExpectQuery(loadTargetsQuery()).WillReturnRows(rows)

	d24 := time.Date(2026, 4, 24, 0, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO listing_daily_price_info`)).
		WithArgs(int64(2), d24, int64(41245), int64(41245), int64(41245), int64(41245-41000)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	r := &Backfiller{
		DB: gdb,
		Client: &dailyHistoryFake{
			bars: map[string][]pricing.DailyBar{
				"MSFT": {{Date: d24, OpenCents: 41000, CloseCents: 41245, HighCents: 41500, LowCents: 40900}},
			},
			errs: map[string]error{"AAPL": errors.New("connection refused")},
		},
		Interval:     time.Hour,
		PerCallDelay: 0,
		WindowDays:   30,
		Now:          func() time.Time { return d24 },
	}
	r.runOnce(context.Background())

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestBackfiller_Start_NilClientIsNoop(t *testing.T) {
	r := &Backfiller{Client: nil}
	cancel := r.Start()
	cancel()
}
