package bank

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"errors"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/bank"
	exchangepb "github.com/RAF-SI-2025/Banka-3-Backend/gen/exchange"
	notificationpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/notification"
	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/user"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

type Server struct {
	bankpb.UnimplementedBankServiceServer
	database            *sql.DB
	db_gorm             *gorm.DB
	ExchangeService     exchangepb.ExchangeServiceClient
	NotificationService notificationpb.NotificationServiceClient
	UserService         userpb.UserServiceClient
}

func NewServer(database *sql.DB, gorm_db *gorm.DB) (*Server, error) {
	exchangeAddr := os.Getenv("EXCHANGE_GRPC_ADDR")
	if exchangeAddr == "" {
		exchangeAddr = "exchange:50051"
	}
	exchangeConn, err := grpc.NewClient(exchangeAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}

	notificationAddr := os.Getenv("NOTIFICATION_GRPC_ADDR")
	if notificationAddr == "" {
		notificationAddr = "notification:50051"
	}
	notificationConn, err := grpc.NewClient(notificationAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}

	userAddr := os.Getenv("USER_GRPC_ADDR")
	if userAddr == "" {
		userAddr = "user:50051"
	}
	userConn, err := grpc.NewClient(userAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}

	return &Server{
		database:            database,
		db_gorm:             gorm_db,
		ExchangeService:     exchangepb.NewExchangeServiceClient(exchangeConn),
		NotificationService: notificationpb.NewNotificationServiceClient(notificationConn),
		UserService:         userpb.NewUserServiceClient(userConn),
	}, nil
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

func mapCardsToProto(cards []Card) []*bankpb.CardResponse {
	pbCards := make([]*bankpb.CardResponse, 0, len(cards))
	for i := range cards {
		pbCards = append(pbCards, mapCardToProto(&cards[i]))
	}
	return pbCards
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

func (s *Server) GetCards(ctx context.Context, _ *bankpb.GetCardsRequest) (*bankpb.GetCardsResponse, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "metadata missing")
	}

	emails := md.Get("user-email")
	if len(emails) == 0 || strings.TrimSpace(emails[0]) == "" {
		return nil, status.Error(codes.Unauthenticated, "email missing in metadata")
	}
	userEmail := emails[0]

	isEmployee, err := s.IsEmployeeByEmail(userEmail)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to resolve caller")
	}

	var cards []Card

	if isEmployee {
		cards, err = s.GetCardsForEmployee()
		if err != nil {
			return nil, status.Error(codes.Internal, "failed to fetch cards")
		}

		return &bankpb.GetCardsResponse{
			Cards: mapCardsToProto(cards),
		}, nil
	}

	clientID, err := s.GetClientIDByEmail(userEmail)
	if err != nil {
		return nil, status.Error(codes.NotFound, "client not found")
	}

	cards, err = s.GetCardsByOwnerID(clientID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to fetch cards")
	}

	return &bankpb.GetCardsResponse{
		Cards: mapCardsToProto(cards),
	}, nil
}

func (s *Server) BlockCard(ctx context.Context, req *bankpb.BlockCardRequest) (*bankpb.BlockCardResponse, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "metadata missing")
	}

	emails := md.Get("user-email")
	if len(emails) == 0 || strings.TrimSpace(emails[0]) == "" {
		return nil, status.Error(codes.Unauthenticated, "email missing in metadata")
	}
	userEmail := emails[0]

	isEmployee, err := s.IsEmployeeByEmail(userEmail)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to resolve caller")
	}

	if req.CardNumber == "" {
		return nil, status.Error(codes.InvalidArgument, "card_number is required")
	}

	card, err := s.GetCardByNumberRecord(req.CardNumber)
	if err != nil {
		return &bankpb.BlockCardResponse{Success: false}, status.Error(codes.NotFound, "card not found")
	}

	currentStatus, err := s.GetCardStatus(card.Id)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to read card status")
	}

	isCurrentlyBlocked := currentStatus == Blocked

	// only employees can unblock
	if isCurrentlyBlocked && !isEmployee {
		return nil, status.Error(codes.PermissionDenied, "only employees can unblock cards")
	}

	var newStatus Card_status
	if isCurrentlyBlocked {
		newStatus = Active
	} else {
		newStatus = Blocked
	}

	err = s.UpdateCardStatus(card.Id, newStatus)
	if err != nil {
		return &bankpb.BlockCardResponse{Success: false}, status.Error(codes.Internal, "failed to update card status")
	}

	// Send email logic:

	// card ID -> account ID
	accountID, err := s.GetAccountIDByCardID(card.Id)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to resolve account")
	}

	// account ID -> owner email
	clientEmail, err := s.getClientEmailByAccountID(accountID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to resolve client email")
	}

	err = s.sendCardBlockedEmail(ctx, clientEmail, newStatus == Blocked)
	if err != nil {
		return nil, err
	}

	return &bankpb.BlockCardResponse{Success: true}, nil
}

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

func parseInterestRateType(value string) (interest_rate_type, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "fixed", "fiksna", "":
		return Fixed, nil
	case "variable", "varijabilna":
		return Variable, nil
	default:
		return "", status.Error(codes.InvalidArgument, "invalid interest_rate_type")
	}
}

func parseEmploymentStatus(value string) (employment_status, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "full_time":
		return Full_time, nil
	case "temporary":
		return Temporary, nil
	case "unemployed":
		return Unemployed, nil
	case "":
		return "", nil
	default:
		return "", status.Error(codes.InvalidArgument, "invalid employment_status")
	}
}

func parseLoanStatus(value string) (loan_status, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "approved":
		return Approved, nil
	case "rejected":
		return Rejected, nil
	case "paid":
		return Paid, nil
	case "late":
		return Late, nil
	default:
		return "", status.Error(codes.InvalidArgument, "invalid status")
	}
}

func loanViewToProto(loan *loanView) *bankpb.Loan {
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
	}
}

func (s *Server) GetLoans(_ context.Context, req *bankpb.GetLoansRequest) (*bankpb.GetLoansResponse, error) {
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
		parsed, err := parseLoanStatus(req.Status)
		if err != nil {
			return nil, err
		}
		loanStatus = string(parsed)
	}

	loans, err := s.getLoansForClient(
		clientEmail,
		loanType,
		strings.TrimSpace(req.AccountNumber),
		loanStatus,
	)
	if err != nil {
		log.Printf("[GetLoans] ERROR fetching loans for client %s: %v", clientEmail, err)
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to retrieve loans: %v", err))
	}

	responseLoans := make([]*bankpb.Loan, 0, len(loans))
	for i := range loans {
		responseLoans = append(responseLoans, loanViewToProto(&loans[i]))
	}

	return &bankpb.GetLoansResponse{
		Loans: responseLoans,
	}, nil
}

func (s *Server) GetLoanByNumber(_ context.Context, req *bankpb.GetLoanByNumberRequest) (*bankpb.Loan, error) {
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

	log.Printf("[GetLoanByNumber] Looking up loan %d for client email: %s", loanID, clientEmail)

	var loan *loanView
	var clientLookupErr error

	loan, clientLookupErr = s.getLoanByIDForClient(clientEmail, loanID)

	if clientLookupErr != nil {
		if errors.Is(clientLookupErr, gorm.ErrRecordNotFound) {
			// Check if this might be an employee (email not in clients table)
			// Try unrestricted lookup for employees
			log.Printf("[GetLoanByNumber] Client lookup failed for %s, trying employee lookup", clientEmail)
			loan, err = s.getLoanByID(loanID)
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					log.Printf("[GetLoanByNumber] Loan %d not found", loanID)
					return nil, status.Error(codes.NotFound, "loan not found")
				}
				log.Printf("[GetLoanByNumber] ERROR fetching loan %d: %v", loanID, err)
				return nil, status.Error(codes.Internal, fmt.Sprintf("failed to retrieve loan: %v", err))
			}
			log.Printf("[GetLoanByNumber] SUCCESS: Found loan %d for employee %s", loanID, clientEmail)
		} else {
			log.Printf("[GetLoanByNumber] ERROR fetching loan %d for client %s: %v", loanID, clientEmail, clientLookupErr)
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to retrieve loan: %v", clientLookupErr))
		}
	} else {
		log.Printf("[GetLoanByNumber] SUCCESS: Found loan %d for client %s", loanID, clientEmail)
	}

	return loanViewToProto(loan), nil
}

func (s *Server) CreateLoanRequest(_ context.Context, req *bankpb.CreateLoanRequestRequest) (*bankpb.CreateLoanRequestResponse, error) {
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

	interestRateType, err := parseInterestRateType(req.InterestRateType)
	if err != nil {
		return nil, err
	}

	empStatus, err := parseEmploymentStatus(req.EmploymentStatus)
	if err != nil {
		return nil, err
	}

	loanRequest := &LoanRequest{
		Type:               normalizedType,
		Currency_id:        currency.Id,
		Amount:             req.Amount,
		Repayment_period:   req.RepaymentPeriod,
		Account_id:         account.Id,
		Status:             LoanRequestPending,
		Submission_date:    time.Now(),
		Purpose:            strings.TrimSpace(req.Purpose),
		Salary:             req.Salary,
		Employment_status:  empStatus,
		Employment_period:  req.EmploymentPeriod,
		Phone_number:       strings.TrimSpace(req.PhoneNumber),
		Interest_rate_type: interestRateType,
	}

	if err := s.createLoanRequest(loanRequest); err != nil {
		return nil, status.Error(codes.Internal, "failed to create loan request")
	}

	return &bankpb.CreateLoanRequestResponse{}, nil
}

func (s *Server) GetTransfersHistoryForUserEmail(
	_ context.Context,
	req *bankpb.TransferHistoryRequest) (*bankpb.TransferHistoryResponse, error) {
	res, err := s.GetTransferHistory(req.Email, req.Page, req.PageSize)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to get transfer history")
		//return &bankpb.TransferHistoryResponse{History: nil}, err
	}
	return res, nil
}

func (s *Server) GetLoanRequests(_ context.Context, req *bankpb.GetLoanRequestsRequest) (*bankpb.GetLoanRequestsResponse, error) {
	loanType := ""
	if strings.TrimSpace(req.LoanType) != "" {
		parsed, err := parseLoanType(req.LoanType)
		if err != nil {
			return nil, err
		}
		loanType = string(parsed)
	}

	requests, err := s.getLoanRequests(loanType, strings.TrimSpace(req.AccountNumber))
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to retrieve loan requests")
	}

	result := make([]*bankpb.LoanRequestView, 0, len(requests))
	for _, r := range requests {
		result = append(result, &bankpb.LoanRequestView{
			Id:               r.Id,
			LoanType:         r.LoanType,
			Amount:           r.Amount,
			Currency:         r.Currency,
			Purpose:          r.Purpose,
			Salary:           r.Salary,
			EmploymentStatus: r.EmploymentStatus,
			EmploymentPeriod: r.EmploymentPeriod,
			PhoneNumber:      r.PhoneNumber,
			RepaymentPeriod:  r.RepaymentPeriod,
			AccountNumber:    r.AccountNumber,
			Status:           r.Status,
			InterestRateType: r.InterestRateType,
			SubmissionDate:   r.SubmissionDate,
		})
	}

	return &bankpb.GetLoanRequestsResponse{LoanRequests: result}, nil
}

func (s *Server) ApproveLoanRequest(_ context.Context, req *bankpb.ApproveLoanRequestRequest) (*bankpb.ApproveLoanRequestResponse, error) {
	if req.Id <= 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid loan request id")
	}

	loanReq, err := s.getLoanRequestByID(req.Id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "loan request not found")
		}
		return nil, status.Error(codes.Internal, "failed to retrieve loan request")
	}

	if loanReq.Status != LoanRequestPending {
		return nil, status.Error(codes.InvalidArgument, "loan request is not pending")
	}

	var account Account
	if err := s.db_gorm.First(&account, loanReq.Account_id).Error; err != nil {
		return nil, status.Error(codes.Internal, "failed to retrieve account")
	}

	// Fetch currency
	var currency Currency
	if err := s.db_gorm.First(&currency, loanReq.Currency_id).Error; err != nil {
		return nil, status.Error(codes.Internal, "failed to retrieve currency")
	}

	// Calculate interest rate
	rateToRSD, err := s.getExchangeRateToRSD(currency.Label)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to retrieve exchange rate")
	}
	amountRSD := int64(float64(loanReq.Amount) * rateToRSD)
	baseRate := BaseAnnualRate(amountRSD)
	margin := MarginForLoanType(loanReq.Type)
	annualRate := baseRate + margin

	now := time.Now()
	dateEnd := now.AddDate(0, int(loanReq.Repayment_period), 0)
	monthlyPayment := CalculateAnnuity(loanReq.Amount, annualRate, loanReq.Repayment_period)
	nextPaymentDue := now.AddDate(0, 1, 0)

	loan := &Loan{
		Account_id:         loanReq.Account_id,
		Amount:             loanReq.Amount,
		Currency_id:        loanReq.Currency_id,
		Installments:       loanReq.Repayment_period,
		Nominal_rate:       float32(annualRate),
		Interest_rate:      float32(annualRate),
		Date_signed:        now,
		Date_end:           dateEnd,
		Monthly_payment:    monthlyPayment,
		Next_payment_due:   nextPaymentDue,
		Remaining_debt:     loanReq.Amount,
		Type:               loanReq.Type,
		Loan_status:        Approved,
		Interest_rate_type: loanReq.Interest_rate_type,
	}

	installment := &LoanInstallment{
		Installment_amount: monthlyPayment,
		Interest_rate:      float32(annualRate),
		Currency_id:        loanReq.Currency_id,
		Due_date:           nextPaymentDue,
		Paid_date:          time.Time{},
		Status:             Due,
	}

	err = s.db_gorm.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(loan).Error; err != nil {
			return err
		}
		installment.Loan_id = loan.Id
		if err := tx.Create(installment).Error; err != nil {
			return err
		}
		if err := tx.Model(&LoanRequest{}).Where("id = ?", req.Id).Update("status", LoanRequestApproved).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to approve loan request")
	}

	return &bankpb.ApproveLoanRequestResponse{}, nil
}

func (s *Server) RejectLoanRequest(_ context.Context, req *bankpb.RejectLoanRequestRequest) (*bankpb.RejectLoanRequestResponse, error) {
	if req.Id <= 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid loan request id")
	}

	loanReq, err := s.getLoanRequestByID(req.Id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "loan request not found")
		}
		return nil, status.Error(codes.Internal, "failed to retrieve loan request")
	}

	if loanReq.Status != LoanRequestPending {
		return nil, status.Error(codes.InvalidArgument, "loan request is not pending")
	}

	if err := s.updateLoanRequestStatus(req.Id, LoanRequestRejected); err != nil {
		return nil, status.Error(codes.Internal, "failed to reject loan request")
	}

	return &bankpb.RejectLoanRequestResponse{}, nil
}

func (s *Server) GetAllLoans(_ context.Context, req *bankpb.GetAllLoansRequest) (*bankpb.GetLoansResponse, error) {
	loanType := ""
	if strings.TrimSpace(req.LoanType) != "" {
		parsed, err := parseLoanType(req.LoanType)
		if err != nil {
			return nil, err
		}
		loanType = string(parsed)
	}

	loanStatus := ""
	if strings.TrimSpace(req.Status) != "" {
		parsed, err := parseLoanStatus(req.Status)
		if err != nil {
			return nil, err
		}
		loanStatus = string(parsed)
	}

	loans, err := s.getAllLoans(
		loanType,
		strings.TrimSpace(req.AccountNumber),
		loanStatus,
	)
	if err != nil {
		log.Printf("[GetAllLoans] ERROR fetching all loans: %v", err)
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to retrieve loans: %v", err))
	}

	responseLoans := make([]*bankpb.Loan, 0, len(loans))
	for i := range loans {
		responseLoans = append(responseLoans, loanViewToProto(&loans[i]))
	}

	return &bankpb.GetLoansResponse{Loans: responseLoans}, nil
}
