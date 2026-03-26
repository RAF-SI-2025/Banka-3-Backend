package bank

import (
	"context"
	"os"

	exchangepb "github.com/RAF-SI-2025/Banka-3-Backend/gen/exchange"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	defaultExchangeURL = "exchange:50051"
)

func (s *Server) callConvertMoney(ctx context.Context, from, to string, amount float64) (*exchangepb.ConversionResponse, error) {
	addr := os.Getenv("EXCHANGE_GRPC_ADDR")
	if addr == "" {
		addr = defaultExchangeURL
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	defer func(conn *grpc.ClientConn) {
		_ = conn.Close()
	}(conn)

	client := exchangepb.NewExchangeServiceClient(conn)
	return client.ConvertMoney(ctx, &exchangepb.ConversionRequest{
		FromCurrency: from,
		ToCurrency:   to,
		Amount:       amount,
	})
}
