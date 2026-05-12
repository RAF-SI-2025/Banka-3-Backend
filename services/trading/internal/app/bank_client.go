package app

import (
	"context"
	"fmt"
	"math/big"

	bankpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/bank/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
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

// SettleForex bridges service.ForexSettler to bank.SettleForexFill.
// Same admin-metadata sentinel idiom as Settle.
func (a *bankSettlerAdapter) SettleForex(ctx context.Context, in service.SettleForexInput) (string, error) {
	ctx = auth.AttachToOutgoing(ctx, auth.Principal{
		UserID:      "00000000-0000-0000-0000-00000000fffe",
		UserKind:    auth.KindEmployee,
		Permissions: []string{permissions.Admin},
	})
	resp, err := a.c.SettleForexFill(ctx, &bankpb.SettleForexFillRequest{
		Direction:     in.Direction,
		BaseCurrency:  currencyToBankProto(in.BaseCurrency),
		BaseAmount:    in.BaseAmount,
		QuoteCurrency: currencyToBankProto(in.QuoteCurrency),
		QuoteAmount:   in.QuoteAmount,
		OpId:          in.OpID,
		Purpose:       in.Purpose,
	})
	if err != nil {
		return "", fmt.Errorf("bank.SettleForexFill: %w", err)
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

// AccountAvailable reads the source account's currency + available
// balance via bank.GetAccount. Uses the admin-sentinel principal so the
// bank's canSeeAccount check admits the read regardless of who owns
// the account.
func (a *bankSettlerAdapter) AccountAvailable(ctx context.Context, accountID string) (domain.Currency, string, error) {
	ctx = auth.AttachToOutgoing(ctx, auth.Principal{
		UserID:      "00000000-0000-0000-0000-00000000fffe",
		UserKind:    auth.KindEmployee,
		Permissions: []string{permissions.Admin},
	})
	resp, err := a.c.GetAccount(ctx, &bankpb.GetAccountRequest{Id: accountID})
	if err != nil {
		return "", "", fmt.Errorf("bank.GetAccount: %w", err)
	}
	return currencyFromBankProto(resp.GetCurrency()), resp.GetAvailableBalance(), nil
}

// Reserve bridges service.BankReservations.Reserve to
// bank.ReserveFunds with the admin-sentinel principal.
func (a *bankSettlerAdapter) Reserve(ctx context.Context, in service.ReserveInput) (string, error) {
	ctx = auth.AttachToOutgoing(ctx, auth.Principal{
		UserID:      "00000000-0000-0000-0000-00000000fffe",
		UserKind:    auth.KindEmployee,
		Permissions: []string{permissions.Admin},
	})
	resp, err := a.c.ReserveFunds(ctx, &bankpb.ReserveFundsRequest{
		AccountId: in.AccountID,
		Amount:    in.Amount,
		Currency:  currencyToBankProto(in.Currency),
		OpId:      in.OpID,
		OpKind:    in.OpKind,
	})
	if err != nil {
		return "", fmt.Errorf("bank.ReserveFunds: %w", err)
	}
	return resp.GetReservationId(), nil
}

// Release bridges service.BankReservations.Release to bank.ReleaseFunds.
// Returns whether the call moved the row from held→released (false on a
// no-op release of an already-released or never-existed reservation).
func (a *bankSettlerAdapter) Release(ctx context.Context, opID string) (bool, error) {
	ctx = auth.AttachToOutgoing(ctx, auth.Principal{
		UserID:      "00000000-0000-0000-0000-00000000fffe",
		UserKind:    auth.KindEmployee,
		Permissions: []string{permissions.Admin},
	})
	resp, err := a.c.ReleaseFunds(ctx, &bankpb.ReleaseFundsRequest{OpId: opID})
	if err != nil {
		return false, fmt.Errorf("bank.ReleaseFunds: %w", err)
	}
	return resp.GetReleased(), nil
}

// Commit bridges service.BankReservations.Commit to bank.CommitReservedFunds.
func (a *bankSettlerAdapter) Commit(ctx context.Context, in service.CommitInput) (string, error) {
	ctx = auth.AttachToOutgoing(ctx, auth.Principal{
		UserID:      "00000000-0000-0000-0000-00000000fffe",
		UserKind:    auth.KindEmployee,
		Permissions: []string{permissions.Admin},
	})
	resp, err := a.c.CommitReservedFunds(ctx, &bankpb.CommitReservedFundsRequest{
		OpId:          in.OpID,
		DestAccountId: in.DestAccountID,
		DestAmount:    in.DestAmount,
		DestCurrency:  currencyToBankProto(in.DestCurrency),
		IsActuary:     in.IsActuary,
		Purpose:       in.Purpose,
	})
	if err != nil {
		return "", fmt.Errorf("bank.CommitReservedFunds: %w", err)
	}
	return resp.GetOpId(), nil
}

// Transfer bridges service.BankReservations.Transfer to
// bank.TransferBetweenClients.
func (a *bankSettlerAdapter) Transfer(ctx context.Context, in service.TransferInput) (string, error) {
	ctx = auth.AttachToOutgoing(ctx, auth.Principal{
		UserID:      "00000000-0000-0000-0000-00000000fffe",
		UserKind:    auth.KindEmployee,
		Permissions: []string{permissions.Admin},
	})
	resp, err := a.c.TransferBetweenClients(ctx, &bankpb.TransferBetweenClientsRequest{
		FromAccountId: in.FromAccountID,
		ToAccountId:   in.ToAccountID,
		Amount:        in.Amount,
		OpId:          in.OpID,
		OpKind:        in.OpKind,
		IsActuary:     in.IsActuary,
		Purpose:       in.Purpose,
	})
	if err != nil {
		return "", fmt.Errorf("bank.TransferBetweenClients: %w", err)
	}
	return resp.GetOpId(), nil
}

// ClientLargestActiveLoan picks the largest remaining_principal across
// the client's active loans. Returns ("","",nil) when none exist.
func (a *bankSettlerAdapter) ClientLargestActiveLoan(ctx context.Context, clientID string) (domain.Currency, string, error) {
	ctx = auth.AttachToOutgoing(ctx, auth.Principal{
		UserID:      "00000000-0000-0000-0000-00000000fffe",
		UserKind:    auth.KindEmployee,
		Permissions: []string{permissions.Admin},
	})
	resp, err := a.c.ListLoans(ctx, &bankpb.ListLoansRequest{
		ClientId: clientID,
		Status:   bankpb.LoanStatus_LOAN_STATUS_APPROVED,
		PageSize: 100,
	})
	if err != nil {
		return "", "", fmt.Errorf("bank.ListLoans: %w", err)
	}
	var bestCur domain.Currency
	bestStr := ""
	var bestRat *big.Rat
	for _, l := range resp.GetLoans() {
		amt := l.GetRemainingPrincipal()
		if amt == "" {
			continue
		}
		r, err := money.Parse(amt)
		if err != nil {
			continue
		}
		if bestRat == nil || r.Cmp(bestRat) > 0 {
			bestRat = r
			bestStr = amt
			bestCur = currencyFromBankProto(l.GetCurrency())
		}
	}
	return bestCur, bestStr, nil
}

func currencyFromBankProto(c bankpb.Currency) domain.Currency {
	switch c {
	case bankpb.Currency_CURRENCY_RSD:
		return domain.CurrencyRSD
	case bankpb.Currency_CURRENCY_EUR:
		return domain.CurrencyEUR
	case bankpb.Currency_CURRENCY_CHF:
		return domain.CurrencyCHF
	case bankpb.Currency_CURRENCY_USD:
		return domain.CurrencyUSD
	case bankpb.Currency_CURRENCY_GBP:
		return domain.CurrencyGBP
	case bankpb.Currency_CURRENCY_JPY:
		return domain.CurrencyJPY
	case bankpb.Currency_CURRENCY_CAD:
		return domain.CurrencyCAD
	case bankpb.Currency_CURRENCY_AUD:
		return domain.CurrencyAUD
	}
	return ""
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
