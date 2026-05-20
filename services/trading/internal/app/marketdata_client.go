package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/external/alphavantage"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/service"
)

// alphaStockAdapter implements service.StockQuoteProvider against the
// Alpha Vantage GLOBAL_QUOTE endpoint. The adapter narrows the rich
// upstream payload to (price, change, volume), the only fields the
// refresher persists, and translates alphavantage.ErrThrottled into
// the provider-agnostic service.ErrMarketDataThrottled sentinel.
type alphaStockAdapter struct {
	c *alphavantage.Client
}

func (a *alphaStockAdapter) Quote(ctx context.Context, symbol string) (price, change string, volume int64, err error) {
	q, err := a.c.Quote(ctx, symbol)
	if err != nil {
		if errors.Is(err, alphavantage.ErrThrottled) {
			return "", "", 0, fmt.Errorf("%w: %v", service.ErrMarketDataThrottled, err)
		}
		return "", "", 0, err
	}
	return q.Price, q.Change, q.Volume, nil
}

// alphaForexAdapter implements service.ForexQuoteProvider.
type alphaForexAdapter struct {
	c *alphavantage.Client
}

func (a *alphaForexAdapter) FXQuote(ctx context.Context, from, to string) (bid, ask string, err error) {
	q, err := a.c.FXQuote(ctx, from, to)
	if err != nil {
		if errors.Is(err, alphavantage.ErrThrottled) {
			return "", "", fmt.Errorf("%w: %v", service.ErrMarketDataThrottled, err)
		}
		return "", "", err
	}
	return q.Bid, q.Ask, nil
}

// alphaHistoryAdapter implements service.StockHistoryProvider against
// the Alpha Vantage TIME_SERIES_DAILY endpoint, translating
// alphavantage.ErrThrottled into the provider-agnostic
// service.ErrMarketDataThrottled sentinel like the other adapters.
type alphaHistoryAdapter struct {
	c *alphavantage.Client
}

func (a *alphaHistoryAdapter) DailyHistory(ctx context.Context, symbol string) ([]service.HistoryBar, error) {
	bars, err := a.c.TimeSeriesDaily(ctx, symbol)
	if err != nil {
		if errors.Is(err, alphavantage.ErrThrottled) {
			return nil, fmt.Errorf("%w: %v", service.ErrMarketDataThrottled, err)
		}
		return nil, err
	}
	out := make([]service.HistoryBar, 0, len(bars))
	for _, b := range bars {
		out = append(out, service.HistoryBar{Date: b.Date, Close: b.Close, Volume: b.Volume})
	}
	return out, nil
}
