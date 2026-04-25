package trading

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/RAF-SI-2025/Banka-3-Backend/internal/trading/pricing"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func newRefresherTestDB(t *testing.T) (*gorm.DB, sqlmock.Sqlmock, *sql.DB) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	gormDB, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	return gormDB, mock, db
}

// pricingFake is a hand-rolled stub so we don't have to spin httptest in
// the refresher tests — the providers are already covered separately.
type pricingFake struct {
	quotes map[string]pricing.Quote
	errs   map[string]error
	calls  []string
}

func (f *pricingFake) Name() string { return "fake" }
func (f *pricingFake) GetQuote(_ context.Context, ticker string) (pricing.Quote, error) {
	f.calls = append(f.calls, ticker)
	if err, ok := f.errs[ticker]; ok {
		return pricing.Quote{}, err
	}
	if q, ok := f.quotes[ticker]; ok {
		return q, nil
	}
	return pricing.Quote{}, pricing.ErrNotFound
}

func TestRefresher_RunOnce_UpdatesPrices(t *testing.T) {
	gdb, mock, raw := newRefresherTestDB(t)
	defer func() { _ = raw.Close() }()

	rows := sqlmock.NewRows([]string{"listing_id", "ticker"}).
		AddRow(int64(1), "AAPL").
		AddRow(int64(2), "MSFT")
	mock.ExpectQuery(regexp.QuoteMeta(
		`SELECT l.id AS listing_id, s.ticker AS ticker FROM listings AS l JOIN stocks s ON s.id = l.stock_id WHERE l.stock_id IS NOT NULL ORDER BY l.id`,
	)).WillReturnRows(rows)

	frozen := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "listings"`).
		WithArgs(int64(17430), int64(17420), frozen, int64(17421), int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "listings"`).
		WithArgs(int64(41250), int64(41240), frozen, int64(41245), int64(2)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	r := &Refresher{
		DB: gdb,
		Client: &pricingFake{quotes: map[string]pricing.Quote{
			"AAPL": {Ticker: "AAPL", PriceCents: 17421, AskCents: 17430, BidCents: 17420},
			"MSFT": {Ticker: "MSFT", PriceCents: 41245, AskCents: 41250, BidCents: 41240},
		}},
		Interval:     time.Hour,
		PerCallDelay: 0,
		Now:          func() time.Time { return frozen },
	}
	r.runOnce(context.Background())

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRefresher_RunOnce_SkipsNotFound(t *testing.T) {
	gdb, mock, raw := newRefresherTestDB(t)
	defer func() { _ = raw.Close() }()

	rows := sqlmock.NewRows([]string{"listing_id", "ticker"}).
		AddRow(int64(1), "RAFA"). // dummy ticker — not on either provider
		AddRow(int64(2), "AAPL")
	mock.ExpectQuery(regexp.QuoteMeta(
		`SELECT l.id AS listing_id, s.ticker AS ticker FROM listings AS l JOIN stocks s ON s.id = l.stock_id WHERE l.stock_id IS NOT NULL ORDER BY l.id`,
	)).WillReturnRows(rows)

	// Only AAPL should produce an UPDATE.
	frozen := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "listings"`).
		WithArgs(int64(17430), int64(17420), frozen, int64(17421), int64(2)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	r := &Refresher{
		DB: gdb,
		Client: &pricingFake{quotes: map[string]pricing.Quote{
			"AAPL": {Ticker: "AAPL", PriceCents: 17421, AskCents: 17430, BidCents: 17420},
		}},
		Interval:     time.Hour,
		PerCallDelay: 0,
		Now:          func() time.Time { return frozen },
	}
	r.runOnce(context.Background())

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRefresher_RunOnce_AbortsOnRateLimit(t *testing.T) {
	gdb, mock, raw := newRefresherTestDB(t)
	defer func() { _ = raw.Close() }()

	rows := sqlmock.NewRows([]string{"listing_id", "ticker"}).
		AddRow(int64(1), "AAPL").
		AddRow(int64(2), "MSFT").
		AddRow(int64(3), "GOOG")
	mock.ExpectQuery(regexp.QuoteMeta(
		`SELECT l.id AS listing_id, s.ticker AS ticker FROM listings AS l JOIN stocks s ON s.id = l.stock_id WHERE l.stock_id IS NOT NULL ORDER BY l.id`,
	)).WillReturnRows(rows)

	frozen := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	// AAPL succeeds, MSFT triggers ErrRateLimited, GOOG should never be called.
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "listings"`).
		WithArgs(int64(17430), int64(17420), frozen, int64(17421), int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	fake := &pricingFake{
		quotes: map[string]pricing.Quote{
			"AAPL": {Ticker: "AAPL", PriceCents: 17421, AskCents: 17430, BidCents: 17420},
		},
		errs: map[string]error{
			"MSFT": pricing.ErrRateLimited,
		},
	}
	r := &Refresher{
		DB:           gdb,
		Client:       fake,
		Interval:     time.Hour,
		PerCallDelay: 0,
		Now:          func() time.Time { return frozen },
	}
	r.runOnce(context.Background())

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
	// GOOG must not have been queried after the rate-limit short-circuit.
	for _, c := range fake.calls {
		if c == "GOOG" {
			t.Errorf("GOOG was queried after rate limit; calls=%v", fake.calls)
		}
	}
}

func TestRefresher_RunOnce_TransientErrorContinues(t *testing.T) {
	gdb, mock, raw := newRefresherTestDB(t)
	defer func() { _ = raw.Close() }()

	rows := sqlmock.NewRows([]string{"listing_id", "ticker"}).
		AddRow(int64(1), "AAPL").
		AddRow(int64(2), "MSFT")
	mock.ExpectQuery(regexp.QuoteMeta(
		`SELECT l.id AS listing_id, s.ticker AS ticker FROM listings AS l JOIN stocks s ON s.id = l.stock_id WHERE l.stock_id IS NOT NULL ORDER BY l.id`,
	)).WillReturnRows(rows)

	frozen := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	// AAPL fails with a transient error (logged + skipped). MSFT proceeds.
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "listings"`).
		WithArgs(int64(41250), int64(41240), frozen, int64(41245), int64(2)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	r := &Refresher{
		DB: gdb,
		Client: &pricingFake{
			quotes: map[string]pricing.Quote{
				"MSFT": {Ticker: "MSFT", PriceCents: 41245, AskCents: 41250, BidCents: 41240},
			},
			errs: map[string]error{"AAPL": errors.New("connection refused")},
		},
		Interval:     time.Hour,
		PerCallDelay: 0,
		Now:          func() time.Time { return frozen },
	}
	r.runOnce(context.Background())

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRefresher_Start_NilClientIsNoop(t *testing.T) {
	r := &Refresher{Client: nil}
	cancel := r.Start()
	cancel() // must not panic and must not have spawned a loop
}
