package gateway

import (
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/bank"
	exchangepb "github.com/RAF-SI-2025/Banka-3-Backend/gen/exchange"
	notificationpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/notification"
	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/user"
)

type Server struct {
	UserClient         userpb.UserServiceClient
	TOTPClient         userpb.TOTPServiceClient
	NotificationClient notificationpb.NotificationServiceClient
	BankClient         bankpb.BankServiceClient
	ExchangeClinet     exchangepb.ExchangeServiceClient
}

func NewServer() (*Server, error) {
	userAddr := os.Getenv("USER_GRPC_ADDR")
	if userAddr == "" {
		userAddr = "user:50051"
	}

	notificationAddr := os.Getenv("NOTIFICATION_GRPC_ADDR")
	if notificationAddr == "" {
		notificationAddr = "notification:50051"
	}

	bankAddr := os.Getenv("BANK_GRPC_ADDR")
	if bankAddr == "" {
		bankAddr = "bank:50051"
	}

	exchangeAddr := os.Getenv("EXCHANGE_GRPC_ADDR")
	if exchangeAddr == "" {
		exchangeAddr = "exhcange:50051"
	}

	userConn, err := grpc.NewClient(userAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}

	notificationConn, err := grpc.NewClient(notificationAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		_ = userConn.Close()
		return nil, err
	}

	bankConn, err := grpc.NewClient(bankAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		_ = userConn.Close()
		_ = notificationConn.Close()
		return nil, err
	}

	exchangeConn, err := grpc.NewClient(exchangeAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		_ = userConn.Close()
		_ = notificationConn.Close()
		_ = bankConn.Close()
		return nil, err
	}

	return &Server{
		UserClient:         userpb.NewUserServiceClient(userConn),
		TOTPClient:         userpb.NewTOTPServiceClient(userConn),
		NotificationClient: notificationpb.NewNotificationServiceClient(notificationConn),
		BankClient:         bankpb.NewBankServiceClient(bankConn),
		ExchangeClinet:     exchangepb.NewExchangeServiceClient(exchangeConn),
	}, nil
}
