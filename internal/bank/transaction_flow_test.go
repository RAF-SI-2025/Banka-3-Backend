package bank

import (
	"context"
	"database/sql"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/bank"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func transactionRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "type", "from_account", "to_account", "start_amount", "end_amount", "commission", "status", "timestamp",
		"recipient_id", "transaction_code", "call_number", "reason", "start_currency_id", "exchange_rate",
	})
}

func TestGetTransactionsSuccess(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*)")).
		WithArgs(int64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(2)))

	mock.ExpectQuery(regexp.QuoteMeta("SELECT\n\t\t\ttx.id,")).
		WithArgs(int64(1), int32(10), int32(0)).
		WillReturnRows(transactionRows().
			AddRow(int64(1), "payment", "ACC1", "ACC2", 100.0, 98.0, 2.0, "realized", now, int64(7), "289", "97-11", "Test", int64(0), 0.0).
			AddRow(int64(2), "transfer", "ACC1", "ACC3", 200.0, 199.0, 1.0, "pending", now, int64(0), "", "", "", int64(1), 117.35))

	resp, err := server.GetTransactions(context.Background(), &bankpb.GetTransactionsRequest{
		ClientId: 1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Transactions) != 2 {
		t.Fatalf("expected 2 transactions, got %d", len(resp.Transactions))
	}
	if resp.Total != 2 || resp.TotalPages != 1 {
		t.Fatalf("unexpected pagination: total=%d pages=%d", resp.Total, resp.TotalPages)
	}
	if resp.Transactions[0].Status != "Realizovano" {
		t.Fatalf("expected localized status, got %q", resp.Transactions[0].Status)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestGetTransactionsInvalidStatusFilter(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	_, err := server.GetTransactions(context.Background(), &bankpb.GetTransactionsRequest{
		ClientId: 1,
		Status:   "unknown-status",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", status.Code(err))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestGetTransactionByIdPaymentAndNotFound(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT\n\t\t\tp.transaction_id AS id,")).
		WithArgs(int64(7), int64(99)).
		WillReturnRows(transactionRows().AddRow(int64(99), "payment", "A1", "A2", 100.0, 97.0, 3.0, "rejected", now, int64(5), "241", "11-22", "Reason", int64(0), 0.0))

	resp, err := server.GetTransactionById(context.Background(), &bankpb.GetTransactionByIdRequest{
		ClientId: 7,
		Id:       99,
		Type:     "payment",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Transaction == nil || resp.Transaction.Id != 99 {
		t.Fatalf("unexpected transaction response: %+v", resp.Transaction)
	}
	if resp.Transaction.Status != "Odbijeno" {
		t.Fatalf("expected translated status, got %q", resp.Transaction.Status)
	}

	mock.ExpectQuery(regexp.QuoteMeta("SELECT\n\t\t\tt.transaction_id AS id,")).
		WithArgs(int64(7), int64(1000)).
		WillReturnError(sql.ErrNoRows)

	_, err = server.GetTransactionById(context.Background(), &bankpb.GetTransactionByIdRequest{
		ClientId: 7,
		Id:       1000,
		Type:     "transfer",
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound, got %v", status.Code(err))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestGenerateTransactionPdfSuccess(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT\n\t\t\tp.transaction_id AS id,")).
		WithArgs(int64(3), int64(33)).
		WillReturnRows(transactionRows().AddRow(int64(33), "payment", "A1", "A2", 150.0, 148.0, 2.0, "realized", now, int64(9), "289", "11-44", "Invoice", int64(0), 0.0))

	resp, err := server.GenerateTransactionPdf(context.Background(), &bankpb.GenerateTransactionPdfRequest{
		ClientId: 3,
		Id:       33,
		Type:     "payment",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.FileName != "transaction_33.pdf" {
		t.Fatalf("unexpected filename: %s", resp.FileName)
	}
	if len(resp.Pdf) == 0 || !strings.HasPrefix(string(resp.Pdf), "%PDF") {
		t.Fatalf("expected valid pdf bytes")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestGenerateTransactionPdfInvalidType(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	_, err := server.GenerateTransactionPdf(context.Background(), &bankpb.GenerateTransactionPdfRequest{
		ClientId: 3,
		Id:       33,
		Type:     "invalid",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", status.Code(err))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestGetTransactionsWithFiltersAndPaginationNormalization(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	now := time.Now()
	mock.ExpectQuery("SELECT COUNT\\(\\*\\)").
		WithArgs(int64(3), "2026-01-01", "2026-01-31", float64(10), float64(1000), "realized").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(1)))

	mock.ExpectQuery("SELECT\\s+tx.id,").
		WithArgs(int64(3), "2026-01-01", "2026-01-31", float64(10), float64(1000), "realized", int32(100), int32(0)).
		WillReturnRows(transactionRows().AddRow(int64(10), "payment", "A", "B", 10.0, 9.0, 1.0, "realized", now, int64(1), "289", "97", "rent", int64(0), 0.0))

	resp, err := server.GetTransactions(context.Background(), &bankpb.GetTransactionsRequest{
		ClientId:   3,
		DateFrom:   "2026-01-01",
		DateTo:     "2026-01-31",
		AmountFrom: 10,
		AmountTo:   1000,
		Status:     "realizovano",
		Page:       0,
		PageSize:   150,
		SortBy:     "commission",
		SortOrder:  "asc",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Page != 1 || resp.PageSize != 100 {
		t.Fatalf("unexpected normalized pagination: page=%d size=%d", resp.Page, resp.PageSize)
	}
	if len(resp.Transactions) != 1 {
		t.Fatalf("expected one transaction")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestGetTransactionByIdInvalidInput(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	_, err := server.GetTransactionById(context.Background(), &bankpb.GetTransactionByIdRequest{ClientId: 0, Id: 1, Type: "payment"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for client id, got %v", status.Code(err))
	}

	_, err = server.GetTransactionById(context.Background(), &bankpb.GetTransactionByIdRequest{ClientId: 1, Id: 0, Type: "payment"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for id, got %v", status.Code(err))
	}

	_, err = server.GetTransactionById(context.Background(), &bankpb.GetTransactionByIdRequest{ClientId: 1, Id: 1, Type: "bad"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for type, got %v", status.Code(err))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}
