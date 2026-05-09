package app

import (
	"context"
	"fmt"

	exchangepb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/exchange/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
)

// exchangeAdapter implements service.RateProvider by dialing the
// exchange service. Lives in the app layer so the service layer stays
// free of any gen/proto imports.
type exchangeAdapter struct {
	c exchangepb.ExchangeServiceClient
}

func (a *exchangeAdapter) Quote(ctx context.Context, from, to domain.Currency) (string, string, error) {
	resp, err := a.c.Quote(ctx, &exchangepb.QuoteRequest{
		From: currencyToExchangeProto(from),
		To:   currencyToExchangeProto(to),
	})
	if err != nil {
		return "", "", fmt.Errorf("exchange quote: %w", err)
	}
	return resp.GetBid(), resp.GetAsk(), nil
}

func currencyToExchangeProto(c domain.Currency) exchangepb.Currency {
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
