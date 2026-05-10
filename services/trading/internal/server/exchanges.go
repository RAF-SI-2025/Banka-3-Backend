package server

import (
	"context"

	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/trading/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/service"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *Server) ListExchanges(ctx context.Context, _ *tradingpb.ListExchangesRequest) (*tradingpb.ListExchangesResponse, error) {
	out, err := s.Svc.ListExchanges(ctx)
	if err != nil {
		return nil, err
	}
	resp := &tradingpb.ListExchangesResponse{Exchanges: make([]*tradingpb.Exchange, 0, len(out))}
	for _, e := range out {
		resp.Exchanges = append(resp.Exchanges, marketStateToProto(e))
	}
	return resp, nil
}

func (s *Server) UpsertExchange(ctx context.Context, in *tradingpb.UpsertExchangeRequest) (*tradingpb.Exchange, error) {
	e, err := s.Svc.UpsertExchange(ctx, &domain.Exchange{
		MIC:        in.GetMic(),
		Name:       in.GetName(),
		Acronym:    in.GetAcronym(),
		Polity:     in.GetPolity(),
		Currency:   currencyFromProto(in.GetCurrency()),
		Timezone:   in.GetTimezone(),
		OpenLocal:  in.GetOpenLocal(),
		CloseLocal: in.GetCloseLocal(),
	})
	if err != nil {
		return nil, err
	}
	ms := s.Svc.MarketStateForRead(e)
	return marketStateToProto(ms), nil
}

func (s *Server) SetExchangeOverride(ctx context.Context, in *tradingpb.SetExchangeOverrideRequest) (*tradingpb.Exchange, error) {
	var state *domain.ExchangeOverrideState
	if v := in.GetState(); v != "" {
		s := domain.ExchangeOverrideState(v)
		state = &s
	}
	e, err := s.Svc.SetExchangeOverride(ctx, in.GetMic(), state)
	if err != nil {
		return nil, err
	}
	ms := s.Svc.MarketStateForRead(e)
	return marketStateToProto(ms), nil
}

// marketStateToProto fills the resolved is_open / after_hours flags.
func marketStateToProto(ms *service.MarketState) *tradingpb.Exchange {
	if ms == nil || ms.Exchange == nil {
		return nil
	}
	e := ms.Exchange
	out := &tradingpb.Exchange{
		Mic:          e.MIC,
		Name:         e.Name,
		Acronym:      e.Acronym,
		Polity:       e.Polity,
		Currency:     currencyToProto(e.Currency),
		Timezone:     e.Timezone,
		OpenLocal:    e.OpenLocal,
		CloseLocal:   e.CloseLocal,
		IsOpen:       ms.IsOpen,
		IsAfterHours: ms.IsAfterHours,
		UpdatedAt:    timestamppb.New(e.UpdatedAt),
	}
	if e.OverrideState != nil {
		out.OverrideState = string(*e.OverrideState)
	}
	return out
}

// currencyToProto and currencyFromProto live with the trading currency
// enum mapping. Symmetric to bank/exchange services.
func currencyToProto(c domain.Currency) tradingpb.Currency {
	switch c {
	case domain.CurrencyRSD:
		return tradingpb.Currency_CURRENCY_RSD
	case domain.CurrencyEUR:
		return tradingpb.Currency_CURRENCY_EUR
	case domain.CurrencyCHF:
		return tradingpb.Currency_CURRENCY_CHF
	case domain.CurrencyUSD:
		return tradingpb.Currency_CURRENCY_USD
	case domain.CurrencyGBP:
		return tradingpb.Currency_CURRENCY_GBP
	case domain.CurrencyJPY:
		return tradingpb.Currency_CURRENCY_JPY
	case domain.CurrencyCAD:
		return tradingpb.Currency_CURRENCY_CAD
	case domain.CurrencyAUD:
		return tradingpb.Currency_CURRENCY_AUD
	}
	return tradingpb.Currency_CURRENCY_UNSPECIFIED
}

func currencyFromProto(c tradingpb.Currency) domain.Currency {
	switch c {
	case tradingpb.Currency_CURRENCY_RSD:
		return domain.CurrencyRSD
	case tradingpb.Currency_CURRENCY_EUR:
		return domain.CurrencyEUR
	case tradingpb.Currency_CURRENCY_CHF:
		return domain.CurrencyCHF
	case tradingpb.Currency_CURRENCY_USD:
		return domain.CurrencyUSD
	case tradingpb.Currency_CURRENCY_GBP:
		return domain.CurrencyGBP
	case tradingpb.Currency_CURRENCY_JPY:
		return domain.CurrencyJPY
	case tradingpb.Currency_CURRENCY_CAD:
		return domain.CurrencyCAD
	case tradingpb.Currency_CURRENCY_AUD:
		return domain.CurrencyAUD
	}
	return ""
}
