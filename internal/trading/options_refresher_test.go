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

func newOptionsTestDB(t *testing.T) (*gorm.DB, sqlmock.Sqlmock, *sql.DB) {
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

type optionsChainFake struct {
	chains map[string][]pricing.OptionContract
	errs   map[string]error
	calls  []string
}

func (f *optionsChainFake) GetOptionsChain(_ context.Context, ticker string) ([]pricing.OptionContract, error) {
	f.calls = append(f.calls, ticker)
	if err, ok := f.errs[ticker]; ok {
		return nil, err
	}
	if c, ok := f.chains[ticker]; ok {
		return c, nil
	}
	return nil, pricing.ErrNotFound
}

func TestOptionsRefresher_RunOnce_UpdatesMatches(t *testing.T) {
	gdb, mock, raw := newOptionsTestDB(t)
	defer func() { _ = raw.Close() }()

	settle := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)

	// loadTargets: one stock with options.
	mock.ExpectQuery(regexp.QuoteMeta(
		`SELECT s.id AS stock_id, s.ticker AS ticker FROM stocks AS s JOIN options o ON o.stock_id = s.id GROUP BY s.id, s.ticker ORDER BY s.id`,
	)).WillReturnRows(sqlmock.NewRows([]string{"stock_id", "ticker"}).AddRow(int64(7), "AAPL"))

	// Inner load of options for the stock.
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "options" WHERE stock_id = $1`)).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "ticker", "name", "stock_id", "option_type",
			"strike_price", "premium", "implied_volatility", "open_interest", "settlement_date",
		}).
			AddRow(int64(101), "AAPL_20260515_00015000_C", "x", int64(7), "call",
				int64(15000), int64(500), 1.0, int64(0), settle).
			AddRow(int64(102), "AAPL_20260515_00015000_P", "x", int64(7), "put",
				int64(15000), int64(200), 1.0, int64(0), settle).
			// row 103 has no provider counterpart (different strike) — must be left alone.
			AddRow(int64(103), "AAPL_20260515_00099900_C", "x", int64(7), "call",
				int64(99900), int64(50), 1.0, int64(0), settle))

	mock.ExpectBegin()
	// Two updates, one per matched row (call then put).
	mock.ExpectExec(`UPDATE "options"`).
		WithArgs(0.42, int64(1230), int64(101)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE "options"`).
		WithArgs(0.39, int64(280), int64(102)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	r := &OptionsRefresher{
		DB: gdb,
		Client: &optionsChainFake{chains: map[string][]pricing.OptionContract{
			"AAPL": {
				{OptionType: "call", StrikeCents: 15000, PremiumCents: 1230, ImpliedVolatility: 0.42, SettlementDate: settle},
				{OptionType: "put", StrikeCents: 15000, PremiumCents: 280, ImpliedVolatility: 0.39, SettlementDate: settle},
			},
		}},
		Interval:     time.Hour,
		PerCallDelay: 0,
		Now:          func() time.Time { return time.Now() },
	}
	r.runOnce(context.Background())

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

func TestOptionsRefresher_RunOnce_AbortsOnRateLimit(t *testing.T) {
	gdb, mock, raw := newOptionsTestDB(t)
	defer func() { _ = raw.Close() }()

	mock.ExpectQuery(regexp.QuoteMeta(
		`SELECT s.id AS stock_id, s.ticker AS ticker FROM stocks AS s JOIN options o ON o.stock_id = s.id GROUP BY s.id, s.ticker ORDER BY s.id`,
	)).WillReturnRows(sqlmock.NewRows([]string{"stock_id", "ticker"}).
		AddRow(int64(1), "AAPL").
		AddRow(int64(2), "MSFT"))

	fake := &optionsChainFake{
		errs: map[string]error{"AAPL": pricing.ErrRateLimited},
	}
	r := &OptionsRefresher{
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
		if c == "MSFT" {
			t.Errorf("MSFT was called after rate limit; calls=%v", fake.calls)
		}
	}
}

func TestOptionsRefresher_NotFoundIsSkipped(t *testing.T) {
	gdb, mock, raw := newOptionsTestDB(t)
	defer func() { _ = raw.Close() }()

	mock.ExpectQuery(regexp.QuoteMeta(
		`SELECT s.id AS stock_id, s.ticker AS ticker FROM stocks AS s JOIN options o ON o.stock_id = s.id GROUP BY s.id, s.ticker ORDER BY s.id`,
	)).WillReturnRows(sqlmock.NewRows([]string{"stock_id", "ticker"}).AddRow(int64(99), "RAFA"))

	r := &OptionsRefresher{
		DB:           gdb,
		Client:       &optionsChainFake{errs: map[string]error{"RAFA": pricing.ErrNotFound}},
		Interval:     time.Hour,
		PerCallDelay: 0,
	}
	r.runOnce(context.Background())

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

func TestOptionsRefresher_Start_NilClientIsNoop(t *testing.T) {
	r := &OptionsRefresher{Client: nil}
	cancel := r.Start()
	cancel()
}

func TestOptionsRefresher_TransientErrorContinues(t *testing.T) {
	gdb, mock, raw := newOptionsTestDB(t)
	defer func() { _ = raw.Close() }()

	mock.ExpectQuery(regexp.QuoteMeta(
		`SELECT s.id AS stock_id, s.ticker AS ticker FROM stocks AS s JOIN options o ON o.stock_id = s.id GROUP BY s.id, s.ticker ORDER BY s.id`,
	)).WillReturnRows(sqlmock.NewRows([]string{"stock_id", "ticker"}).
		AddRow(int64(1), "AAPL").
		AddRow(int64(2), "MSFT"))

	settle := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)
	// AAPL fails transiently — refresher should log + continue to MSFT.
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM "options" WHERE stock_id = $1`)).
		WithArgs(int64(2)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "ticker", "name", "stock_id", "option_type",
			"strike_price", "premium", "implied_volatility", "open_interest", "settlement_date",
		}).AddRow(int64(50), "x", "x", int64(2), "call",
			int64(20000), int64(100), 1.0, int64(0), settle))
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "options"`).
		WithArgs(0.5, int64(150), int64(50)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	r := &OptionsRefresher{
		DB: gdb,
		Client: &optionsChainFake{
			errs: map[string]error{"AAPL": errors.New("connection reset")},
			chains: map[string][]pricing.OptionContract{
				"MSFT": {{OptionType: "call", StrikeCents: 20000, PremiumCents: 150, ImpliedVolatility: 0.5, SettlementDate: settle}},
			},
		},
		Interval:     time.Hour,
		PerCallDelay: 0,
	}
	r.runOnce(context.Background())

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}
