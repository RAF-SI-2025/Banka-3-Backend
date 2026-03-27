package bank

import (
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func cardRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "number", "type", "brand", "creation_date", "valid_until", "account_number", "cvv", "card_limit", "status"})
}

func TestGetCardsRecordsAndCardLookups(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, number, type, brand, creation_date, valid_until, account_number, cvv, card_limit, status
		FROM cards`)).
		WillReturnRows(cardRows().
			AddRow(int64(1), "4111", "debit", "visa", now, now.AddDate(5, 0, 0), "A1", "111", int64(1000), "active").
			AddRow(int64(2), "5111", "credit", "mastercard", now, now.AddDate(5, 0, 0), "A2", "222", int64(2000), "blocked"))

	cards, err := server.GetCardsRecords()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cards) != 2 {
		t.Fatalf("expected 2 cards, got %d", len(cards))
	}

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, number, type, brand, creation_date, valid_until, account_number, cvv, card_limit, status
		FROM cards WHERE number = $1`)).
		WithArgs("4111").
		WillReturnRows(cardRows().AddRow(int64(1), "4111", "debit", "visa", now, now.AddDate(5, 0, 0), "A1", "111", int64(1000), "active"))

	cardByNumber, err := server.GetCardByNumberRecord("4111")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cardByNumber.Number != "4111" {
		t.Fatalf("unexpected card number: %s", cardByNumber.Number)
	}

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, number, type, brand, creation_date, valid_until, account_number, cvv, card_limit, status
		FROM cards WHERE id = $1`)).
		WithArgs(int64(2)).
		WillReturnRows(cardRows().AddRow(int64(2), "5111", "credit", "mastercard", now, now.AddDate(5, 0, 0), "A2", "222", int64(2000), "blocked"))

	cardByID, err := server.GetCardByIDRecord(2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cardByID.Id != 2 {
		t.Fatalf("unexpected card id: %d", cardByID.Id)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}
