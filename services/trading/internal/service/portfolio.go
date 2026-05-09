package service

import (
	"context"
	"math/big"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/store"
)

// HoldingDecorated is a Holding row plus the security row plus the
// computed market metrics (current price, market value, profit).
type HoldingDecorated struct {
	Holding      *domain.Holding
	Security     *domain.Security
	CurrentPrice string
	MarketValue  string
	Profit       string
}

// ListHoldingsInput exposes ListHoldings filters to the server layer.
type ListHoldingsInput struct {
	UserID   string
	UserKind domain.UserKind
	Type     domain.SecurityType
}

// ListHoldings returns the caller's holdings (decorated). Visibility:
//   - clients/agents: their own holdings only;
//   - supervisors/admin: filterable by user_id / user_kind.
func (s *Service) ListHoldings(ctx context.Context, in ListHoldingsInput) ([]*HoldingDecorated, string, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, "0", err
	}
	supervisor := permissions.HasAny(p.Permissions, permissions.Admin, permissions.ActuarySupervisor)
	f := store.HoldingFilter{
		UserID:   in.UserID,
		UserKind: in.UserKind,
	}
	if !supervisor {
		f.UserID = p.UserID
		f.UserKind = domain.UserKind(p.UserKind)
	}
	rows, err := s.Store.ListHoldings(ctx, f)
	if err != nil {
		return nil, "0", err
	}

	totalProfit := new(big.Rat)
	out := make([]*HoldingDecorated, 0, len(rows))
	for _, h := range rows {
		sec, err := s.Store.GetSecurity(ctx, h.SecurityID)
		if err != nil {
			s.Log.Warn("holding security lookup failed", "holding_id", h.ID, "err", err.Error())
			continue
		}
		// Optional security-type filter.
		if in.Type != "" && sec.Type != in.Type {
			continue
		}
		dec := &HoldingDecorated{Holding: h, Security: sec}
		listing, err := s.Store.GetListingBySecurityID(ctx, h.SecurityID)
		if err == nil {
			dec.CurrentPrice = listing.Price
			price, _ := money.Parse(listing.Price)
			cs, _ := money.Parse(listing.ContractSize)
			if cs.Sign() == 0 {
				cs = money.MustParse("1")
			}
			qty := new(big.Rat).SetInt64(int64(h.Quantity))
			marketValue := money.Mul(qty, money.Mul(cs, price))
			dec.MarketValue = money.FormatAmount(marketValue)
			avg, _ := money.Parse(h.WeightedAvgPrice)
			costBasis := money.Mul(qty, money.Mul(cs, avg))
			profit := money.Sub(marketValue, costBasis)
			dec.Profit = money.FormatAmount(profit)
			totalProfit = money.Add(totalProfit, profit)
		}
		out = append(out, dec)
	}
	return out, money.FormatAmount(totalProfit), nil
}

// SetPublicCount toggles the spec p.61 OTC public-share count for one
// of the caller's holdings. Lands now so the column survives c4.
//
// Only the holding's owner (or supervisor/admin) may set it.
func (s *Service) SetPublicCount(ctx context.Context, holdingID string, count int32) (*domain.Holding, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if count < 0 {
		return nil, apperr.Validation("public_count must be non-negative")
	}
	h, err := s.Store.GetHoldingByID(ctx, holdingID)
	if err != nil {
		return nil, err
	}
	if h.UserID != p.UserID {
		if !permissions.HasAny(p.Permissions, permissions.Admin, permissions.ActuarySupervisor) {
			return nil, apperr.PermissionDenied("nedovoljne permisije")
		}
	}
	if count > h.Quantity {
		return nil, apperr.Validation("public_count ne može da bude veći od quantity")
	}
	return s.Store.SetPublicCount(ctx, holdingID, count)
}
