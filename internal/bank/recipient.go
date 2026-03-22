package bank

import (
	"context"
	"fmt"
	"math"
	"strings"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/bank"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type paymentRecipientRow struct {
	ID            int64
	Name          string
	AccountNumber string
}

func normalizeRecipientInput(clientID int64, name, accountNumber string) (string, string, error) {
	if clientID <= 0 {
		return "", "", status.Error(codes.InvalidArgument, "client_id must be provided")
	}

	trimmedName := strings.TrimSpace(name)
	if trimmedName == "" {
		return "", "", status.Error(codes.InvalidArgument, "name is required")
	}

	trimmedAccountNumber := strings.TrimSpace(accountNumber)
	if trimmedAccountNumber == "" {
		return "", "", status.Error(codes.InvalidArgument, "account_number is required")
	}

	return trimmedName, trimmedAccountNumber, nil
}

func (s *Server) GetPaymentRecipients(
	ctx context.Context,
	req *bankpb.GetPaymentRecipientsRequest,
) (*bankpb.GetPaymentRecipientsResponse, error) {
	if req.ClientId <= 0 {
		return nil, status.Error(codes.InvalidArgument, "client_id must be provided")
	}

	rows, err := s.database.QueryContext(ctx, `
		SELECT
			id,
			name,
			account_number
		FROM payment_recipients
		WHERE client_id = $1
		ORDER BY id ASC
	`, req.ClientId)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	recipients := make([]*bankpb.PaymentRecipient, 0)

	for rows.Next() {
		var row paymentRecipientRow

		if err := rows.Scan(
			&row.ID,
			&row.Name,
			&row.AccountNumber,
		); err != nil {
			return nil, err
		}

		recipients = append(recipients, &bankpb.PaymentRecipient{
			Id:            row.ID,
			Name:          row.Name,
			AccountNumber: row.AccountNumber,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return &bankpb.GetPaymentRecipientsResponse{
		Recipients: recipients,
	}, nil
}
func (s *Server) CreatePaymentRecipient(
	ctx context.Context,
	req *bankpb.CreatePaymentRecipientRequest,
) (*bankpb.CreatePaymentRecipientResponse, error) {
	name, accountNumber, err := normalizeRecipientInput(req.ClientId, req.Name, req.AccountNumber)
	if err != nil {
		return nil, err
	}

	var recipientID int64
	err = s.database.QueryRowContext(ctx, `
		INSERT INTO payment_recipients (
			client_id,
			name,
			account_number
		)
		VALUES ($1, $2, $3)
		RETURNING id
	`,
		req.ClientId,
		name,
		accountNumber,
	).Scan(&recipientID)
	if err != nil {
		errText := strings.ToLower(err.Error())
		if strings.Contains(errText, "duplicate key") {
			return nil, status.Error(codes.AlreadyExists, "recipient with this account number already exists for this client")
		}
		if strings.Contains(errText, "foreign key") {
			return nil, status.Error(codes.NotFound, "client not found")
		}
		return nil, err
	}

	return &bankpb.CreatePaymentRecipientResponse{
		Recipient: &bankpb.PaymentRecipient{
			Id:            recipientID,
			Name:          name,
			AccountNumber: accountNumber,
		},
	}, nil
}
func (s *Server) UpdatePaymentRecipient(
	ctx context.Context,
	req *bankpb.UpdatePaymentRecipientRequest,
) (*bankpb.UpdatePaymentRecipientResponse, error) {
	if req.Id <= 0 {
		return nil, status.Error(codes.InvalidArgument, "id must be provided")
	}

	name, accountNumber, err := normalizeRecipientInput(req.ClientId, req.Name, req.AccountNumber)
	if err != nil {
		return nil, err
	}

	result, err := s.database.ExecContext(ctx, `
		UPDATE payment_recipients
		SET name = $1,
			account_number = $2,
			updated_at = NOW()
		WHERE id = $3 AND client_id = $4
	`,
		name,
		accountNumber,
		req.Id,
		req.ClientId,
	)
	if err != nil {
		errText := strings.ToLower(err.Error())
		if strings.Contains(errText, "duplicate key") {
			return nil, status.Error(codes.AlreadyExists, "recipient with this account number already exists for this client")
		}
		if strings.Contains(errText, "foreign key") {
			return nil, status.Error(codes.NotFound, "client not found")
		}
		return nil, err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if rowsAffected == 0 {
		return nil, status.Error(codes.NotFound, "payment recipient not found")
	}

	return &bankpb.UpdatePaymentRecipientResponse{
		Recipient: &bankpb.PaymentRecipient{
			Id:            req.Id,
			Name:          name,
			AccountNumber: accountNumber,
		},
	}, nil
}
func (s *Server) DeletePaymentRecipient(
	ctx context.Context,
	req *bankpb.DeletePaymentRecipientRequest,
) (*bankpb.DeletePaymentRecipientResponse, error) {
	if req.Id <= 0 {
		return nil, status.Error(codes.InvalidArgument, "id must be provided")
	}
	if req.ClientId <= 0 {
		return nil, status.Error(codes.InvalidArgument, "client_id must be provided")
	}

	result, err := s.database.ExecContext(ctx, `
		DELETE FROM payment_recipients
		WHERE id = $1 AND client_id = $2
	`, req.Id, req.ClientId)
	if err != nil {
		return nil, err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if rowsAffected == 0 {
		return nil, status.Error(codes.NotFound, "payment recipient not found")
	}

	return &bankpb.DeletePaymentRecipientResponse{
		Success: true,
	}, nil
}
func (s *Server) GetTransactions(
	ctx context.Context,
	req *bankpb.GetTransactionsRequest,
) (*bankpb.GetTransactionsResponse, error) {
	if req.ClientId <= 0 {
		return nil, status.Error(codes.InvalidArgument, "client_id must be provided")
	}

	page := req.Page
	if page <= 0 {
		page = 1
	}

	pageSize := req.PageSize
	if pageSize <= 0 {
		pageSize = 10
	}
	if pageSize > 100 {
		pageSize = 100
	}

	sortBy := normalizeTransactionSortBy(req.SortBy)
	sortOrder := normalizeTransactionSortOrder(req.SortOrder)
	statusFilter := normalizeTransactionStatus(req.Status)

	baseQuery := `
		FROM (
			SELECT
				p.transaction_id AS id,
				'payment' AS type,
				p.from_account,
				p.to_account,
				p.start_amount::double precision AS start_amount,
				p.end_amount::double precision AS end_amount,
				p.commission::double precision AS commission,
				p.status,
				p.timestamp,
				COALESCE(p.recipient_id, 0) AS recipient_id,
				COALESCE(p.transcaction_code::text, '') AS transaction_code,
				COALESCE(p.call_number, '') AS call_number,
				COALESCE(p.reason, '') AS reason,
				0::bigint AS start_currency_id,
				0::double precision AS exchange_rate
				FROM payments p
				JOIN accounts a ON a.number = p.from_account
				WHERE a.owner = $1

			UNION ALL

			SELECT
				t.transaction_id AS id,
				'transfer' AS type,
				t.from_account,
				t.to_account,
				t.start_amount::double precision AS start_amount,
				t.end_amount::double precision AS end_amount,
				t.commission::double precision AS commission,
				t.status,
				t.timestamp,
				0::bigint AS recipient_id,
				''::text AS transaction_code,
				''::text AS call_number,
				''::text AS reason,
				COALESCE(t.start_currency_id, 0) AS start_currency_id,
				COALESCE(t.exchange_rate::double precision, 0) AS exchange_rate
				FROM transfers t
				JOIN accounts a ON a.number = t.from_account
				WHERE a.owner = $1
		) tx
		WHERE 1=1
	`

	args := []interface{}{req.ClientId}
	argPos := 2

	if strings.TrimSpace(req.DateFrom) != "" {
		baseQuery += fmt.Sprintf(" AND tx.timestamp >= $%d::date", argPos)
		args = append(args, req.DateFrom)
		argPos++
	}

	if strings.TrimSpace(req.DateTo) != "" {
		baseQuery += fmt.Sprintf(" AND tx.timestamp < ($%d::date + interval '1 day')", argPos)
		args = append(args, req.DateTo)
		argPos++
	}

	if req.AmountFrom > 0 {
		baseQuery += fmt.Sprintf(" AND tx.start_amount >= $%d", argPos)
		args = append(args, req.AmountFrom)
		argPos++
	}

	if req.AmountTo > 0 {
		baseQuery += fmt.Sprintf(" AND tx.start_amount <= $%d", argPos)
		args = append(args, req.AmountTo)
		argPos++
	}

	if statusFilter != "" {
		if statusFilter != "realized" && statusFilter != "rejected" && statusFilter != "pending" {
			return nil, status.Error(codes.InvalidArgument, "invalid status filter")
		}

		baseQuery += fmt.Sprintf(" AND tx.status = $%d", argPos)
		args = append(args, statusFilter)
		argPos++
	}

	countQuery := "SELECT COUNT(*) " + baseQuery

	var total int64
	if err := s.database.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, err
	}

	listQuery := `
		SELECT
			tx.id,
			tx.type,
			tx.from_account,
			tx.to_account,
			tx.start_amount,
			tx.end_amount,
			tx.commission,
			tx.status,
			tx.timestamp,
			tx.recipient_id,
			tx.transaction_code,
			tx.call_number,
			tx.reason,
			tx.start_currency_id,
			tx.exchange_rate
	` + baseQuery + fmt.Sprintf(`
		ORDER BY %s %s
		LIMIT $%d OFFSET $%d
	`, sortBy, sortOrder, argPos, argPos+1)

	offset := (page - 1) * pageSize
	listArgs := append(args, pageSize, offset)

	rows, err := s.database.QueryContext(ctx, listQuery, listArgs...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	transactions := make([]*bankpb.Transaction, 0)

	for rows.Next() {
		var row transactionListRow

		if err := rows.Scan(
			&row.ID,
			&row.Type,
			&row.FromAccount,
			&row.ToAccount,
			&row.StartAmount,
			&row.EndAmount,
			&row.Commission,
			&row.Status,
			&row.Timestamp,
			&row.RecipientID,
			&row.TransactionCode,
			&row.CallNumber,
			&row.Reason,
			&row.StartCurrencyID,
			&row.ExchangeRate,
		); err != nil {
			return nil, err
		}

		transactions = append(transactions, &bankpb.Transaction{
			Id:              row.ID,
			Type:            row.Type,
			FromAccount:     row.FromAccount,
			ToAccount:       row.ToAccount,
			StartAmount:     row.StartAmount,
			EndAmount:       row.EndAmount,
			Commission:      row.Commission,
			Status:          displayTransactionStatus(row.Status),
			Timestamp:       row.Timestamp.Unix(),
			RecipientId:     row.RecipientID,
			TransactionCode: row.TransactionCode,
			CallNumber:      row.CallNumber,
			Reason:          row.Reason,
			StartCurrencyId: row.StartCurrencyID,
			ExchangeRate:    row.ExchangeRate,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	totalPages := int32(0)
	if total > 0 {
		totalPages = int32(math.Ceil(float64(total) / float64(pageSize)))
	}

	return &bankpb.GetTransactionsResponse{
		Transactions: transactions,
		Page:         page,
		PageSize:     pageSize,
		Total:        total,
		TotalPages:   totalPages,
	}, nil
}
