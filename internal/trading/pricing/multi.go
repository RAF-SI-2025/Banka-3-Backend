package pricing

import (
	"context"
	"errors"
	"fmt"
)

// MultiClient tries its underlying providers in order and returns the first
// successful quote. ErrNotFound from one provider falls through to the next
// (a delisted Alpaca ticker may still resolve on Alpha Vantage); other
// errors short-circuit so a single misconfigured provider doesn't quietly
// hide a real outage of the next.
//
// ErrRateLimited is treated like ErrNotFound for fall-through: AV's
// 25-call/day cap is the operationally noisy one, and we'd rather try
// Alpaca than fail the whole refresh tick.
type MultiClient struct {
	Providers []Client
}

func NewMulti(clients ...Client) *MultiClient {
	out := make([]Client, 0, len(clients))
	for _, c := range clients {
		if c != nil {
			out = append(out, c)
		}
	}
	return &MultiClient{Providers: out}
}

func (m *MultiClient) Name() string { return "multi" }

func (m *MultiClient) GetQuote(ctx context.Context, ticker string) (Quote, error) {
	if len(m.Providers) == 0 {
		return Quote{}, fmt.Errorf("pricing: no providers configured")
	}
	var lastSkip error
	for _, p := range m.Providers {
		q, err := p.GetQuote(ctx, ticker)
		if err == nil {
			return q, nil
		}
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrRateLimited) {
			lastSkip = err
			continue
		}
		return Quote{}, fmt.Errorf("%s: %w", p.Name(), err)
	}
	if lastSkip != nil {
		return Quote{}, lastSkip
	}
	return Quote{}, ErrNotFound
}
