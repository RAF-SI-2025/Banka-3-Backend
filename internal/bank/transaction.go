package bank

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"time"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/bank"
	"github.com/go-pdf/fpdf"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GetTransactions handles the retrieval of a paginated list of transactions for the authenticated client.
// It filters by the client's account numbers and applies any additional criteria provided in the request.
func (s *Server) GetTransactions(ctx context.Context, req *bankpb.GetTransactionsRequest) (*bankpb.GetTransactionsResponse, error) {
	// 1. Retrieve client email and ID from context metadata
	email, err := s.GetEmailFromMetadata(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "failed to get email from metadata: %v", err)
	}

	clientId, err := s.GetClientIDByEmail(email)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to resolve client id: %v", err)
	}

	// 2. Fetch all account numbers associated with this client to ensure data isolation
	accNumbers, err := s.GetClientAccountNumbers(clientId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to fetch client accounts: %v", err)
	}

	// 3. Call repository layer to fetch combined results from payments and transfers
	rows, total, err := s.GetFilteredTransactionsRepo(accNumbers, req)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "database error: %v", err)
	}

	// 4. Map the UnifiedTransaction database DTOs to Protobuf messages
	var pbTransactions []*bankpb.Transaction
	for _, r := range rows {
		pbTransactions = append(pbTransactions, &bankpb.Transaction{
			Id:              r.ID,
			Type:            r.Type,
			FromAccount:     r.FromAccount,
			ToAccount:       r.ToAccount,
			InitialAmount:   r.InitialAmount,
			FinalAmount:     r.FinalAmount,
			Fee:             r.Fee,
			Currency:        r.Currency,
			PaymentCode:     r.PaymentCode,
			ReferenceNumber: r.ReferenceNumber,
			Purpose:         r.Purpose,
			Status:          r.Status,
			Timestamp:       r.Timestamp.Format(time.RFC3339),
			RecipientId:     r.RecipientID,
			StartCurrencyId: r.StartCurrencyID,
			ExchangeRate:    r.ExchangeRate,
		})
	}

	// Calculate total pages for pagination response
	var totalPages int32 = 0
	if req.PageSize > 0 {
		totalPages = int32(math.Ceil(float64(total) / float64(req.PageSize)))
	}

	return &bankpb.GetTransactionsResponse{
		Transactions: pbTransactions,
		Page:         req.Page,
		PageSize:     req.PageSize,
		Total:        total,
		TotalPages:   totalPages,
	}, nil
}

// GetTransactionById retrieves a single transaction and verifies that the authenticated
// client is either the sender or the receiver of the funds.
func (s *Server) GetTransactionById(ctx context.Context, req *bankpb.GetTransactionByIdRequest) (*bankpb.GetTransactionByIdResponse, error) {
	// 1. Authenticate using context metadata
	email, err := s.GetEmailFromMetadata(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "unauthenticated")
	}

	clientId, err := s.GetClientIDByEmail(email)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "error resolving client")
	}

	// 2. Fetch the specific transaction from the repository
	row, err := s.GetSingleTransactionRepo(req.Id, req.Type)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "transaction not found")
	}

	// 3. Security Check: Verify that the client owns one of the accounts involved in this transaction
	isOwner, err := s.CheckTransactionOwnership(clientId, row.FromAccount, row.ToAccount)
	if err != nil || !isOwner {
		return nil, status.Error(codes.PermissionDenied, "you do not have access to this transaction")
	}

	return &bankpb.GetTransactionByIdResponse{
		Transaction: &bankpb.Transaction{
			Id:              row.ID,
			Type:            row.Type,
			FromAccount:     row.FromAccount,
			ToAccount:       row.ToAccount,
			InitialAmount:   row.InitialAmount,
			FinalAmount:     row.FinalAmount,
			Fee:             row.Fee,
			Currency:        row.Currency,
			PaymentCode:     row.PaymentCode,
			ReferenceNumber: row.ReferenceNumber,
			Purpose:         row.Purpose,
			Status:          row.Status,
			Timestamp:       row.Timestamp.Format(time.RFC3339),
			RecipientId:     row.RecipientID,
			StartCurrencyId: row.StartCurrencyID,
			ExchangeRate:    row.ExchangeRate,
		},
	}, nil
}

// GenerateTransactionPdf creates a PDF receipt for a specific transaction.
// It reuses GetTransactionById logic to ensure the user has permission to access the data.
func (s *Server) GenerateTransactionPdf(ctx context.Context, req *bankpb.GenerateTransactionPdfRequest) (*bankpb.GenerateTransactionPdfResponse, error) {
	// Re-use existing retrieval logic (this handles authentication and ownership verification)
	txResp, err := s.GetTransactionById(ctx, &bankpb.GetTransactionByIdRequest{
		Id:   req.Id,
		Type: req.Type,
	})
	if err != nil {
		return nil, err
	}

	t := txResp.Transaction
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddPage()
	pdf.SetFont("Arial", "B", 16)
	pdf.Cell(190, 10, "Transaction Confirmation") // Translated header
	pdf.Ln(14)
	pdf.SetFont("Arial", "", 12)

	// Build PDF content lines
	content := []string{
		fmt.Sprintf("Transaction ID: %d", t.Id),
		fmt.Sprintf("From Account: %s", t.FromAccount),
		fmt.Sprintf("To Account: %s", t.ToAccount),
		fmt.Sprintf("Initial Amount: %d", t.InitialAmount),
		fmt.Sprintf("Currency: %s", t.Currency),
		fmt.Sprintf("Status: %s", t.Status),
		fmt.Sprintf("Timestamp: %s", t.Timestamp),
	}

	for _, line := range content {
		pdf.Cell(190, 8, line)
		pdf.Ln(8)
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, status.Error(codes.Internal, "PDF generation failed")
	}

	return &bankpb.GenerateTransactionPdfResponse{
		Pdf:      buf.Bytes(),
		FileName: fmt.Sprintf("transaction_%d.pdf", t.Id),
	}, nil
}

//=================================================================

func (s *Server) PayoutMoneyToOtherAccount(
	_ context.Context,
	req *bankpb.PaymentRequest,
) (*bankpb.PaymentResponse, error) {

	payment, currency, err := s.ProcessPayment(req.SenderAccount, req.RecipientAccount,
		req.Amount, req.PaymentCode, req.ReferenceNumber, req.Purpose)

	if err != nil {
		log.Printf("bank/server.go: payment failed: %v", err)
		switch {
		case errors.Is(err, ErrAccountNotFound):
			return nil, status.Error(codes.NotFound, "account not found")
		case errors.Is(err, ErrInsufficientFunds):
			return nil, status.Error(codes.FailedPrecondition, "insufficient funds")
		case strings.Contains(err.Error(), "exchange error"):
			return nil, status.Error(codes.Unavailable, "exchange service unavailable")
		default:
			return nil, status.Error(codes.Internal, "internal error")
		}
	}

	return &bankpb.PaymentResponse{
		FromAccount:     payment.From_account,
		ToAccount:       payment.To_account,
		InitialAmount:   payment.Start_amount,
		FinalAmount:     payment.End_amount,
		Fee:             payment.Commission,
		Currency:        strconv.FormatInt(currency.Id, 10),
		PaymentCode:     req.PaymentCode,
		ReferenceNumber: req.ReferenceNumber,
		Purpose:         req.Purpose,
		Status:          "realized",
		Timestamp:       time.Now().Format("2006-01-02 15:04:05"),
	}, nil
}

func (s *Server) TransferMoneyBetweenAccounts(
	_ context.Context,
	req *bankpb.TransferRequest,
) (*bankpb.TransferResponse, error) {

	if strings.TrimSpace(req.FromAccount) == "" || strings.TrimSpace(req.ToAccount) == "" {
		return nil, status.Error(codes.InvalidArgument, "account numbers are required")
	}

	if req.Amount <= 0 {
		return nil, status.Error(codes.InvalidArgument, "amount must be greater than zero")
	}

	transfer, err := s.CreateTransfer(req.FromAccount, req.ToAccount, req.Amount)
	if err != nil {
		log.Printf("bank/server.go: failed to create transfer: %v", err)
		switch {
		case strings.Contains(err.Error(), "same account"):
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case strings.Contains(err.Error(), "insufficient funds"):
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case strings.Contains(err.Error(), "exchange error"):
			return nil, status.Error(codes.Unavailable, "exchange service currently unavailable")
		default:
			return nil, status.Error(codes.Internal, "failed to create transfer")
		}
	}

	err = s.ConfirmTransfer(transfer.Transaction_id, "123456")
	if err != nil {
		log.Printf("bank/server.go: transfer confirmation failed: %v", err)
		switch {
		case strings.Contains(err.Error(), "insufficient funds"):
			return nil, status.Error(codes.FailedPrecondition, "insufficient funds")
		default:
			return nil, status.Error(codes.Internal, "transfer confirmation failed")
		}
	}

	res := &bankpb.TransferResponse{
		FromAccount:     transfer.From_account,
		ToAccount:       transfer.To_account,
		InitialAmount:   transfer.Start_amount,
		FinalAmount:     transfer.End_amount,
		Fee:             transfer.Commission,
		Currency:        strconv.FormatInt(transfer.Start_currency_id, 10),
		PaymentCode:     "",
		ReferenceNumber: "",
		Purpose:         req.Description,
		Status:          string(transfer.Status),
		Timestamp:       fmt.Sprintf("%d", time.Now().Unix()),
	}

	return res, nil
}
