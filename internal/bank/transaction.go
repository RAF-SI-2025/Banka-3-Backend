package bank

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/bank"
	"github.com/go-pdf/fpdf"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type transactionListRow struct {
	ID              int64
	Type            string
	FromAccount     string
	ToAccount       string
	StartAmount     float64
	EndAmount       float64
	Commission      float64
	Status          string
	Timestamp       time.Time
	RecipientID     int64
	TransactionCode string
	CallNumber      string
	Reason          string
	StartCurrencyID int64
	ExchangeRate    float64
}

func normalizeTransactionStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "all":
		return ""
	case "realized", "realizovano":
		return "realized"
	case "rejected", "odbijeno":
		return "rejected"
	case "pending", "u obradi":
		return "pending"
	default:
		return value
	}
}

func displayTransactionStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "realized":
		return "Realizovano"
	case "rejected":
		return "Odbijeno"
	case "pending":
		return "U obradi"
	default:
		return value
	}
}

func normalizeTransactionSortBy(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "id":
		return "tx.id"
	case "type":
		return "tx.type"
	case "from_account":
		return "tx.from_account"
	case "to_account":
		return "tx.to_account"
	case "start_amount", "amount":
		return "tx.start_amount"
	case "end_amount":
		return "tx.end_amount"
	case "commission":
		return "tx.commission"
	case "status":
		return "tx.status"
	case "timestamp", "":
		return "tx.timestamp"
	default:
		return "tx.timestamp"
	}
}

func normalizeTransactionSortOrder(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "asc":
		return "ASC"
	default:
		return "DESC"
	}
}

func normalizeTransactionType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "payment", "transfer":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func (s *Server) GetTransactionById(
	ctx context.Context,
	req *bankpb.GetTransactionByIdRequest,
) (*bankpb.GetTransactionByIdResponse, error) {
	if req.ClientId <= 0 {
		return nil, status.Error(codes.InvalidArgument, "client_id must be provided")
	}
	if req.Id <= 0 {
		return nil, status.Error(codes.InvalidArgument, "id must be provided")
	}
	transactionType := normalizeTransactionType(req.Type)
	if transactionType == "" {
		return nil, status.Error(codes.InvalidArgument, "type must be 'payment' or 'transfer'")
	}

	query := `
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
		WHERE a.owner = $1 AND p.transaction_id = $2
		LIMIT 1
	`
	if transactionType == "transfer" {
		query = `
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
			WHERE a.owner = $1 AND t.transaction_id = $2
			LIMIT 1
		`
	}

	var row transactionListRow

	err := s.database.QueryRowContext(ctx, query, req.ClientId, req.Id).Scan(
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
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, status.Error(codes.NotFound, "transaction not found")
		}
		return nil, err
	}

	return &bankpb.GetTransactionByIdResponse{
		Transaction: &bankpb.Transaction{
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
		},
	}, nil
}
func (s *Server) GenerateTransactionPdf(
	ctx context.Context,
	req *bankpb.GenerateTransactionPdfRequest,
) (*bankpb.GenerateTransactionPdfResponse, error) {
	if req.ClientId <= 0 {
		return nil, status.Error(codes.InvalidArgument, "client_id must be provided")
	}
	if req.Id <= 0 {
		return nil, status.Error(codes.InvalidArgument, "id must be provided")
	}
	transactionType := normalizeTransactionType(req.Type)
	if transactionType == "" {
		return nil, status.Error(codes.InvalidArgument, "type must be 'payment' or 'transfer'")
	}

	txResp, err := s.GetTransactionById(ctx, &bankpb.GetTransactionByIdRequest{
		ClientId: req.ClientId,
		Id:       req.Id,
		Type:     transactionType,
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

	lines := []string{
		fmt.Sprintf("ID transakcije: %d", t.Id),
		fmt.Sprintf("Tip transakcije: %s", t.Type),
		fmt.Sprintf("Sa racuna: %s", t.FromAccount),
		fmt.Sprintf("Na racun: %s", t.ToAccount),
		fmt.Sprintf("Pocetni iznos: %.2f", t.StartAmount),
		fmt.Sprintf("Krajnji iznos: %.2f", t.EndAmount),
		fmt.Sprintf("Provizija: %.2f", t.Commission),
		fmt.Sprintf("Status: %s", t.Status),
		fmt.Sprintf("Vreme: %s", time.Unix(t.Timestamp, 0).Format("2006-01-02 15:04:05")),
	}

	if t.Type == "payment" {
		lines = append(lines,
			fmt.Sprintf("Recipient ID: %d", t.RecipientId),
			fmt.Sprintf("Sifra placanja: %s", t.TransactionCode),
			fmt.Sprintf("Poziv na broj: %s", t.CallNumber),
			fmt.Sprintf("Svrha placanja: %s", t.Reason),
		)
	}

	if t.Type == "transfer" {
		lines = append(lines,
			fmt.Sprintf("Start currency ID: %d", t.StartCurrencyId),
			fmt.Sprintf("Kurs: %.4f", t.ExchangeRate),
		)
	}

	for _, line := range lines {
		pdf.Cell(190, 8, line)
		pdf.Ln(8)
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, status.Error(codes.Internal, "failed to generate pdf")
	}

	fileName := fmt.Sprintf("transaction_%d.pdf", t.Id)

	return &bankpb.GenerateTransactionPdfResponse{
		Pdf:      buf.Bytes(),
		FileName: fileName,
	}, nil
}
