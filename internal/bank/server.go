package bank

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"errors"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/bank"
	"github.com/go-pdf/fpdf"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

type Server struct {
	bankpb.UnimplementedBankServiceServer
	database *sql.DB
	db_gorm  *gorm.DB
}

func NewServer(database *sql.DB, gorm_db *gorm.DB) *Server {
	return &Server{
		database: database,
		db_gorm:  gorm_db,
	}
}

func mapCompanyToProto(company *Company) *bankpb.Company {
	if company == nil {
		return nil
	}

	return &bankpb.Company{
		Id:             company.Id,
		RegisteredId:   company.Registered_id,
		Name:           company.Name,
		TaxCode:        company.Tax_code,
		ActivityCodeId: company.Activity_code_id,
		Address:        company.Address,
		OwnerId:        company.Owner_id,
	}
}

func validateCreateCompanyInput(registeredID int64, name string, taxCode int64, address string, ownerID int64) error {
	if registeredID <= 0 {
		return status.Error(codes.InvalidArgument, "registered id must be greater than zero")
	}
	if strings.TrimSpace(name) == "" {
		return status.Error(codes.InvalidArgument, "name is required")
	}
	if taxCode <= 0 {
		return status.Error(codes.InvalidArgument, "tax code must be greater than zero")
	}
	if strings.TrimSpace(address) == "" {
		return status.Error(codes.InvalidArgument, "address is required")
	}
	if ownerID <= 0 {
		return status.Error(codes.InvalidArgument, "owner id must be greater than zero")
	}
	return nil
}

func validateUpdateCompanyInput(id int64, name string, address string, ownerID int64) error {
	if id <= 0 {
		return status.Error(codes.InvalidArgument, "id must be greater than zero")
	}
	if strings.TrimSpace(name) == "" {
		return status.Error(codes.InvalidArgument, "name is required")
	}
	if strings.TrimSpace(address) == "" {
		return status.Error(codes.InvalidArgument, "address is required")
	}
	if ownerID <= 0 {
		return status.Error(codes.InvalidArgument, "owner id must be greater than zero")
	}
	return nil
}

func (s *Server) CreateCompany(_ context.Context, req *bankpb.CreateCompanyRequest) (*bankpb.CreateCompanyResponse, error) {
	if err := validateCreateCompanyInput(req.RegisteredId, req.Name, req.TaxCode, req.Address, req.OwnerId); err != nil {
		return nil, err
	}

	company, err := s.CreateCompanyRecord(Company{
		Registered_id:    req.RegisteredId,
		Name:             strings.TrimSpace(req.Name),
		Tax_code:         req.TaxCode,
		Activity_code_id: req.ActivityCodeId,
		Address:          strings.TrimSpace(req.Address),
		Owner_id:         req.OwnerId,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrCompanyRegisteredIDExists):
			return nil, status.Error(codes.AlreadyExists, "company with that registered id already exists")
		case errors.Is(err, ErrCompanyOwnerNotFound):
			return nil, status.Error(codes.InvalidArgument, "owner does not exist")
		case errors.Is(err, ErrCompanyActivityCodeNotFound):
			return nil, status.Error(codes.InvalidArgument, "activity code does not exist")
		default:
			return nil, status.Error(codes.Internal, "company creation failed")
		}
	}

	return &bankpb.CreateCompanyResponse{Company: mapCompanyToProto(company)}, nil
}

func (s *Server) GetCompanyById(_ context.Context, req *bankpb.GetCompanyByIdRequest) (*bankpb.GetCompanyByIdResponse, error) {
	if req.Id <= 0 {
		return nil, status.Error(codes.InvalidArgument, "id must be greater than zero")
	}

	company, err := s.GetCompanyByIDRecord(req.Id)
	if err != nil {
		switch {
		case errors.Is(err, ErrCompanyNotFound):
			return nil, status.Error(codes.NotFound, "company not found")
		default:
			return nil, status.Error(codes.Internal, "company lookup failed")
		}
	}

	return &bankpb.GetCompanyByIdResponse{Company: mapCompanyToProto(company)}, nil
}

func (s *Server) GetCompanies(_ context.Context, _ *bankpb.GetCompaniesRequest) (*bankpb.GetCompaniesResponse, error) {
	companies, err := s.GetCompaniesRecords()
	if err != nil {
		return nil, status.Error(codes.Internal, "company listing failed")
	}

	var responseCompanies []*bankpb.Company
	for _, company := range companies {
		responseCompanies = append(responseCompanies, mapCompanyToProto(company))
	}

	return &bankpb.GetCompaniesResponse{Companies: responseCompanies}, nil
}

func (s *Server) UpdateCompany(_ context.Context, req *bankpb.UpdateCompanyRequest) (*bankpb.UpdateCompanyResponse, error) {
	if err := validateUpdateCompanyInput(req.Id, req.Name, req.Address, req.OwnerId); err != nil {
		return nil, err
	}

	company, err := s.UpdateCompanyRecord(Company{
		Id:               req.Id,
		Name:             strings.TrimSpace(req.Name),
		Activity_code_id: req.ActivityCodeId,
		Address:          strings.TrimSpace(req.Address),
		Owner_id:         req.OwnerId,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrCompanyNotFound):
			return nil, status.Error(codes.NotFound, "company not found")
		case errors.Is(err, ErrCompanyOwnerNotFound):
			return nil, status.Error(codes.InvalidArgument, "owner does not exist")
		case errors.Is(err, ErrCompanyActivityCodeNotFound):
			return nil, status.Error(codes.InvalidArgument, "activity code does not exist")
		default:
			return nil, status.Error(codes.Internal, "company update failed")
		}
	}

	return &bankpb.UpdateCompanyResponse{Company: mapCompanyToProto(company)}, nil
}

func mapCardToProto(card *Card) *bankpb.CardResponse {
	if card == nil {
		return nil
	}
	return &bankpb.CardResponse{
		CardId:         fmt.Sprintf("%d", card.Id),
		CardNumber:     card.Number,
		CardType:       string(card.Type),
		CardBrand:      string(card.Brand),
		CreationDate:   card.Creation_date.Format(time.RFC3339),
		ExpirationDate: card.Valid_until.Format(time.RFC3339),
		AccountNumber:  card.Account_number,
		Cvv:            card.Cvv,
		Limit:          card.Card_limit,
		Status:         string(card.Status),
	}
}

func (s *Server) checkCardLimit(userEmail string, accountNumber string) error {
	isAuth, _ := s.IsAuthorizedParty(userEmail, accountNumber)
	limit := 2
	if isAuth {
		limit = 1
	}

	count, err := s.CountActiveCardsByAccountNumber(accountNumber)
	if err != nil {
		return status.Error(codes.Internal, "failed to check limits")
	}

	if count >= limit {
		return status.Error(codes.FailedPrecondition, "card limit reached for this user type")
	}
	return nil
}

func (s *Server) CreateCard(_ context.Context, req *bankpb.CreateCardRequest) (*bankpb.CardResponse, error) {
	_, err := s.GetAccountByNumberRecord(req.AccountNumber)
	if err != nil {
		return nil, status.Error(codes.NotFound, "account not found")
	}

	if err := s.checkCardLimit(req.Email, req.AccountNumber); err != nil {
		return nil, err
	}

	brand := card_brand(strings.ToLower(req.CardBrand))
	number, err := GenerateCardNumber(brand, req.AccountNumber)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	card, err := s.CreateCardRecord(Card{
		Number:         number,
		Type:           card_type(strings.ToLower(req.CardType)),
		Brand:          brand,
		Valid_until:    time.Now().AddDate(5, 0, 0),
		Account_number: req.AccountNumber,
		Cvv:            GenerateCVV(),
		Status:         Active,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to create card")
	}

	return mapCardToProto(card), nil
}

func (s *Server) RequestCard(ctx context.Context, req *bankpb.RequestCardRequest) (*bankpb.RequestCardResponse, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "metadata missing")
	}

	emails := md.Get("user-email")
	if len(emails) == 0 {
		return nil, status.Error(codes.Unauthenticated, "email missing in metadata")
	}
	userEmail := emails[0]

	acc, err := s.GetAccountByNumberRecord(req.AccountNumber)
	if err != nil {
		return nil, status.Error(codes.NotFound, "account not found")
	}

	err = s.checkCardLimit(emails[0], req.AccountNumber)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	token := fmt.Sprintf("tkn-%d-%d", time.Now().UnixNano(), acc.Id)
	cardReq := CardRequest{
		Account_number: req.AccountNumber,
		Type:           card_type(strings.ToLower(req.CardType)),
		Brand:          card_brand(strings.ToLower(req.CardBrand)),
		Token:          token,
		ExpirationDate: time.Now().Add(24 * time.Hour),
		Complete:       false,
		Email:          userEmail,
	}

	_, err = s.CreateCardRequestRecord(cardReq)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to create request")
	}

	baseUrl := "http://localhost:8080/api/cards/confirm/?token="
	url := baseUrl + token

	err = s.sendCardConfirmationEmail(ctx, userEmail, url)
	if err != nil {
		return nil, err
	}

	return &bankpb.RequestCardResponse{Accepted: true}, nil
}

func (s *Server) ConfirmCard(ctx context.Context, req *bankpb.ConfirmCardRequest) (*bankpb.ConfirmCardResponse, error) {
	request, err := s.GetCardRequestByToken(req.Token)
	if err != nil {
		return nil, status.Error(codes.NotFound, "invalid or expired token")
	}

	if time.Now().After(request.ExpirationDate) {
		return nil, status.Error(codes.DeadlineExceeded, "token expired")
	}

	cardNumber, _ := GenerateCardNumber(request.Brand, request.Account_number)
	_, err = s.CreateCardRecord(Card{
		Number:         cardNumber,
		Type:           request.Type,
		Brand:          request.Brand,
		Valid_until:    time.Now().AddDate(5, 0, 0),
		Account_number: request.Account_number,
		Cvv:            GenerateCVV(),
		Status:         Active,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to create card from request")
	}

	err = s.MarkCardRequestFulfilled(request.Id)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to close request")
	}

	err = s.sendCardCreatedEmail(ctx, request.Email)
	if err != nil {
		return nil, err
	}

	return &bankpb.ConfirmCardResponse{}, nil
}

func (s *Server) GetCards(_ context.Context, _ *bankpb.GetCardsRequest) (*bankpb.GetCardsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented yet")
}

func (s *Server) BlockCard(_ context.Context, req *bankpb.BlockCardRequest) (*bankpb.BlockCardResponse, error) {
	if req.CardId <= 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid card id")
	}

	err := s.BlockCardRecord(req.CardId)
	if err != nil {
		return &bankpb.BlockCardResponse{Success: false}, status.Error(codes.NotFound, "card not found")
	}

	return &bankpb.BlockCardResponse{Success: true}, nil
}

type paymentRecipientRow struct {
	ID            int64
	Name          string
	AccountNumber string
}
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

func validateCreateAccountInput(name string, owner int64, currency string, ownerType string, accountType string, maintainanceCost int64, dailyLimit int64, monthlyLimit int64, createdBy int64, validUntil int64) error {
	if strings.TrimSpace(name) == "" {
		return status.Error(codes.InvalidArgument, "name is required")
	}
	if owner <= 0 {
		return status.Error(codes.InvalidArgument, "owner must be greater than zero")
	}
	if createdBy <= 0 {
		return status.Error(codes.InvalidArgument, "created_by must be greater than zero")
	}
	if strings.TrimSpace(currency) == "" {
		return status.Error(codes.InvalidArgument, "currency is required")
	}
	if ownerType != string(Personal) && ownerType != string(Business) {
		return status.Error(codes.InvalidArgument, "owner_type must be one of personal or business")
	}
	if accountType != string(Checking) && accountType != string(Foreign) {
		return status.Error(codes.InvalidArgument, "account_type must be one of checking or foreign")
	}
	if maintainanceCost < 0 {
		return status.Error(codes.InvalidArgument, "maintainance_cost must be greater than or equal to zero")
	}
	if dailyLimit < 0 {
		return status.Error(codes.InvalidArgument, "daily_limit must be greater than or equal to zero")
	}
	if monthlyLimit < 0 {
		return status.Error(codes.InvalidArgument, "monthly_limit must be greater than or equal to zero")
	}
	if validUntil != 0 && !time.Unix(validUntil, 0).After(time.Now()) {
		return status.Error(codes.InvalidArgument, "valid_until must be in the future")
	}
	return nil
}

func (s *Server) CreateAccount(_ context.Context, req *bankpb.CreateAccountRequest) (*bankpb.CreateAccountResponse, error) {
	name := strings.TrimSpace(req.Name)
	currency := strings.TrimSpace(req.Currency)
	ownerType := strings.TrimSpace(strings.ToLower(req.OwnerType))
	accountType := strings.TrimSpace(strings.ToLower(req.AccountType))

	if err := validateCreateAccountInput(
		name,
		req.Owner,
		currency,
		ownerType,
		accountType,
		req.MaintainanceCost,
		req.DailyLimit,
		req.MonthlyLimit,
		req.CreatedBy,
		req.ValidUntil,
	); err != nil {
		return nil, err
	}

	account := Account{
		Name:              name,
		Owner:             req.Owner,
		Currency:          currency,
		Owner_type:        owner_type(ownerType),
		Account_type:      account_type(accountType),
		Maintainance_cost: req.MaintainanceCost,
		Daily_limit:       req.DailyLimit,
		Monthly_limit:     req.MonthlyLimit,
		Created_by:        req.CreatedBy,
	}
	if req.ValidUntil != 0 {
		account.Valid_until = time.Unix(req.ValidUntil, 0)
	}

	created, err := s.CreateAccountRecord(account)
	if err != nil {
		switch {
		case errors.Is(err, ErrAccountOwnerNotFound):
			return nil, status.Error(codes.InvalidArgument, "owner does not exist")
		case errors.Is(err, ErrAccountCreatorNotFound):
			return nil, status.Error(codes.InvalidArgument, "creator does not exist")
		case errors.Is(err, ErrAccountCurrencyNotFound):
			return nil, status.Error(codes.InvalidArgument, "currency does not exist")
		case errors.Is(err, ErrAccountNumberGenerationFailed):
			return nil, status.Error(codes.Internal, "account number generation failed")
		default:
			return nil, status.Error(codes.Internal, "account creation failed")
		}
	}

	return &bankpb.CreateAccountResponse{
		Valid:         true,
		AccountNumber: created.Number,
		Error:         "",
	}, nil
}

func parseLoanType(value string) (loan_type, error) {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "GOTOVINSKI":
		return Cash, nil
	case "STAMBENI":
		return Mortgage, nil
	case "AUTO":
		return Car, nil
	case "REFINANSIRAJUCI":
		return Refinancing, nil
	case "STUDENTSKI":
		return Student, nil
	default:
		return "", status.Error(codes.InvalidArgument, "invalid loan_type")
	}
}

func (s *Server) GetLoans(ctx context.Context, req *bankpb.GetLoansRequest) (*bankpb.GetLoansResponse, error) {
	clientEmail := strings.TrimSpace(req.ClientEmail)
	if clientEmail == "" {
		return nil, status.Error(codes.Unauthenticated, "client email required")
	}

	loanType := ""
	if strings.TrimSpace(req.LoanType) != "" {
		parsedLoanType, err := parseLoanType(req.LoanType)
		if err != nil {
			return nil, err
		}
		loanType = string(parsedLoanType)
	}

	loanStatus := ""
	if strings.TrimSpace(req.Status) != "" {
		switch strings.ToLower(strings.TrimSpace(req.Status)) {
		case "approved":
			loanStatus = string(Approved)
		case "rejected":
			loanStatus = string(Rejected)
		case "paid":
			loanStatus = string(Paid)
		case "late":
			loanStatus = string(Late)
		default:
			return nil, status.Error(codes.InvalidArgument, "invalid status")
		}
	}

	loans, err := s.getLoansForClient(
		clientEmail,
		loanType,
		strings.TrimSpace(req.AccountNumber),
		loanStatus,
	)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to retrieve loans")
	}

	responseLoans := make([]*bankpb.Loan, 0, len(loans))
	for _, loan := range loans {
		responseLoans = append(responseLoans, &bankpb.Loan{
			LoanNumber:            loan.LoanNumber,
			LoanType:              loan.LoanType,
			AccountNumber:         loan.AccountNumber,
			LoanAmount:            loan.LoanAmount,
			RepaymentPeriod:       loan.RepaymentPeriod,
			NominalRate:           loan.NominalRate,
			EffectiveRate:         loan.EffectiveRate,
			AgreementDate:         loan.AgreementDate,
			MaturityDate:          loan.MaturityDate,
			NextInstallmentAmount: loan.NextInstallmentAmount,
			NextInstallmentDate:   loan.NextInstallmentDate,
			RemainingDebt:         loan.RemainingDebt,
			Currency:              loan.Currency,
			Status:                loan.Status,
		})
	}

	return &bankpb.GetLoansResponse{
		Loans: responseLoans,
	}, nil
}

func (s *Server) GetLoanByNumber(ctx context.Context, req *bankpb.GetLoanByNumberRequest) (*bankpb.Loan, error) {
	clientEmail := strings.TrimSpace(req.ClientEmail)
	if clientEmail == "" {
		return nil, status.Error(codes.Unauthenticated, "client email required")
	}

	loanNumber := strings.TrimSpace(req.LoanNumber)
	if loanNumber == "" {
		return nil, status.Error(codes.InvalidArgument, "loan number required")
	}

	loanID, err := strconv.ParseInt(loanNumber, 10, 64)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid loan number")
	}

	loan, err := s.getLoanByIDForClient(clientEmail, loanID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "loan not found")
		}
		return nil, status.Error(codes.Internal, "failed to retrieve loan")
	}

	return &bankpb.Loan{
		LoanNumber:            loan.LoanNumber,
		LoanType:              loan.LoanType,
		AccountNumber:         loan.AccountNumber,
		LoanAmount:            loan.LoanAmount,
		RepaymentPeriod:       loan.RepaymentPeriod,
		NominalRate:           loan.NominalRate,
		EffectiveRate:         loan.EffectiveRate,
		AgreementDate:         loan.AgreementDate,
		MaturityDate:          loan.MaturityDate,
		NextInstallmentAmount: loan.NextInstallmentAmount,
		NextInstallmentDate:   loan.NextInstallmentDate,
		RemainingDebt:         loan.RemainingDebt,
		Currency:              loan.Currency,
		Status:                loan.Status,
	}, nil
}

func (s *Server) CreateLoanRequest(ctx context.Context, req *bankpb.CreateLoanRequestRequest) (*bankpb.CreateLoanRequestResponse, error) {
	clientEmail := strings.TrimSpace(req.ClientEmail)
	if clientEmail == "" {
		return nil, status.Error(codes.Unauthenticated, "client email required")
	}

	accountNumber := strings.TrimSpace(req.AccountNumber)
	currencyLabel := strings.TrimSpace(req.Currency)

	if accountNumber == "" {
		return nil, status.Error(codes.InvalidArgument, "account_number is required")
	}
	if currencyLabel == "" {
		return nil, status.Error(codes.InvalidArgument, "currency is required")
	}
	if req.Amount <= 0 {
		return nil, status.Error(codes.InvalidArgument, "amount must be positive")
	}
	if req.RepaymentPeriod <= 0 {
		return nil, status.Error(codes.InvalidArgument, "repayment_period must be positive")
	}

	normalizedType, err := parseLoanType(req.LoanType)
	if err != nil {
		return nil, err
	}

	account, err := s.getOwnedAccountByNumber(clientEmail, accountNumber)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "account not found")
		}
		return nil, status.Error(codes.Internal, "failed to retrieve account")
	}

	if !strings.EqualFold(account.Currency, currencyLabel) {
		return nil, status.Error(codes.InvalidArgument, "account currency and request currency must match")
	}

	currency, err := s.getCurrencyByLabel(currencyLabel)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.InvalidArgument, "unsupported currency")
		}
		return nil, status.Error(codes.Internal, "failed to retrieve currency")
	}

	loanRequest := &LoanRequest{
		Type:             normalizedType,
		Currency_id:      currency.Id,
		Amount:           req.Amount,
		Repayment_period: int64(req.RepaymentPeriod),
		Account_id:       account.Id,
		Status:           LoanRequestPending,
		Submission_date:  time.Now(),
	}

	if err := s.createLoanRequest(loanRequest); err != nil {
		return nil, status.Error(codes.Internal, "failed to create loan request")
	}

	return &bankpb.CreateLoanRequestResponse{}, nil
}

func (s *Server) PayoutMoneyToOtherAccount(
	ctx context.Context,
	req *bankpb.PaymentRequest,
) (*bankpb.PaymentResponse, error) {

	_, err := s.ProcessPayment(req.SenderAccount, req.RecipientAccount,
		req.Amount, req.Amount, 0, req.PaymentCode,
		req.ReferenceNumber, req.Purpose)
	if err != nil {
		switch {
		case errors.Is(err, ErrAccountNotFound):
			return nil, status.Error(codes.NotFound, "account not found")
		case errors.Is(err, ErrInsufficientFunds):
			return nil, status.Error(codes.FailedPrecondition, "insufficient funds")
		case errors.Is(err, ErrLimitExceeded):
			return nil, status.Error(codes.FailedPrecondition, "limit exceeded")
		default:
			return nil, status.Error(codes.Internal, "internal error")
		}
	}

	return &bankpb.PaymentResponse{
		FromAccount:     req.SenderAccount,
		ToAccount:       req.RecipientAccount,
		InitialAmount:   req.Amount,
		FinalAmount:     req.Amount,
		Fee:             0,
		Currency:        "EUR",
		PaymentCode:     req.PaymentCode,
		ReferenceNumber: req.ReferenceNumber,
		Purpose:         req.Purpose,
		Status:          "realized",
		Timestamp:       time.Now().Format("2006-01-02 15:04:05"), //standard layout of Go, don't change this date, it's layout, not hardcode
	}, nil
}
func (s *Server) TransferMoneyBetweenAccounts(
	ctx context.Context,
	req *bankpb.TransferRequest,
) (*bankpb.TransferResponse, error) {

	if strings.TrimSpace(req.FromAccount) == "" || strings.TrimSpace(req.ToAccount) == "" {
		return nil, status.Error(codes.InvalidArgument, "account numbers are required")
	}

	if req.Amount <= 0 {
		return nil, status.Error(codes.InvalidArgument, "amount must be greater than zero")
	}

	transfer, err := s.CreateTransfer( req.FromAccount, req.ToAccount, int64(req.Amount))
	if err != nil {
		switch {
		case strings.Contains(err.Error(), "same account"):
			{
				return nil, status.Error(codes.InvalidArgument, err.Error())
			}
		case strings.Contains(err.Error(), "currency mismatch"):
			{
				return nil, status.Error(codes.InvalidArgument, err.Error())
			}
		case strings.Contains(err.Error(), "insufficient funds"):
			{
				return nil, status.Error(codes.InvalidArgument, err.Error())
			}
		default:
			{
				return nil, status.Error(codes.Internal, "failed to create transfer")
			}
		}
	}

	err = s.ConfirmTransfer(transfer.Transaction_id, "123456")
	if err != nil {
		switch {
		case strings.Contains(err.Error(), "insufficient funds"):
			{
				return nil, status.Error(codes.FailedPrecondition, "insufficient funds")
			}
		default:
			{
				return nil, status.Error(codes.Internal, "transfer confirmation failed")
			}

		}
	}
	res, err := &bankpb.TransferResponse{
		FromAccount:     transfer.From_account,
		ToAccount:       transfer.To_account,
		InitialAmount:   transfer.Start_amount,
		FinalAmount:     transfer.End_amount,
		Fee:             transfer.Commission,
		Currency:        strconv.FormatInt(transfer.Start_currency_id, 10),
		PaymentCode:     "",
		ReferenceNumber: "",
		Purpose:         "",
		Status:          "realized",
		Timestamp:       fmt.Sprintf("%d", transfer.Timestamp.Unix()),
	}, nil
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to transfer money")
	}
	return res, nil
}

func (s *Server) GetTransfersHistoryForUserEmail(
	ctx context.Context,
	req *bankpb.TransferHistoryRequest) (*bankpb.TransferHistoryResponse, error) {
	res, err := s.GetTransferHistory(req.Email, req.Page, req.PageSize)
	if err != nil {
		status.Error(codes.Internal, "failed to get transfer history")
		return &bankpb.TransferHistoryResponse{History: nil}, err
	}
	return res, nil
}
