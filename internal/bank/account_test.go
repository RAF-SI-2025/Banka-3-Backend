package bank

import (
	"context"
	"database/sql"
	"net"
	"os"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/bank"
	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/user"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type testUserServer struct {
	userpb.UnimplementedUserServiceServer
	isEmployee bool
	clientID   uint64
	clientMail string
}

func (s *testUserServer) GetClientById(_ context.Context, _ *userpb.GetUserByIdRequest) (*userpb.GetClientResponse, error) {
	date := time.Date(1990, 5, 20, 0, 0, 0, 0, time.UTC)
	return &userpb.GetClientResponse{
		Id:          1,
		FirstName:   "Petar",
		LastName:    "Petrovic",
		BirthDate:   date.Unix(),
		Gender:      "M",
		Email:       "petar@primer.raf",
		PhoneNumber: "+381645555555",
		Address:     "Njegoseva 25",
	}, nil
}

func (s *testUserServer) GetEmployeeByEmail(_ context.Context, _ *userpb.GetUserByEmailRequest) (*userpb.GetEmployeeResponse, error) {
	if s.isEmployee {
		return &userpb.GetEmployeeResponse{Id: 1, Email: "emp@banka.rs"}, nil
	}
	return nil, status.Error(codes.NotFound, "not employee")
}

func (s *testUserServer) GetClients(_ context.Context, _ *userpb.GetClientsRequest) (*userpb.GetClientsResponse, error) {
	if !s.isEmployee && s.clientMail != "" {
		return &userpb.GetClientsResponse{
			Clients: []*userpb.Client{{Id: int64(s.clientID), Email: s.clientMail}},
		}, nil
	}
	return &userpb.GetClientsResponse{Clients: []*userpb.Client{}}, nil
}

func setupMockGorm(t *testing.T, db *sql.DB) *gorm.DB {
	gormDB, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm open: %v", err)
	}
	return gormDB
}

func startUserMock(srv *testUserServer) (string, func()) {
	lis, _ := net.Listen("tcp", "127.0.0.1:0")
	s := grpc.NewServer()
	userpb.RegisterUserServiceServer(s, srv)
	go func() { _ = s.Serve(lis) }()
	return lis.Addr().String(), s.Stop
}

func TestListAccounts(t *testing.T) {
	sqlDB, mock, _ := sqlmock.New()
	gormDB := setupMockGorm(t, sqlDB)
	server := &Server{db_gorm: gormDB}

	userSrv := &testUserServer{isEmployee: true}
	addr, stop := startUserMock(userSrv)
	defer stop()
	_ = os.Setenv("USER_SERVICE_ADDR", addr)

	t.Run("EmployeeSuccess", func(t *testing.T) {
		ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("user-email", "emp@banka.rs"))
		mock.ExpectQuery(`SELECT (.+) FROM "accounts" JOIN clients ON clients.id = accounts.owner WHERE clients.first_name ILIKE \$1`).
			WithArgs("Danilo%").
			WillReturnRows(sqlmock.NewRows([]string{"number"}).AddRow("123"))

		resp, err := server.ListAccounts(ctx, &bankpb.ListAccountsRequest{FirstName: "Danilo"})
		if err != nil || len(resp.Accounts) != 1 {
			t.Fatalf("fail: %v", err)
		}
	})

	t.Run("ClientSuccess", func(t *testing.T) {
		userSrv.isEmployee = false
		userSrv.clientID = 55
		userSrv.clientMail = "danilo@mail.com"
		ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("user-email", "danilo@mail.com"))

		mock.ExpectQuery(`SELECT \* FROM "accounts" WHERE "accounts"."owner" = \$1 AND "accounts"."active" = \$2`).
			WithArgs(int64(55), true).
			WillReturnRows(sqlmock.NewRows([]string{"number"}).AddRow("555"))

		resp, err := server.ListAccounts(ctx, &bankpb.ListAccountsRequest{})
		if err != nil || resp.Accounts[0].AccountNumber != "555" {
			t.Fatalf("fail: %v", err)
		}
	})
}

func TestGetAccountDetails(t *testing.T) {
	sqlDB, mock, _ := sqlmock.New()
	gormDB := setupMockGorm(t, sqlDB)
	server := &Server{db_gorm: gormDB}

	userSrv := &testUserServer{isEmployee: false, clientID: 10, clientMail: "user@mail.com"}
	addr, stop := startUserMock(userSrv)
	defer stop()
	_ = os.Setenv("USER_SERVICE_ADDR", addr)

	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("user-email", "user@mail.com"))

	t.Run("SuccessOwner", func(t *testing.T) {
		mock.ExpectQuery(`SELECT \* FROM "accounts" WHERE "accounts"."number" = \$1`).
			WithArgs("111", 1).
			WillReturnRows(sqlmock.NewRows([]string{"number", "owner", "active", "created_at", "valid_until"}).
				AddRow("111", 10, true, time.Now(), time.Now()))
		resp, err := server.GetAccountDetails(ctx, &bankpb.GetAccountDetailsRequest{AccountNumber: "111"})
		if err != nil || resp.Account.AccountNumber != "111" {
			t.Fatalf("fail: %v", err)
		}
	})
}

func TestListClientTransactions(t *testing.T) {
	sqlDB, mock, _ := sqlmock.New()
	gormDB := setupMockGorm(t, sqlDB)
	server := &Server{db_gorm: gormDB}

	userSrv := &testUserServer{isEmployee: false, clientID: 10, clientMail: "client@mail.com"}
	addr, stop := startUserMock(userSrv)
	defer stop()
	_ = os.Setenv("USER_SERVICE_ADDR", addr)

	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("user-email", "client@mail.com"))

	t.Run("Success", func(t *testing.T) {
		mock.ExpectQuery(`SELECT \* FROM "accounts" WHERE "accounts"\."owner" = \$1 AND "accounts"\."active" = \$2`).
			WithArgs(int64(10), true).
			WillReturnRows(sqlmock.NewRows([]string{"number", "owner"}).AddRow("123", 10))

		mock.ExpectQuery(`SELECT \* FROM "payments" WHERE \(from_account IN \(\$1\) OR to_account IN \(\$2\)\)`).
			WillReturnRows(sqlmock.NewRows([]string{"transaction_id", "status", "timestamp"}).AddRow(1, "realized", time.Now()))

		mock.ExpectQuery(`SELECT \* FROM "transfers" WHERE \(from_account IN \(\$1\) OR to_account IN \(\$2\)\)`).
			WillReturnRows(sqlmock.NewRows([]string{"transaction_id", "timestamp"}).AddRow(2, time.Now()))

		resp, err := server.ListClientTransactions(ctx, &bankpb.ListClientTranasctionsRequest{AccountNumber: "123"})
		if err != nil {
			t.Fatalf("fail: %v", err)
		}
		if resp == nil || len(resp.Transactions) != 2 {
			t.Fatalf("expected 2 tx, got %v", resp)
		}
	})
}

func TestGetCompanyByOwnerID(t *testing.T) {
	sqlDB, mock, _ := sqlmock.New()
	gormDB := setupMockGorm(t, sqlDB)
	server := &Server{db_gorm: gormDB}

	t.Run("Success", func(t *testing.T) {
		mock.ExpectQuery(`SELECT \* FROM "companies" WHERE "companies"."owner_id" = \$1`).
			WithArgs(int64(1), 1).
			WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(1, "RAF"))
		res, err := server.GetCompanyByOwnerID(1)
		if err != nil || res.Name != "RAF" {
			t.Fail()
		}
	})
}
