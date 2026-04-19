package bank

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/bank"
	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/user"
)

type callerIdentity struct {
	Email      string
	ClientID   int64
	IsClient   bool
	IsEmployee bool
}

// CallerIdentity is the exported form of callerIdentity, exposed for other
// services running in the bank process (e.g. trading) that reuse the same
// metadata-based auth scheme.
type CallerIdentity = callerIdentity

// ResolveCaller is exported for in-process callers that share bank's auth
// scheme. See authorizeAccountAccess for the ownership-check counterpart.
func (s *Server) ResolveCaller(ctx context.Context) (*CallerIdentity, error) {
	return s.resolveCaller(ctx)
}

// AuthorizeAccountAccess is exported for in-process callers.
func (s *Server) AuthorizeAccountAccess(ctx context.Context, acc *Account) error {
	return s.authorizeAccountAccess(ctx, acc)
}

func (s *Server) ListAccounts(ctx context.Context, req *bankpb.ListAccountsRequest) (*bankpb.ListAccountsResponse, error) {
	caller, err := s.resolveCaller(ctx)
	if err != nil {
		return nil, err
	}

	if caller.IsEmployee {
		accounts, err := s.GetAccountsForEmployee(req.FirstName, req.LastName, req.AccountNumber)
		if err != nil {
			return nil, status.Error(codes.Internal, "failed to fetch accounts")
		}

		return &bankpb.ListAccountsResponse{
			Accounts: s.mapSliceToProto(accounts),
		}, nil
	}

	if caller.IsClient {
		accounts, err := s.GetActiveAccountsByOwnerID(caller.ClientID)
		if err != nil {
			return nil, status.Error(codes.Internal, "failed to fetch client accounts")
		}

		return &bankpb.ListAccountsResponse{
			Accounts: s.mapSliceToProto(accounts),
		}, nil
	}

	return nil, status.Error(codes.PermissionDenied, "access denied")
}

func (s *Server) GetAccountDetails(ctx context.Context, req *bankpb.GetAccountDetailsRequest) (*bankpb.GetAccountDetailsResponse, error) {
	acc, err := s.GetAccountByNumber(req.AccountNumber)
	if err != nil {
		return nil, status.Error(codes.NotFound, "account not found")
	}

	if err := s.authorizeAccountAccess(ctx, acc); err != nil {
		return nil, err
	}

	pbAccount := s.mapToAccountProto(*acc)
	return &bankpb.GetAccountDetailsResponse{Account: pbAccount}, nil
}

func (s *Server) ListClientTransactions(ctx context.Context, req *bankpb.ListClientTranasctionsRequest) (*bankpb.ListClientTransactionsResponse, error) {
	caller, err := s.resolveCaller(ctx)
	if err != nil {
		return nil, err
	}

	if !caller.IsClient {
		return nil, status.Error(codes.PermissionDenied, "access denied")
	}

	accounts, err := s.GetActiveAccountsByOwnerID(caller.ClientID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to fetch client accounts")
	}

	var accNumbers []string
	for _, a := range accounts {
		accNumbers = append(accNumbers, a.Number)
	}

	if len(accNumbers) == 0 {
		return &bankpb.ListClientTransactionsResponse{}, nil
	}

	transactions, err := s.GetFilteredTransactions(accNumbers, req.AccountNumber, req.Date, req.Amount, req.Status)
	// fmt.Printf("GetFilteredTransactions(%v, %v, %v, %v, %v) = %v, %v\n", accNumbers, req.AccountNumber, req.Date, req.Amount, req.Status, transactions, err)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to fetch transactions")
	}

	return &bankpb.ListClientTransactionsResponse{Transactions: transactions}, nil
}

func (s *Server) UpdateAccountName(ctx context.Context, req *bankpb.UpdateAccountNameRequest) (*bankpb.UpdateAccountNameResponse, error) {
	accountNumber := strings.TrimSpace(req.AccountNumber)
	name := strings.TrimSpace(req.Name)

	if accountNumber == "" {
		return nil, status.Error(codes.InvalidArgument, "account number is required")
	}
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}

	account, err := s.GetAccountByNumber(accountNumber)
	if err != nil {
		return nil, status.Error(codes.NotFound, "account not found")
	}

	if err := s.authorizeAccountAccess(ctx, account); err != nil {
		return nil, err
	}

	exists, err := s.AccountNameExistsForOwner(account.Owner, name, accountNumber)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to check account name")
	}
	if exists {
		return nil, status.Error(codes.InvalidArgument, "name is already used by another account belonging to the customer")
	}

	if err := s.UpdateAccountNameRecord(accountNumber, name); err != nil {
		if err.Error() == "account not found" {
			return nil, status.Error(codes.NotFound, "account not found")
		}
		return nil, status.Error(codes.Internal, "failed to update account name")
	}

	return &bankpb.UpdateAccountNameResponse{}, nil
}

func (s *Server) UpdateAccountLimits(ctx context.Context, req *bankpb.UpdateAccountLimitsRequest) (*bankpb.UpdateAccountLimitsResponse, error) {
	accountNumber := strings.TrimSpace(req.AccountNumber)
	if accountNumber == "" {
		return nil, status.Error(codes.InvalidArgument, "account number is required")
	}

	if req.DailyLimit == nil && req.MonthlyLimit == nil {
		return nil, status.Error(codes.InvalidArgument, "at least one limit must be provided")
	}

	account, err := s.GetAccountByNumber(accountNumber)
	if err != nil {
		return nil, status.Error(codes.NotFound, "account not found")
	}

	if err := s.authorizeAccountAccess(ctx, account); err != nil {
		return nil, err
	}

	if req.DailyLimit != nil && *req.DailyLimit < 0 {
		return nil, status.Error(codes.InvalidArgument, "daily_limit must be non-negative")
	}

	if req.MonthlyLimit != nil && *req.MonthlyLimit < 0 {
		return nil, status.Error(codes.InvalidArgument, "monthly_limit must be non-negative")
	}

	if err := s.UpdateAccountLimitsRecord(accountNumber, req.DailyLimit, req.MonthlyLimit); err != nil {
		if err.Error() == "account not found" {
			return nil, status.Error(codes.NotFound, "account not found")
		}
		return nil, status.Error(codes.Internal, "failed to update account limits")
	}

	return &bankpb.UpdateAccountLimitsResponse{}, nil
}

func (s *Server) authorizeAccountAccess(ctx context.Context, acc *Account) error {
	caller, err := s.resolveCaller(ctx)
	if err != nil {
		return err
	}

	if caller.IsEmployee {
		return nil
	}

	if caller.IsClient && acc.Owner == caller.ClientID {
		return nil
	}

	return status.Error(codes.PermissionDenied, "access denied")
}

func (s *Server) resolveCaller(ctx context.Context) (*callerIdentity, error) {
	email, err := s.getEmailFromMetadata(ctx)
	if err != nil {
		return nil, err
	}

	userClient, conn, err := s.getUserServiceClient()
	if err != nil {
		return nil, status.Error(codes.Internal, "user service connection failed")
	}
	defer func(conn *grpc.ClientConn) {
		_ = conn.Close()
	}(conn)

	empResp, err := userClient.GetEmployeeByEmail(ctx, &userpb.GetUserByEmailRequest{
		Email: email,
	})
	if err == nil && empResp != nil {
		return &callerIdentity{
			Email:      email,
			IsEmployee: true,
		}, nil
	}

	clientResp, err := userClient.GetClients(ctx, &userpb.GetClientsRequest{
		Email: email,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to query user service")
	}
	if len(clientResp.Clients) == 0 {
		return nil, status.Error(codes.NotFound, "client not found")
	}

	return &callerIdentity{
		Email:    email,
		ClientID: clientResp.Clients[0].Id,
		IsClient: true,
	}, nil
}

func (s *Server) getEmailFromMetadata(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", status.Error(codes.Unauthenticated, "metadata missing")
	}

	emails := md.Get("user-email")
	if len(emails) == 0 || strings.TrimSpace(emails[0]) == "" {
		return "", status.Error(codes.Unauthenticated, "user-email missing")
	}

	return emails[0], nil
}

func (s *Server) getUserServiceClient() (userpb.UserServiceClient, *grpc.ClientConn, error) {
	addr := os.Getenv("USER_SERVICE_ADDR")
	if addr == "" {
		addr = "user:50051"
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	return userpb.NewUserServiceClient(conn), conn, err
}

func (s *Server) mapSliceToProto(accounts []Account) []*bankpb.Account {
	pbAccounts := make([]*bankpb.Account, 0, len(accounts))
	for _, a := range accounts {
		pbAccounts = append(pbAccounts, s.mapToAccountProto(a))
	}
	return pbAccounts
}

func (s *Server) mapToAccountProto(a Account) *bankpb.Account {
	statusStr := "Neaktivan"
	if a.Active {
		statusStr = "Aktivan"
	}

	return &bankpb.Account{
		AccountNumber:    a.Number,
		AccountName:      a.Name,
		OwnerId:          a.Owner,
		Balance:          float64(a.Balance),
		AvailableBalance: float64(a.Balance),
		EmployeeId:       a.Created_by,
		CreationDate:     a.Created_at.Unix(),
		ExpirationDate:   a.Valid_until.Unix(),
		Currency:         a.Currency,
		Status:           statusStr,
		AccountType:      string(a.Account_type),
		DailyLimit:       float64(a.Daily_limit),
		MonthlyLimit:     float64(a.Monthly_limit),
		DailySpending:    float64(a.Daily_expenditure),
		MonthlySpending:  float64(a.Monthly_expenditure),
	}
}

func (s *Server) CreateAccount(ctx context.Context, req *bankpb.CreateAccountRequest) (*bankpb.CreateAccountResponse, error) {
	if err := validateCreateAccountInput(req); err != nil {
		return nil, err
	}

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "metadata missing")
	}

	// 1. Get Employee ID from context
	idVals := md.Get("employee-id")
	if len(idVals) == 0 {
		return nil, status.Error(codes.Unauthenticated, "employee-id missing in context")
	}
	employeeID, err := strconv.ParseInt(idVals[0], 10, 64)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid employee-id format")
	}

	// 2. Get Email from context (needed for CreateCard)
	emailVals := md.Get("user-email")
	if len(emailVals) == 0 {
		return nil, status.Error(codes.Unauthenticated, "user-email missing in context")
	}
	email := emailVals[0]

	ownerType := Personal
	subtypeLower := strings.ToLower(req.Subtype)
	if req.AccountType == "business" || req.AccountType == "poslovni" || strings.Contains(subtypeLower, "business") || strings.Contains(subtypeLower, "poslovni") {
		ownerType = Business
	}

	// Use provided name, or generate a default
	accountName := strings.TrimSpace(req.Name)
	if accountName == "" {
		accountName = fmt.Sprintf("%s-%s", req.AccountType, req.Subtype)
	}

	account := Account{
		Name:              accountName,
		Owner:             req.ClientId,
		CompanyID:         nil,
		Currency:          req.Currency,
		Owner_type:        ownerType,
		Account_type:      account_type(strings.ToLower(req.AccountType)),
		Balance:           int64(req.InitialBalance * 100),
		Daily_limit:       int64(req.DailyLimit),
		Monthly_limit:     int64(req.MonthlyLimit),
		Created_by:        employeeID,
		Maintainance_cost: 0,
	}

	// Logika za kompaniju - Izvršava se samo ako postoje podaci o firmi
	if req.BusinessInfo != nil {
		pib, err := strconv.ParseInt(req.BusinessInfo.Pib, 10, 64)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid PIB: %v", err)
		}

		// Provera da li firma već postoji preko PIB-a
		existing, _ := s.GetCompanyByTaxCode(pib)

		// Ako postoji, dedelimo ID i preskačemo kreiranje
		if existing != nil {
			account.CompanyID = &existing.Id
		} else {
			// Ako NE postoji, pokušavamo da je kreiramo
			regID, err := strconv.ParseInt(req.BusinessInfo.RegistrationNumber, 10, 64)
			if err != nil {
				return nil, status.Errorf(codes.InvalidArgument, "invalid registration number: %v", err)
			}

			activityCode, err := strconv.ParseInt(req.BusinessInfo.ActivityCode, 10, 64)
			if err != nil {
				return nil, status.Errorf(codes.InvalidArgument, "invalid activity code: %v", err)
			}

			company := Company{
				Registered_id:    regID,
				Name:             req.BusinessInfo.CompanyName,
				Tax_code:         pib,
				Activity_code_id: activityCode,
				Address:          req.BusinessInfo.Address,
				Owner_id:         req.ClientId,
			}

			createdCompany, err := s.CreateCompanyRecord(company)
			if err != nil {
				return nil, handleCompanyError(err)
			}
			account.CompanyID = &createdCompany.Id
		}
	}

	created, err := s.CreateAccountRecord(account)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to save account record")
	}

	if req.CreateCard {
		if req.CardType == "" {
			req.CardType = "DEBIT"
		}
		if req.CardBrand == "" {
			req.CardBrand = "VISA"
		}
		s.triggerCardCreation(ctx, email, created.Number, req)
	}

	return mapToCreateAccountResponse(created), nil
}

func handleCompanyError(err error) error {
	switch {
	case errors.Is(err, ErrCompanyRegisteredIDExists):
		return status.Errorf(codes.AlreadyExists, "company registration id already exists")
	case errors.Is(err, ErrCompanyOwnerNotFound):
		return status.Error(codes.InvalidArgument, "owner does not exist")
	case errors.Is(err, ErrCompanyActivityCodeNotFound):
		return status.Error(codes.InvalidArgument, "activity code does not exist")
	default:
		return status.Error(codes.Internal, "company creation failed")
	}
}

func (s *Server) triggerCardCreation(ctx context.Context, email, accNum string, req *bankpb.CreateAccountRequest) {
	_, err := s.CreateCard(ctx, &bankpb.CreateCardRequest{
		Email:         email,
		AccountNumber: accNum,
		CardType:      req.CardType,
		CardBrand:     req.CardBrand,
	})
	if err != nil {
		fmt.Printf("warning: card creation failed for %s: %v\n", accNum, err)
	}
}

func mapToCreateAccountResponse(created *Account) *bankpb.CreateAccountResponse {
	statusStr := "Neaktivan"
	if created.Active {
		statusStr = "Aktivan"
	}

	return &bankpb.CreateAccountResponse{
		AccountNumber:    created.Number,
		AccountName:      created.Name,
		OwnerId:          created.Owner,
		Balance:          float64(created.Balance) / 100,
		AvailableBalance: float64(created.Balance) / 100,
		EmployeeId:       created.Created_by,
		CreationDate:     created.Created_at.Format(time.RFC3339),
		ExpirationDate:   created.Valid_until.Format(time.RFC3339),
		Currency:         created.Currency,
		Status:           statusStr,
		AccountType:      string(created.Account_type),
		DailyLimit:       float64(created.Daily_limit),
		MonthlyLimit:     float64(created.Monthly_limit),
		DailySpending:    float64(created.Daily_expenditure),
		MonthlySpending:  float64(created.Monthly_expenditure),
	}
}

func validateCreateAccountInput(req *bankpb.CreateAccountRequest) error {
	if req.ClientId <= 0 {
		return status.Error(codes.InvalidArgument, "client_id must be positive")
	}
	if strings.TrimSpace(req.Currency) == "" {
		return status.Error(codes.InvalidArgument, "currency is required")
	}

	accType := strings.ToLower(req.AccountType)
	if accType != "checking" && accType != "foreign" && accType != "tekuci" && accType != "devizni" && accType != "business" && accType != "poslovni" {
		return status.Error(codes.InvalidArgument, "invalid account_type: must be checking, foreign, or business")
	}

	if req.InitialBalance < 0 {
		return status.Error(codes.InvalidArgument, "initial_balance cannot be negative")
	}

	isBusiness := accType == "business" || accType == "poslovni"
	if isBusiness && req.BusinessInfo == nil {
		return status.Error(codes.InvalidArgument, "business_info is required for business accounts")
	}

	if isBusiness && req.BusinessInfo != nil {
		if strings.TrimSpace(req.BusinessInfo.CompanyName) == "" {
			return status.Error(codes.InvalidArgument, "business_info.company_name is required")
		}
		if strings.TrimSpace(req.BusinessInfo.Pib) == "" {
			return status.Error(codes.InvalidArgument, "business_info.pib is required")
		}
		if strings.TrimSpace(req.BusinessInfo.RegistrationNumber) == "" {
			return status.Error(codes.InvalidArgument, "business_info.registration_number is required")
		}
	}

	return nil
}
