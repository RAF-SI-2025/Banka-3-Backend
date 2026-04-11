package bank

import (
	"context"
	"os"
	"strings"

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

	empResp, err := userClient.GetEmployeeByEmail(ctx, &userpb.GetEmployeeByEmailRequest{
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
		ClientID: int64(clientResp.Clients[0].Id),
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
	statusStr := "Inactive"
	if a.Active {
		statusStr = "Active"
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
