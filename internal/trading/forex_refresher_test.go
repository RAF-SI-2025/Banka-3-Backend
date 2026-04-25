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

type forexFake struct {
	rates map[string]map[string]float64
	errs  map[string]error
	calls []string
}

func (f *forexFake) GetRates(_ context.Context, base string) (map[string]float64, error) {
	f.calls = append(f.calls, base)
	if err, ok := f.errs[base]; ok {
		return nil, err
	}
	if r, ok := f.rates[base]; ok {
		return r, nil
	}
	return nil, pricing.ErrNotFound
}

func TestForexRefresher_RunOnce_UpdatesPairs(t *testing.T) {
	gdb, mock, raw := newRefresherTestDB(t)
	defer func() { _ = raw.Close() }()

	mock.ExpectQuery(regexp.QuoteMeta(
		`SELECT DISTINCT base_currency FROM "forex_pairs" ORDER BY base_currency`,
	)).WillReturnRows(sqlmock.NewRows([]string{"base_currency"}).AddRow("USD"))

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "forex_pairs" WHERE base_currency = $1`)).
		WithArgs("USD").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "ticker", "name", "base_currency", "quote_currency", "exchange_rate", "liquidity",
		}).
			AddRow(int64(1), "USD/EUR", "x", "USD", "EUR", 0.90, "high").
			AddRow(int64(2), "USD/JPY", "x", "USD", "JPY", 150.0, "high").
			// XYZ has no rate from upstream — must be left untouched.
			AddRow(int64(3), "USD/XYZ", "x", "USD", "XYZ", 1.0, "low"))

	mock.ExpectExec(`UPDATE "forex_pairs"`).
		WithArgs(0.93, int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE "forex_pairs"`).
		WithArgs(151.42, int64(2)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	r := &ForexRefresher{
		DB: gdb,
		Client: &forexFake{rates: map[string]map[string]float64{
			"USD": {"EUR": 0.93, "JPY": 151.42},
		}},
		Interval:     time.Hour,
		PerCallDelay: 0,
	}
	r.runOnce(context.Background())

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

func TestForexRefresher_AbortsOnRateLimit(t *testing.T) {
	gdb, mock, raw := newRefresherTestDB(t)
	defer func() { _ = raw.Close() }()

	mock.ExpectQuery(regexp.QuoteMeta(
		`SELECT DISTINCT base_currency FROM "forex_pairs" ORDER BY base_currency`,
	)).WillReturnRows(sqlmock.NewRows([]string{"base_currency"}).
		AddRow("EUR").
		AddRow("USD"))

	fake := &forexFake{errs: map[string]error{"EUR": pricing.ErrRateLimited}}
	r := &ForexRefresher{
		DB:           gdb,
		Client:       fake,
		Interval:     time.Hour,
		PerCallDelay: 0,
	}
	r.runOnce(context.Background())

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
	for _, c := range fake.calls {
		if c == "USD" {
			t.Errorf("USD was called after rate limit; calls=%v", fake.calls)
		}
	}
}

func TestForexRefresher_TransientErrorContinues(t *testing.T) {
	gdb, mock, raw := newRefresherTestDB(t)
	defer func() { _ = raw.Close() }()

	mock.ExpectQuery(regexp.QuoteMeta(
		`SELECT DISTINCT base_currency FROM "forex_pairs" ORDER BY base_currency`,
	)).WillReturnRows(sqlmock.NewRows([]string{"base_currency"}).
		AddRow("EUR").
		AddRow("USD"))

	// EUR fails transiently. USD proceeds to write.
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "forex_pairs" WHERE base_currency = $1`)).
		WithArgs("USD").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "ticker", "name", "base_currency", "quote_currency", "exchange_rate", "liquidity",
		}).AddRow(int64(2), "USD/EUR", "x", "USD", "EUR", 0.90, "high"))
	mock.ExpectExec(`UPDATE "forex_pairs"`).
		WithArgs(0.91, int64(2)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	r := &ForexRefresher{
		DB: gdb,
		Client: &forexFake{
			errs:  map[string]error{"EUR": errors.New("dns timeout")},
			rates: map[string]map[string]float64{"USD": {"EUR": 0.91}},
		},
		Interval:     time.Hour,
		PerCallDelay: 0,
	}
	r.runOnce(context.Background())

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

func TestForexRefresher_Start_NilClientIsNoop(t *testing.T) {
	r := &ForexRefresher{Client: nil}
	cancel := r.Start()
	cancel()
}
