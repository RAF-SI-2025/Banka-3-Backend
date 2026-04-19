package trading

import (
	"context"

	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/trading"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

type Server struct {
	tradingpb.UnimplementedTradingServiceServer
	db *gorm.DB
}

func NewServer(db *gorm.DB) *Server {
	return &Server{db: db}
}

func (s *Server) ListExchanges(_ context.Context, _ *tradingpb.ListExchangesRequest) (*tradingpb.ListExchangesResponse, error) {
	rows, err := s.ListExchangesRecord()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}

	out := make([]*tradingpb.Exchange, 0, len(rows))
	for _, r := range rows {
		out = append(out, &tradingpb.Exchange{
			Id:             r.ID,
			Name:           r.Name,
			Acronym:        r.Acronym,
			MicCode:        r.MICCode,
			Polity:         r.Polity,
			Currency:       r.Currency,
			TimeZoneOffset: r.TimeZoneOffset,
			OpenOverride:   r.OpenOverride,
		})
	}
	return &tradingpb.ListExchangesResponse{Exchanges: out}, nil
}

func (s *Server) ListListings(_ context.Context, _ *tradingpb.ListListingsRequest) (*tradingpb.ListListingsResponse, error) {
	rows, err := s.ListListingsRecord()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}

	out := make([]*tradingpb.Listing, 0, len(rows))
	for _, r := range rows {
		var stockID, futureID int64
		if r.StockID != nil {
			stockID = *r.StockID
		}
		if r.FutureID != nil {
			futureID = *r.FutureID
		}
		out = append(out, &tradingpb.Listing{
			Id:              r.ID,
			ExchangeId:      r.ExchangeID,
			StockId:         stockID,
			FutureId:        futureID,
			Price:           r.Price,
			AskPrice:        r.AskPrice,
			BidPrice:        r.BidPrice,
			LastRefreshUnix: r.LastRefresh.Unix(),
		})
	}
	return &tradingpb.ListListingsResponse{Listings: out}, nil
}
