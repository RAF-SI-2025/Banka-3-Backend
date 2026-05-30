package server

import (
	"context"
	"time"

	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/trading/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// parseHistoryDate turns an optional YYYY-MM-DD string into a time.Time.
// Empty → zero time (the store treats a zero bound as unbounded). The
// FE sends bare dates from its range picker; a google.protobuf.Timestamp
// proto field can't be bound from that through grpc-gateway.
func parseHistoryDate(v string) (time.Time, error) {
	if v == "" {
		return time.Time{}, nil
	}
	return time.Parse("2006-01-02", v)
}

func (s *Server) UpsertListing(ctx context.Context, in *tradingpb.UpsertListingRequest) (*tradingpb.Listing, error) {
	l, err := s.Svc.UpsertListing(ctx, &domain.Listing{
		SecurityID:   in.GetSecurityId(),
		ExchangeMIC:  in.GetExchangeMic(),
		Price:        in.GetPrice(),
		Ask:          in.GetAsk(),
		Bid:          in.GetBid(),
		Volume:       in.GetVolume(),
		ChangeAmt:    in.GetChangeAmt(),
		ContractSize: in.GetContractSize(),
	})
	if err != nil {
		return nil, err
	}
	return listingToProto(l), nil
}

func (s *Server) GetListing(ctx context.Context, in *tradingpb.GetListingRequest) (*tradingpb.Listing, error) {
	l, err := s.Svc.GetListing(ctx, in.GetId())
	if err != nil {
		return nil, err
	}
	return listingToProto(l), nil
}

func (s *Server) ListListings(ctx context.Context, in *tradingpb.ListListingsRequest) (*tradingpb.ListListingsResponse, error) {
	rows, total, err := s.Svc.ListListings(ctx, store.ListingFilter{
		Type:        securityTypeFromProto(in.GetType()),
		ExchangeMIC: in.GetExchangeMic(),
		Search:      in.GetSearch(),
		SortBy:      in.GetSortBy(),
		SortDesc:    in.GetSortDesc(),
	}, int(in.GetPage()), int(in.GetPageSize()))
	if err != nil {
		return nil, err
	}
	out := &tradingpb.ListListingsResponse{Items: make([]*tradingpb.SecurityWithListing, 0, len(rows))}
	for _, r := range rows {
		out.Items = append(out.Items, securityWithListingToProto(r))
	}
	page := int(in.GetPage())
	if page < 1 {
		page = 1
	}
	pageSize := int(in.GetPageSize())
	if pageSize < 1 {
		pageSize = 50
	}
	out.Page = int32(page)
	out.PageSize = int32(pageSize)
	out.Total = total
	return out, nil
}

func (s *Server) GetListingDailyHistory(ctx context.Context, in *tradingpb.GetListingDailyHistoryRequest) (*tradingpb.GetListingDailyHistoryResponse, error) {
	from, err := parseHistoryDate(in.GetFrom())
	if err != nil {
		return nil, apperr.Validation("from: očekivan format YYYY-MM-DD")
	}
	to, err := parseHistoryDate(in.GetTo())
	if err != nil {
		return nil, apperr.Validation("to: očekivan format YYYY-MM-DD")
	}
	if !to.IsZero() {
		// Inclusive upper bound: cover the whole "to" day.
		to = to.Add(24*time.Hour - time.Second)
	}
	rows, err := s.Svc.GetListingDailyHistory(ctx, in.GetListingId(), from, to)
	if err != nil {
		return nil, err
	}
	out := &tradingpb.GetListingDailyHistoryResponse{Rows: make([]*tradingpb.ListingDailyPrice, 0, len(rows))}
	for _, r := range rows {
		out.Rows = append(out.Rows, &tradingpb.ListingDailyPrice{
			Date:      timestamppb.New(r.Date),
			Price:     r.Price,
			Ask:       r.Ask,
			Bid:       r.Bid,
			ChangeAmt: r.ChangeAmt,
			Volume:    r.Volume,
		})
	}
	return out, nil
}

func listingToProto(l *domain.Listing) *tradingpb.Listing {
	if l == nil {
		return nil
	}
	return &tradingpb.Listing{
		Id:           l.ID,
		SecurityId:   l.SecurityID,
		ExchangeMic:  l.ExchangeMIC,
		Price:        l.Price,
		Ask:          l.Ask,
		Bid:          l.Bid,
		Volume:       l.Volume,
		ChangeAmt:    l.ChangeAmt,
		ContractSize: l.ContractSize,
		LastRefresh:  timestamppb.New(l.LastRefresh),
		CreatedAt:    timestamppb.New(l.CreatedAt),
	}
}
