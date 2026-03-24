package bank

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/bank"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGetPaymentRecipientsSuccess(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT
			id,
			name,
			account_number
		FROM payment_recipients
		WHERE client_id = $1
		ORDER BY id ASC`)).
		WithArgs(int64(10)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "account_number"}).
			AddRow(int64(1), "Ana", "111").
			AddRow(int64(2), "Mika", "222"))

	resp, err := server.GetPaymentRecipients(context.Background(), &bankpb.GetPaymentRecipientsRequest{ClientId: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Recipients) != 2 {
		t.Fatalf("expected 2 recipients, got %d", len(resp.Recipients))
	}
	if resp.Recipients[0].Name != "Ana" {
		t.Fatalf("unexpected first recipient: %+v", resp.Recipients[0])
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestGetPaymentRecipientsInvalidClient(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	_, err := server.GetPaymentRecipients(context.Background(), &bankpb.GetPaymentRecipientsRequest{ClientId: 0})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", status.Code(err))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestCreatePaymentRecipientDuplicateKey(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO payment_recipients (
			client_id,
			name,
			account_number
		)
		VALUES ($1, $2, $3)
		RETURNING id`)).
		WithArgs(int64(1), "Ana", "123").
		WillReturnError(errors.New("duplicate key value violates unique constraint"))

	_, err := server.CreatePaymentRecipient(context.Background(), &bankpb.CreatePaymentRecipientRequest{
		ClientId:      1,
		Name:          " Ana ",
		AccountNumber: " 123 ",
	})
	if status.Code(err) != codes.AlreadyExists {
		t.Fatalf("expected AlreadyExists, got %v", status.Code(err))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestCreatePaymentRecipientSuccess(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO payment_recipients (
			client_id,
			name,
			account_number
		)
		VALUES ($1, $2, $3)
		RETURNING id`)).
		WithArgs(int64(1), "Ana", "123").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(11)))

	resp, err := server.CreatePaymentRecipient(context.Background(), &bankpb.CreatePaymentRecipientRequest{
		ClientId:      1,
		Name:          " Ana ",
		AccountNumber: " 123 ",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Recipient == nil || resp.Recipient.Id != 11 {
		t.Fatalf("unexpected response: %+v", resp.Recipient)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestUpdatePaymentRecipientNotFound(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	mock.ExpectExec(regexp.QuoteMeta(`UPDATE payment_recipients
		SET name = $1,
			account_number = $2,
			updated_at = NOW()
		WHERE id = $3 AND client_id = $4`)).
		WithArgs("Ana", "123", int64(88), int64(2)).
		WillReturnResult(sqlmock.NewResult(0, 0))

	_, err := server.UpdatePaymentRecipient(context.Background(), &bankpb.UpdatePaymentRecipientRequest{
		Id:            88,
		ClientId:      2,
		Name:          " Ana ",
		AccountNumber: " 123 ",
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound, got %v", status.Code(err))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestUpdatePaymentRecipientValidationAndDuplicate(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	_, err := server.UpdatePaymentRecipient(context.Background(), &bankpb.UpdatePaymentRecipientRequest{Id: 0, ClientId: 1, Name: "A", AccountNumber: "1"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", status.Code(err))
	}

	mock.ExpectExec(regexp.QuoteMeta(`UPDATE payment_recipients
		SET name = $1,
			account_number = $2,
			updated_at = NOW()
		WHERE id = $3 AND client_id = $4`)).
		WithArgs("Ana", "123", int64(2), int64(1)).
		WillReturnError(errors.New("duplicate key value violates unique constraint"))

	_, err = server.UpdatePaymentRecipient(context.Background(), &bankpb.UpdatePaymentRecipientRequest{
		Id:            2,
		ClientId:      1,
		Name:          "Ana",
		AccountNumber: "123",
	})
	if status.Code(err) != codes.AlreadyExists {
		t.Fatalf("expected AlreadyExists, got %v", status.Code(err))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestDeletePaymentRecipientSuccess(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM payment_recipients
		WHERE id = $1 AND client_id = $2`)).
		WithArgs(int64(5), int64(2)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	resp, err := server.DeletePaymentRecipient(context.Background(), &bankpb.DeletePaymentRecipientRequest{
		Id:       5,
		ClientId: 2,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success=true")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestDeletePaymentRecipientValidationAndNotFound(t *testing.T) {
	server, mock, db := newTestServer(t)
	defer func() { _ = db.Close() }()

	_, err := server.DeletePaymentRecipient(context.Background(), &bankpb.DeletePaymentRecipientRequest{Id: 0, ClientId: 1})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for id, got %v", status.Code(err))
	}

	_, err = server.DeletePaymentRecipient(context.Background(), &bankpb.DeletePaymentRecipientRequest{Id: 1, ClientId: 0})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for client id, got %v", status.Code(err))
	}

	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM payment_recipients
		WHERE id = $1 AND client_id = $2`)).
		WithArgs(int64(99), int64(2)).
		WillReturnResult(sqlmock.NewResult(0, 0))

	_, err = server.DeletePaymentRecipient(context.Background(), &bankpb.DeletePaymentRecipientRequest{Id: 99, ClientId: 2})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound, got %v", status.Code(err))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}
