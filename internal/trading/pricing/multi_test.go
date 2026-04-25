package pricing

import (
	"context"
	"errors"
	"testing"
)

type fakeClient struct {
	name string
	q    Quote
	err  error
}

func (f *fakeClient) Name() string { return f.name }
func (f *fakeClient) GetQuote(_ context.Context, _ string) (Quote, error) {
	return f.q, f.err
}

func TestMulti_FirstSuccess(t *testing.T) {
	a := &fakeClient{name: "a", q: Quote{Ticker: "X", PriceCents: 100}}
	b := &fakeClient{name: "b", q: Quote{Ticker: "X", PriceCents: 200}}
	m := NewMulti(a, b)
	q, err := m.GetQuote(context.Background(), "X")
	if err != nil {
		t.Fatalf("GetQuote: %v", err)
	}
	if q.PriceCents != 100 {
		t.Errorf("got price %d, want first provider's 100", q.PriceCents)
	}
}

func TestMulti_FallthroughOnNotFound(t *testing.T) {
	a := &fakeClient{name: "a", err: ErrNotFound}
	b := &fakeClient{name: "b", q: Quote{Ticker: "X", PriceCents: 200}}
	m := NewMulti(a, b)
	q, err := m.GetQuote(context.Background(), "X")
	if err != nil {
		t.Fatalf("GetQuote: %v", err)
	}
	if q.PriceCents != 200 {
		t.Errorf("got price %d, want fallback's 200", q.PriceCents)
	}
}

func TestMulti_FallthroughOnRateLimit(t *testing.T) {
	a := &fakeClient{name: "a", err: ErrRateLimited}
	b := &fakeClient{name: "b", q: Quote{Ticker: "X", PriceCents: 200}}
	m := NewMulti(a, b)
	q, err := m.GetQuote(context.Background(), "X")
	if err != nil {
		t.Fatalf("GetQuote: %v", err)
	}
	if q.PriceCents != 200 {
		t.Errorf("got price %d, want fallback's 200", q.PriceCents)
	}
}

func TestMulti_HardErrorShortCircuits(t *testing.T) {
	hard := errors.New("connection refused")
	a := &fakeClient{name: "a", err: hard}
	b := &fakeClient{name: "b", q: Quote{Ticker: "X", PriceCents: 200}}
	m := NewMulti(a, b)
	_, err := m.GetQuote(context.Background(), "X")
	if err == nil {
		t.Fatal("expected error on hard failure")
	}
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrRateLimited) {
		t.Errorf("hard error misclassified: %v", err)
	}
}

func TestMulti_AllSkipped(t *testing.T) {
	a := &fakeClient{name: "a", err: ErrNotFound}
	b := &fakeClient{name: "b", err: ErrRateLimited}
	m := NewMulti(a, b)
	_, err := m.GetQuote(context.Background(), "X")
	if !errors.Is(err, ErrRateLimited) && !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want last skip error", err)
	}
}

func TestMulti_NoProviders(t *testing.T) {
	m := NewMulti()
	_, err := m.GetQuote(context.Background(), "X")
	if err == nil {
		t.Fatal("expected error with no providers")
	}
}

func TestMulti_NilSkipped(t *testing.T) {
	a := &fakeClient{name: "a", q: Quote{Ticker: "X", PriceCents: 7}}
	m := NewMulti(nil, a, nil)
	q, err := m.GetQuote(context.Background(), "X")
	if err != nil {
		t.Fatalf("GetQuote: %v", err)
	}
	if q.PriceCents != 7 {
		t.Errorf("price = %d", q.PriceCents)
	}
}
