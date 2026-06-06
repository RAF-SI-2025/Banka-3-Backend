package server

import (
	"context"

	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/trading/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *Server) CreateWatchlist(ctx context.Context, in *tradingpb.CreateWatchlistRequest) (*tradingpb.Watchlist, error) {
	w, err := s.Svc.CreateWatchlist(ctx, in.GetName())
	if err != nil {
		return nil, err
	}
	return watchlistToProto(w), nil
}

func (s *Server) ListWatchlists(ctx context.Context, _ *tradingpb.ListWatchlistsRequest) (*tradingpb.ListWatchlistsResponse, error) {
	lists, err := s.Svc.ListWatchlists(ctx)
	if err != nil {
		return nil, err
	}
	out := &tradingpb.ListWatchlistsResponse{Watchlists: make([]*tradingpb.Watchlist, 0, len(lists))}
	for _, w := range lists {
		out.Watchlists = append(out.Watchlists, watchlistToProto(w))
	}
	return out, nil
}

func (s *Server) DeleteWatchlist(ctx context.Context, in *tradingpb.DeleteWatchlistRequest) (*tradingpb.DeleteWatchlistResponse, error) {
	if err := s.Svc.DeleteWatchlist(ctx, in.GetId()); err != nil {
		return nil, err
	}
	return &tradingpb.DeleteWatchlistResponse{}, nil
}

func (s *Server) AddToWatchlist(ctx context.Context, in *tradingpb.AddToWatchlistRequest) (*tradingpb.WatchlistItem, error) {
	it, err := s.Svc.AddToWatchlist(ctx, in.GetId(), in.GetSecurityId())
	if err != nil {
		return nil, err
	}
	return watchlistItemToProto(it), nil
}

func (s *Server) RemoveFromWatchlist(ctx context.Context, in *tradingpb.RemoveFromWatchlistRequest) (*tradingpb.RemoveFromWatchlistResponse, error) {
	if err := s.Svc.RemoveFromWatchlist(ctx, in.GetId(), in.GetSecurityId()); err != nil {
		return nil, err
	}
	return &tradingpb.RemoveFromWatchlistResponse{}, nil
}

func watchlistToProto(w *domain.Watchlist) *tradingpb.Watchlist {
	if w == nil {
		return nil
	}
	out := &tradingpb.Watchlist{
		Id:        w.ID,
		UserId:    w.UserID,
		UserKind:  userKindToProto(w.UserKind),
		Name:      w.Name,
		CreatedAt: timestamppb.New(w.CreatedAt),
		Items:     make([]*tradingpb.WatchlistItem, 0, len(w.Items)),
	}
	for _, it := range w.Items {
		out.Items = append(out.Items, watchlistItemToProto(it))
	}
	return out
}

func watchlistItemToProto(it *domain.WatchlistItem) *tradingpb.WatchlistItem {
	if it == nil {
		return nil
	}
	return &tradingpb.WatchlistItem{
		Id:           it.ID,
		SecurityId:   it.SecurityID,
		CreatedAt:    timestamppb.New(it.CreatedAt),
		Ticker:       it.Ticker,
		Name:         it.Name,
		SecurityType: securityTypeToProto(it.SecurityType),
		Currency:     currencyToProto(it.Currency),
		Price:        it.Price,
		DailyChange:  it.DailyChange,
	}
}
