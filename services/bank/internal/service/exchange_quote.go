package service

import (
	"context"
	"math/big"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
)

// RateProvider abstracts the exchange service. The app layer wires it
// to a gRPC client; tests stub it directly.
type RateProvider interface {
	// Quote returns the directional bid/ask for `from` → `to`.
	Quote(ctx context.Context, from, to domain.Currency) (bid, ask string, err error)
}

// QuoteResult is the resolved menjačnica preview.
type QuoteResult struct {
	FromAmount string
	ToAmount   string
	Rate       string // "1 from = Rate to"; empty for same-currency
	Commission string // in `to` currency
}

// QuoteExchange computes the destination amount + composite rate +
// commission for the given from/to/amount triple.
func (s *Service) QuoteExchange(ctx context.Context, from, to domain.Currency, amount string, includeCommission bool) (*QuoteResult, error) {
	if !from.Supported() || !to.Supported() {
		return nil, apperr.Validation("unsupported currency")
	}
	amt, err := money.Parse(amount)
	if err != nil {
		return nil, apperr.Validation(err.Error())
	}
	if !money.IsPositive(amt) {
		return nil, apperr.Validation("amount must be positive")
	}

	if from == to {
		return &QuoteResult{
			FromAmount: money.FormatAmount(amt),
			ToAmount:   money.FormatAmount(amt),
			Rate:       "",
			Commission: "0.0000",
		}, nil
	}

	composite, toBefore, err := s.rateAndConvert(ctx, from, to, amt)
	if err != nil {
		return nil, err
	}

	commission := big.NewRat(0, 1)
	toAfter := toBefore
	if includeCommission {
		commission = money.Mul(toBefore, s.commissionRate())
		toAfter = money.Sub(toBefore, commission)
	}

	return &QuoteResult{
		FromAmount: money.FormatAmount(amt),
		ToAmount:   money.FormatAmount(toAfter),
		Rate:       money.FormatRate(composite),
		Commission: money.FormatAmount(commission),
	}, nil
}

// commissionRate returns the configured FX commission (default 0.5%).
func (s *Service) commissionRate() *big.Rat {
	if s.Cfg.FXCommission != "" {
		if r, err := money.Parse(s.Cfg.FXCommission); err == nil {
			return r
		}
	}
	return money.MustParse("0.005")
}

// rateAndConvert resolves the conversion. Returns composite rate
// (to per 1 from) and to-amount before commission.
func (s *Service) rateAndConvert(ctx context.Context, from, to domain.Currency, amt *big.Rat) (composite, toAmount *big.Rat, err error) {
	if s.Rates == nil {
		return nil, nil, apperr.Internal("exchange rate provider not configured", nil)
	}
	switch {
	case from == domain.CurrencyRSD:
		// RSD → X: bank sells X, use ASK of (X,RSD).
		_, ask, err := s.Rates.Quote(ctx, to, domain.CurrencyRSD)
		if err != nil {
			return nil, nil, err
		}
		askR, perr := money.Parse(ask)
		if perr != nil {
			return nil, nil, apperr.Internal("parse rate", perr)
		}
		conv, derr := money.Div(amt, askR)
		if derr != nil {
			return nil, nil, apperr.Validation(derr.Error())
		}
		invAsk, derr := money.Div(money.MustParse("1"), askR)
		if derr != nil {
			return nil, nil, apperr.Internal("invert rate", derr)
		}
		return invAsk, conv, nil
	case to == domain.CurrencyRSD:
		// X → RSD: bank buys X, use BID of (X,RSD).
		bid, _, err := s.Rates.Quote(ctx, from, domain.CurrencyRSD)
		if err != nil {
			return nil, nil, err
		}
		bidR, perr := money.Parse(bid)
		if perr != nil {
			return nil, nil, apperr.Internal("parse rate", perr)
		}
		conv := money.Mul(amt, bidR)
		return bidR, conv, nil
	default:
		// X → Y: X→RSD at bid, then RSD→Y at ask.
		bid, _, err := s.Rates.Quote(ctx, from, domain.CurrencyRSD)
		if err != nil {
			return nil, nil, err
		}
		_, ask, err := s.Rates.Quote(ctx, to, domain.CurrencyRSD)
		if err != nil {
			return nil, nil, err
		}
		bidR, _ := money.Parse(bid)
		askR, _ := money.Parse(ask)
		rsdAmt := money.Mul(amt, bidR)
		conv, derr := money.Div(rsdAmt, askR)
		if derr != nil {
			return nil, nil, apperr.Validation(derr.Error())
		}
		composite, derr := money.Div(bidR, askR)
		if derr != nil {
			return nil, nil, apperr.Internal("composite rate", derr)
		}
		return composite, conv, nil
	}
}
