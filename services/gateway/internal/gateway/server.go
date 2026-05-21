package gateway

import (
	"net/http"
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/pkg/proto/bank"
	exchangepb "github.com/RAF-SI-2025/Banka-3-Backend/pkg/proto/exchange"
	notificationpb "github.com/RAF-SI-2025/Banka-3-Backend/pkg/proto/notification"
	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/pkg/proto/trading"
	userpb "github.com/RAF-SI-2025/Banka-3-Backend/pkg/proto/user"
)

func dialOpts() []grpc.DialOption {
	return []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(logger.UnaryClientInterceptor()),
		grpc.WithStreamInterceptor(logger.StreamClientInterceptor()),
	}
}

type Server struct {
	UserClient         userpb.UserServiceClient
	TOTPClient         userpb.TOTPServiceClient
	NotificationClient notificationpb.NotificationServiceClient
	BankClient         bankpb.BankServiceClient
	ExchangeClient     exchangepb.ExchangeServiceClient
	TradingClient      tradingpb.TradingServiceClient
	HTTPClient         *http.Client
	InterbankRoutes    map[string]string
	InterbankAPIKey    string
	BankInternalHTTPURL string
	BankInternalAPIKey  string
	RoutingNumber      string
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
		exchangeAddr = "exchange:50051"
	}

	tradingAddr := os.Getenv("TRADING_GRPC_ADDR")
	if tradingAddr == "" {
		tradingAddr = "bank:50051"
	}
	interbankRoutes := parseInterbankRoutes(os.Getenv("INTERBANK_ROUTES"))
	bankInternalHTTPURL := os.Getenv("BANK_INTERNAL_HTTP_URL")
	if bankInternalHTTPURL == "" {
		bankInternalHTTPURL = "http://bank:50090"
	}
	bankInternalAPIKey := os.Getenv("BANK_INTERNAL_API_KEY")
	if bankInternalAPIKey == "" {
		bankInternalAPIKey = "dev-internal-banka3"
	}

	userConn, err := grpc.NewClient(userAddr, dialOpts()...)
	if err != nil {
		return nil, err
	}

	notificationConn, err := grpc.NewClient(notificationAddr, dialOpts()...)
	if err != nil {
		_ = userConn.Close()
		return nil, err
	}

	bankConn, err := grpc.NewClient(bankAddr, dialOpts()...)
	if err != nil {
		_ = userConn.Close()
		_ = notificationConn.Close()
		return nil, err
	}

	exchangeConn, err := grpc.NewClient(exchangeAddr, dialOpts()...)
	if err != nil {
		_ = userConn.Close()
		_ = notificationConn.Close()
		_ = bankConn.Close()
		return nil, err
	}

	tradingConn, err := grpc.NewClient(tradingAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		_ = userConn.Close()
		_ = notificationConn.Close()
		_ = bankConn.Close()
		_ = exchangeConn.Close()
		return nil, err
	}

	return &Server{
		UserClient:         userpb.NewUserServiceClient(userConn),
		TOTPClient:         userpb.NewTOTPServiceClient(userConn),
		NotificationClient: notificationpb.NewNotificationServiceClient(notificationConn),
		BankClient:         bankpb.NewBankServiceClient(bankConn),
		ExchangeClient:     exchangepb.NewExchangeServiceClient(exchangeConn),
		TradingClient:      tradingpb.NewTradingServiceClient(tradingConn),
		HTTPClient:         newInterbankHTTPClient(),
		InterbankRoutes:    interbankRoutes,
		InterbankAPIKey:    interbankOutboundAPIKey(),
		BankInternalHTTPURL: bankInternalHTTPURL,
		BankInternalAPIKey:  bankInternalAPIKey,
		RoutingNumber:      ownRoutingNumber(),
	}, nil
}
