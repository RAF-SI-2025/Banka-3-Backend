package trading

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/RAF-SI-2025/Banka-3-Backend/internal/trading/pricing"
)

// overviewFake stubs the AV OVERVIEW client so syncer tests don't need
// httptest. The provider is covered separately.
type overviewFake struct {
	overviews map[string]pricing.CompanyOverview
	errs      map[string]error
	calls     []string
}

func (f *overviewFake) GetCompanyOverview(_ context.Context, ticker string) (pricing.CompanyOverview, error) {
	f.calls = append(f.calls, ticker)
	if err, ok := f.errs[ticker]; ok {
		return pricing.CompanyOverview{}, err
	}
	if o, ok := f.overviews[ticker]; ok {
		return o, nil
	}
	return pricing.CompanyOverview{}, pricing.ErrNotFound
}

func loadStocksQuery() string {
	return regexp.QuoteMeta(`SELECT id AS stock_id, ticker AS ticker FROM "stocks" ORDER BY id`)
}

func TestMetadataSyncer_RunOnce_UpdatesStocks(t *testing.T) {
	gdb, mock, raw := newRefresherTestDB(t)
	defer func() { _ = raw.Close() }()

	rows := sqlmock.NewRows([]string{"stock_id", "ticker"}).
		AddRow(int64(1), "AAPL").
		AddRow(int64(2), "MSFT")
	mock.ExpectQuery(loadStocksQuery()).WillReturnRows(rows)

	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "stocks"`).
		WithArgs(0.005, int64(15500000000), int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "stocks"`).
		WithArgs(0.0072, int64(7430000000), int64(2)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	m := &MetadataSyncer{
		DB: gdb,
		Client: &overviewFake{overviews: map[string]pricing.CompanyOverview{
			"AAPL": {Ticker: "AAPL", SharesOutstanding: 15500000000, DividendYield: 0.005},
			"MSFT": {Ticker: "MSFT", SharesOutstanding: 7430000000, DividendYield: 0.0072},
		}},
		Interval:     time.Hour,
		PerCallDelay: 0,
		Now:          time.Now,
	}
	m.runOnce(context.Background())

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestMetadataSyncer_RunOnce_SkipsNotFound(t *testing.T) {
	gdb, mock, raw := newRefresherTestDB(t)
	defer func() { _ = raw.Close() }()

	rows := sqlmock.NewRows([]string{"stock_id", "ticker"}).
		AddRow(int64(1), "RAFA"). // dummy seed
		AddRow(int64(2), "MSFT")
	mock.ExpectQuery(loadStocksQuery()).WillReturnRows(rows)

	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "stocks"`).
		WithArgs(0.0072, int64(7430000000), int64(2)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	m := &MetadataSyncer{
		DB: gdb,
		Client: &overviewFake{overviews: map[string]pricing.CompanyOverview{
			"MSFT": {Ticker: "MSFT", SharesOutstanding: 7430000000, DividendYield: 0.0072},
		}},
		Interval:     time.Hour,
		PerCallDelay: 0,
		Now:          time.Now,
	}
	m.runOnce(context.Background())

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestMetadataSyncer_RunOnce_AbortsOnRateLimit(t *testing.T) {
	gdb, mock, raw := newRefresherTestDB(t)
	defer func() { _ = raw.Close() }()

	rows := sqlmock.NewRows([]string{"stock_id", "ticker"}).
		AddRow(int64(1), "AAPL").
		AddRow(int64(2), "MSFT") // never reached
	mock.ExpectQuery(loadStocksQuery()).WillReturnRows(rows)

	m := &MetadataSyncer{
		DB:           gdb,
		Client:       &overviewFake{errs: map[string]error{"AAPL": pricing.ErrRateLimited}},
		Interval:     time.Hour,
		PerCallDelay: 0,
		Now:          time.Now,
	}
	m.runOnce(context.Background())

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestMetadataSyncer_NilClient_NoOp(t *testing.T) {
	m := NewMetadataSyncer(nil, nil)
	stop := m.Start()
	stop()
}
