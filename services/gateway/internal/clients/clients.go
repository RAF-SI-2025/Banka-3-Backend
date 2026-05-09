// Package clients dials the upstream gRPC services. The gateway's HTTP
// handlers consume these.
package clients

import (
	"fmt"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/bank/v1"
	exchangepb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/exchange/v1"
	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/trading/v1"
	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/user/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Set bundles every upstream client. Address-less services stay nil
// so a single-celina dev stack doesn't need every backend running.
type Set struct {
	User     userpb.UserServiceClient
	Bank     bankpb.BankServiceClient
	Exchange exchangepb.ExchangeServiceClient
	Trading  tradingpb.TradingServiceClient

	UserConn     *grpc.ClientConn
	BankConn     *grpc.ClientConn
	ExchangeConn *grpc.ClientConn
	TradingConn  *grpc.ClientConn

	conns []*grpc.ClientConn
}

// Dial connects to every upstream service that has a non-empty
// address. Caller defers Close().
func Dial(addrs Addrs) (*Set, error) {
	s := &Set{}

	userConn, err := dial(addrs.User)
	if err != nil {
		return nil, fmt.Errorf("dial user: %w", err)
	}
	s.UserConn = userConn
	s.User = userpb.NewUserServiceClient(userConn)
	s.conns = append(s.conns, userConn)

	if addrs.Bank != "" {
		c, err := dial(addrs.Bank)
		if err != nil {
			return nil, fmt.Errorf("dial bank: %w", err)
		}
		s.BankConn = c
		s.Bank = bankpb.NewBankServiceClient(c)
		s.conns = append(s.conns, c)
	}

	if addrs.Exchange != "" {
		c, err := dial(addrs.Exchange)
		if err != nil {
			return nil, fmt.Errorf("dial exchange: %w", err)
		}
		s.ExchangeConn = c
		s.Exchange = exchangepb.NewExchangeServiceClient(c)
		s.conns = append(s.conns, c)
	}

	if addrs.Trading != "" {
		c, err := dial(addrs.Trading)
		if err != nil {
			return nil, fmt.Errorf("dial trading: %w", err)
		}
		s.TradingConn = c
		s.Trading = tradingpb.NewTradingServiceClient(c)
		s.conns = append(s.conns, c)
	}

	return s, nil
}

// Close releases every dialed connection.
func (s *Set) Close() {
	for _, c := range s.conns {
		_ = c.Close()
	}
}

// Addrs holds the gRPC dial targets for each upstream service.
type Addrs struct {
	User         string
	Bank         string
	Trading      string
	Exchange     string
	Notification string
}

func dial(target string) (*grpc.ClientConn, error) {
	if target == "" {
		return nil, fmt.Errorf("empty target")
	}
	return grpc.NewClient(target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
}
