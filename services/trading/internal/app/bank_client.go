package app

import (
	"context"
	"fmt"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/bank/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/service"
)

// bankSettlerAdapter wraps the bank-service gRPC client and satisfies
// service.TradeSettler. The execution worker calls this on every fill
// to settle one leg of money movement against the user's account.
type bankSettlerAdapter struct {
	c bankpb.BankServiceClient
}

func (a *bankSettlerAdapter) Settle(ctx context.Context, in service.SettleInput) (string, error) {
	// Internal call: pad outgoing metadata with an admin principal so
	// the bank's incoming-metadata interceptor admits us as a
	// principal. We use a sentinel UUID — the bank's auth interceptor
	// rejects empty user-ids — and the bank-side SettleTrade handler
	// clears it before calling the money-move engine so it doesn't
	// land in transactions.initiator_client_id.
	ctx = auth.AttachToOutgoing(ctx, auth.Principal{
		UserID:      "00000000-0000-0000-0000-00000000fffe",
		UserKind:    auth.KindEmployee,
		Permissions: []string{permissions.Admin},
	})
	resp, err := a.c.SettleTrade(ctx, &bankpb.SettleTradeRequest{
		AccountId: in.AccountID,
		Direction: in.Direction,
		Currency:  currencyToBankProto(in.Currency),
		Amount:    in.Amount,
		OpId:      in.OpID,
		IsActuary: in.IsActuary,
		Purpose:   in.Purpose,
	})
	if err != nil {
		return "", fmt.Errorf("bank.SettleTrade: %w", err)
	}
	return resp.GetOpId(), nil
}

// SettleTax bridges service.TaxSettler to bank.SettleCapitalGainsTax.
// Same admin-metadata sentinel idiom as Settle — bank's interceptor
// rejects empty user-ids, and the bank-side handler clears the sentinel
// before writing initiator_client_id.
func (a *bankSettlerAdapter) SettleTax(ctx context.Context, in service.TaxSettleInput) (string, error) {
	ctx = auth.AttachToOutgoing(ctx, auth.Principal{
		UserID:      "00000000-0000-0000-0000-00000000fffe",
		UserKind:    auth.KindEmployee,
		Permissions: []string{permissions.Admin},
	})
	resp, err := a.c.SettleCapitalGainsTax(ctx, &bankpb.SettleCapitalGainsTaxRequest{
		AccountId: in.AccountID,
		AmountRsd: in.AmountRSD,
		OpId:      in.OpID,
		Purpose:   in.Purpose,
	})
	if err != nil {
		return "", fmt.Errorf("bank.SettleCapitalGainsTax: %w", err)
	}
	return resp.GetOpId(), nil
}

func currencyToBankProto(c domain.Currency) bankpb.Currency {
	switch c {
	case domain.CurrencyRSD:
		return bankpb.Currency_CURRENCY_RSD
	case domain.CurrencyEUR:
		return bankpb.Currency_CURRENCY_EUR
	case domain.CurrencyCHF:
		return bankpb.Currency_CURRENCY_CHF
	case domain.CurrencyUSD:
		return bankpb.Currency_CURRENCY_USD
	case domain.CurrencyGBP:
		return bankpb.Currency_CURRENCY_GBP
	case domain.CurrencyJPY:
		return bankpb.Currency_CURRENCY_JPY
	case domain.CurrencyCAD:
		return bankpb.Currency_CURRENCY_CAD
	case domain.CurrencyAUD:
		return bankpb.Currency_CURRENCY_AUD
	}
	return bankpb.Currency_CURRENCY_UNSPECIFIED
}
