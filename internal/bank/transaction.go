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

func (s *Server) GetTransactions(ctx context.Context, req *bankpb.GetTransactionsRequest) (*bankpb.GetTransactionsResponse, error) {
	// 1. Dobijanje email-a i klijenta iz konteksta
	email, err := s.GetEmailFromMetadata(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "failed to get email from metadata: %v", err)
	}

	clientId, err := s.GetClientIDByEmail(email)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to resolve client id: %v", err)
	}

	// 2. Provera vlasništva nad računima
	accNumbers, err := s.GetClientAccountNumbers(clientId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to fetch client accounts: %v", err)
	}

	// 3. Poziv Repo sloja
	rows, total, err := s.GetFilteredTransactionsRepo(accNumbers, req)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "database error: %v", err)
	}

	// 4. Mapiranje rezultata na Proto
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

	return &bankpb.GetTransactionsResponse{
		Transactions: pbTransactions,
		Page:         req.Page,
		PageSize:     req.PageSize,
		Total:        total,
		TotalPages:   int32(math.Ceil(float64(total) / float64(req.PageSize))),
	}, nil
}

func (s *Server) GetTransactionById(ctx context.Context, req *bankpb.GetTransactionByIdRequest) (*bankpb.GetTransactionByIdResponse, error) {
	// 1. Autorizacija preko konteksta
	email, err := s.GetEmailFromMetadata(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "unauthenticated")
	}

	clientId, err := s.GetClientIDByEmail(email)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "error resolving client")
	}

	// 2. Dobavljanje transakcije
	row, err := s.GetSingleTransactionRepo(req.Id, req.Type)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "transaction not found")
	}

	// 3. Sigurnosna provera: Da li klijent poseduje jedan od računa u ovoj transakciji?
	isOwner, err := s.CheckTransactionOwnership(clientId, row.FromAccount, row.ToAccount)
	if err != nil || !isOwner {
		return nil, status.Error(codes.PermissionDenied, "you do not have access to this transaction")
	}

	return &bankpb.GetTransactionByIdResponse{
		Transaction: &bankpb.Transaction{
			Id:              row.ID,
			Type:            req.Type,
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

func (s *Server) GenerateTransactionPdf(ctx context.Context, req *bankpb.GenerateTransactionPdfRequest) (*bankpb.GenerateTransactionPdfResponse, error) {
	// Koristimo postojeću metodu da dobijemo podatke
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
	pdf.Cell(190, 10, "Potvrda o transakciji")
	pdf.Ln(14)
	pdf.SetFont("Arial", "", 12)

	// Generisanje linija teksta
	content := []string{
		fmt.Sprintf("ID: %d", t.Id),
		fmt.Sprintf("Sa računa: %s", t.FromAccount),
		fmt.Sprintf("Na račun: %s", t.ToAccount),
		fmt.Sprintf("Iznos: %d", t.InitialAmount),
		fmt.Sprintf("Status: %s", t.Status),
		fmt.Sprintf("Datum: %s", t.Timestamp),
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
		FileName: fmt.Sprintf("transakcija_%d.pdf", t.Id),
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
