// Package server adapts the proto-generated ExchangeService surface to
// the service layer.
package server

import (
	"context"

	exchangepb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/exchange/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/exchange/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/exchange/internal/service"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Server struct {
	exchangepb.UnimplementedExchangeServiceServer
	Svc *service.Service
}

func New(svc *service.Service) *Server { return &Server{Svc: svc} }

func (s *Server) UpsertRate(ctx context.Context, in *exchangepb.UpsertRateRequest) (*exchangepb.Rate, error) {
	r, err := s.Svc.UpsertRate(ctx, &domain.Rate{
		From: currencyFromProto(in.GetFrom()),
		To:   currencyFromProto(in.GetTo()),
		Bid:  in.GetBid(),
		Ask:  in.GetAsk(),
	})
	if err != nil {
		return nil, err
	}
	return rateToProto(r), nil
}

func (s *Server) ListRates(ctx context.Context, in *exchangepb.ListRatesRequest) (*exchangepb.ListRatesResponse, error) {
	rates, err := s.Svc.ListRates(ctx, currencyFromProto(in.GetFrom()))
	if err != nil {
		return nil, err
	}
	out := make([]*exchangepb.Rate, 0, len(rates))
	for _, r := range rates {
		out = append(out, rateToProto(r))
	}
	return &exchangepb.ListRatesResponse{Rates: out}, nil
}

func (s *Server) Quote(ctx context.Context, in *exchangepb.QuoteRequest) (*exchangepb.Rate, error) {
	r, err := s.Svc.Quote(ctx, currencyFromProto(in.GetFrom()), currencyFromProto(in.GetTo()))
	if err != nil {
		return nil, err
	}
	return rateToProto(r), nil
}

func rateToProto(r *domain.Rate) *exchangepb.Rate {
	return &exchangepb.Rate{
		From:      currencyToProto(r.From),
		To:        currencyToProto(r.To),
		Bid:       r.Bid,
		Ask:       r.Ask,
		UpdatedAt: timestamppb.New(r.UpdatedAt),
	}
}

func currencyToProto(c domain.Currency) exchangepb.Currency {
	switch c {
	case domain.CurrencyRSD:
		return exchangepb.Currency_CURRENCY_RSD
	case domain.CurrencyEUR:
		return exchangepb.Currency_CURRENCY_EUR
	case domain.CurrencyCHF:
		return exchangepb.Currency_CURRENCY_CHF
	case domain.CurrencyUSD:
		return exchangepb.Currency_CURRENCY_USD
	case domain.CurrencyGBP:
		return exchangepb.Currency_CURRENCY_GBP
	case domain.CurrencyJPY:
		return exchangepb.Currency_CURRENCY_JPY
	case domain.CurrencyCAD:
		return exchangepb.Currency_CURRENCY_CAD
	case domain.CurrencyAUD:
		return exchangepb.Currency_CURRENCY_AUD
	}
	return exchangepb.Currency_CURRENCY_UNSPECIFIED
}

func currencyFromProto(c exchangepb.Currency) domain.Currency {
	switch c {
	case exchangepb.Currency_CURRENCY_RSD:
		return domain.CurrencyRSD
	case exchangepb.Currency_CURRENCY_EUR:
		return domain.CurrencyEUR
	case exchangepb.Currency_CURRENCY_CHF:
		return domain.CurrencyCHF
	case exchangepb.Currency_CURRENCY_USD:
		return domain.CurrencyUSD
	case exchangepb.Currency_CURRENCY_GBP:
		return domain.CurrencyGBP
	case exchangepb.Currency_CURRENCY_JPY:
		return domain.CurrencyJPY
	case exchangepb.Currency_CURRENCY_CAD:
		return domain.CurrencyCAD
	case exchangepb.Currency_CURRENCY_AUD:
		return domain.CurrencyAUD
	}
	return ""
}
