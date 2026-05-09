package service

import (
	"context"
	"math/big"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
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

	// Quote previews don't always have an authenticated principal on
	// ctx (e.g. the public rates calculator). Treat absent principal
	// as "client" — clients pay the commission, actuaries don't.
	p, _ := auth.PrincipalFrom(ctx)
	commission := big.NewRat(0, 1)
	toAfter := toBefore
	if includeCommission {
		commission = money.Mul(toBefore, s.commissionRateFor(p))
		toAfter = money.Sub(toBefore, commission)
	}

	return &QuoteResult{
		FromAmount: money.FormatAmount(amt),
		ToAmount:   money.FormatAmount(toAfter),
		Rate:       money.FormatRate(composite),
		Commission: money.FormatAmount(commission),
	}, nil
}

// commissionRateFor returns the FX commission to apply for principal p.
//
// Spec edge case (CLAUDE.md #2): clients pay 0–1% on every cross-
// currency conversion; actuaries trading on behalf of the bank pay
// none. For c2 no role bundle carries the Actuary permission so this
// always returns the configured rate, but the structural branch is
// in place so c3 actuary-driven trades route through the same code
// path without retrofit.
func (s *Service) commissionRateFor(p auth.Principal) *big.Rat {
	if permissions.IsActuary(p.Permissions) {
		return big.NewRat(0, 1)
	}
	return s.commissionRate()
}

// commissionRate returns the configured FX commission (default 0.5%).
// Clients of this method must already have decided that the actor is
// not an actuary; use commissionRateFor at call sites that touch a
// principal.
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
//
// Spec p.26 pins the rate-direction policy: "uvek koristi prodajni
// kurs" — always use the sell-side rate of the foreign/RSD pair, on
// every leg, even when the bank is buying foreign from the client.
// The bank's profit comes from the commission, not from the bid/ask
// spread. Cross-currency goes through RSD: X → RSD at ASK_X, then
// RSD → Y at ASK_Y (primer 2). The BID column is unused by this path.
func (s *Service) rateAndConvert(ctx context.Context, from, to domain.Currency, amt *big.Rat) (composite, toAmount *big.Rat, err error) {
	if s.Rates == nil {
		return nil, nil, apperr.Internal("exchange rate provider not configured", nil)
	}
	switch {
	case from == domain.CurrencyRSD:
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
		_, ask, err := s.Rates.Quote(ctx, from, domain.CurrencyRSD)
		if err != nil {
			return nil, nil, err
		}
		askR, perr := money.Parse(ask)
		if perr != nil {
			return nil, nil, apperr.Internal("parse rate", perr)
		}
		conv := money.Mul(amt, askR)
		return askR, conv, nil
	default:
		_, askFrom, err := s.Rates.Quote(ctx, from, domain.CurrencyRSD)
		if err != nil {
			return nil, nil, err
		}
		_, askTo, err := s.Rates.Quote(ctx, to, domain.CurrencyRSD)
		if err != nil {
			return nil, nil, err
		}
		askFromR, perr := money.Parse(askFrom)
		if perr != nil {
			return nil, nil, apperr.Internal("parse from rate", perr)
		}
		askToR, perr := money.Parse(askTo)
		if perr != nil {
			return nil, nil, apperr.Internal("parse to rate", perr)
		}
		rsdAmt := money.Mul(amt, askFromR)
		conv, derr := money.Div(rsdAmt, askToR)
		if derr != nil {
			return nil, nil, apperr.Validation(derr.Error())
		}
		composite, derr := money.Div(askFromR, askToR)
		if derr != nil {
			return nil, nil, apperr.Internal("composite rate", derr)
		}
		return composite, conv, nil
	}
}
