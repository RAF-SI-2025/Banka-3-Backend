package server

import (
	"context"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/trading/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/service"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// parseDatePtr turns an optional YYYY-MM-DD string into a *time.Time.
// Empty → (nil, nil) meaning "unbounded". Used for the catalog
// settlement-date range: the FE binds these from <input type="date">,
// and a google.protobuf.Timestamp can't be bound from a bare date
// query param through grpc-gateway (it silently dropped the filter).
func parseDatePtr(v string) (*time.Time, error) {
	if v == "" {
		return nil, nil
	}
	t, err := time.Parse("2006-01-02", v)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *Server) UpsertSecurity(ctx context.Context, in *tradingpb.UpsertSecurityRequest) (*tradingpb.Security, error) {
	sec := &domain.Security{
		ID:                   in.GetId(),
		Ticker:               in.GetTicker(),
		Name:                 in.GetName(),
		Type:                 securityTypeFromProto(in.GetType()),
		ExchangeMIC:          in.GetExchangeMic(),
		Currency:             currencyFromProto(in.GetCurrency()),
		OutstandingShares:    in.GetOutstandingShares(),
		DividendYield:        in.GetDividendYield(),
		ContractSize:         in.GetContractSize(),
		ContractUnit:         in.GetContractUnit(),
		BaseCurrency:         currencyFromProto(in.GetBaseCurrency()),
		QuoteCurrency:        currencyFromProto(in.GetQuoteCurrency()),
		Liquidity:            in.GetLiquidity(),
		UnderlyingSecurityID: in.GetUnderlyingSecurityId(),
		OptionType:           optionTypeFromProto(in.GetOptionType()),
		StrikePrice:          in.GetStrikePrice(),
		ImpliedVolatility:    in.GetImpliedVolatility(),
		Premium:              in.GetPremium(),
		OpenInterest:         in.GetOpenInterest(),
	}
	if t := in.GetSettlementDate(); t != nil {
		v := t.AsTime()
		sec.SettlementDate = &v
	}
	out, err := s.Svc.UpsertSecurity(ctx, sec)
	if err != nil {
		return nil, err
	}
	return securityToProto(out), nil
}

func (s *Server) GetSecurity(ctx context.Context, in *tradingpb.GetSecurityRequest) (*tradingpb.SecurityWithListing, error) {
	swl, err := s.Svc.GetSecurity(ctx, in.GetId())
	if err != nil {
		return nil, err
	}
	return securityWithListingToProto(swl), nil
}

func (s *Server) ListSecurities(ctx context.Context, in *tradingpb.ListSecuritiesRequest) (*tradingpb.ListSecuritiesResponse, error) {
	input := service.ListSecuritiesInput{
		Type:        securityTypeFromProto(in.GetType()),
		Search:      in.GetSearch(),
		ExchangeMIC: in.GetExchangeMic(),
		MinPrice:    in.GetMinPrice(),
		MaxPrice:    in.GetMaxPrice(),
		MinAsk:      in.GetMinAsk(),
		MaxAsk:      in.GetMaxAsk(),
		MinBid:      in.GetMinBid(),
		MaxBid:      in.GetMaxBid(),
		MinVolume:   in.GetMinVolume(),
		MaxVolume:   in.GetMaxVolume(),
		SortBy:      in.GetSortBy(),
		SortDesc:    in.GetSortDesc(),
		Page:        int(in.GetPage()),
		PageSize:    int(in.GetPageSize()),
	}
	minS, err := parseDatePtr(in.GetMinSettlement())
	if err != nil {
		return nil, apperr.Validation("min_settlement: očekivan format YYYY-MM-DD")
	}
	input.MinSettlement = minS
	maxS, err := parseDatePtr(in.GetMaxSettlement())
	if err != nil {
		return nil, apperr.Validation("max_settlement: očekivan format YYYY-MM-DD")
	}
	input.MaxSettlement = maxS
	rows, total, err := s.Svc.ListSecurities(ctx, input)
	if err != nil {
		return nil, err
	}
	out := &tradingpb.ListSecuritiesResponse{Items: make([]*tradingpb.SecurityWithListing, 0, len(rows))}
	for _, r := range rows {
		out.Items = append(out.Items, securityWithListingToProto(r))
	}
	page := int(in.GetPage())
	if page < 1 {
		page = 1
	}
	pageSize := int(in.GetPageSize())
	if pageSize < 1 {
		pageSize = 50
	}
	out.Page = int32(page)
	out.PageSize = int32(pageSize)
	out.Total = total
	return out, nil
}

func (s *Server) GetOptionChain(ctx context.Context, in *tradingpb.GetOptionChainRequest) (*tradingpb.GetOptionChainResponse, error) {
	var settleTime *time.Time
	if t := in.GetSettlementDate(); t != nil {
		v := t.AsTime()
		settleTime = &v
	}
	groups, err := s.Svc.GetOptionChain(ctx, in.GetStockId(), settleTime, int(in.GetStrikesWindow()))
	if err != nil {
		return nil, err
	}
	out := &tradingpb.GetOptionChainResponse{}
	for _, g := range groups {
		group := &tradingpb.OptionChainGroup{
			SettlementDate: timestamppb.New(g.SettlementDate),
			SharedPrice:    g.SharedPrice,
		}
		for _, r := range g.Rows {
			row := &tradingpb.OptionChainRow{StrikePrice: r.StrikePrice}
			if r.Call != nil {
				row.Call = securityToProto(r.Call)
			}
			if r.Put != nil {
				row.Put = securityToProto(r.Put)
			}
			group.Rows = append(group.Rows, row)
		}
		out.Groups = append(out.Groups, group)
	}
	return out, nil
}

func securityToProto(sec *domain.Security) *tradingpb.Security {
	if sec == nil {
		return nil
	}
	out := &tradingpb.Security{
		Id:                   sec.ID,
		Ticker:               sec.Ticker,
		Name:                 sec.Name,
		Type:                 securityTypeToProto(sec.Type),
		ExchangeMic:          sec.ExchangeMIC,
		Currency:             currencyToProto(sec.Currency),
		OutstandingShares:    sec.OutstandingShares,
		DividendYield:        sec.DividendYield,
		ContractSize:         sec.ContractSize,
		ContractUnit:         sec.ContractUnit,
		BaseCurrency:         currencyToProto(sec.BaseCurrency),
		QuoteCurrency:        currencyToProto(sec.QuoteCurrency),
		Liquidity:            sec.Liquidity,
		UnderlyingSecurityId: sec.UnderlyingSecurityID,
		OptionType:           optionTypeToProto(sec.OptionType),
		StrikePrice:          sec.StrikePrice,
		ImpliedVolatility:    sec.ImpliedVolatility,
		Premium:              sec.Premium,
		OpenInterest:         sec.OpenInterest,
		CreatedAt:            timestamppb.New(sec.CreatedAt),
		UpdatedAt:            timestamppb.New(sec.UpdatedAt),
	}
	if sec.SettlementDate != nil {
		out.SettlementDate = timestamppb.New(*sec.SettlementDate)
	}
	return out
}

func securityWithListingToProto(r *service.SecurityWithListing) *tradingpb.SecurityWithListing {
	if r == nil {
		return nil
	}
	out := &tradingpb.SecurityWithListing{
		Security:           securityToProto(r.Security),
		MaintenanceMargin:  r.MaintenanceMargin,
		InitialMarginCost:  r.InitialMarginCost,
	}
	// market_cap lives on the proto Security message but is derived
	// per-listing in the service layer, so we stamp it onto the
	// already-built proto here rather than mutating the domain row.
	if out.Security != nil {
		out.Security.MarketCap = r.MarketCap
	}
	if r.Listing != nil {
		out.Listing = listingToProto(r.Listing)
	}
	return out
}

func securityTypeToProto(t domain.SecurityType) tradingpb.SecurityType {
	switch t {
	case domain.SecurityStock:
		return tradingpb.SecurityType_SECURITY_TYPE_STOCK
	case domain.SecurityFuture:
		return tradingpb.SecurityType_SECURITY_TYPE_FUTURE
	case domain.SecurityForex:
		return tradingpb.SecurityType_SECURITY_TYPE_FOREX
	case domain.SecurityOption:
		return tradingpb.SecurityType_SECURITY_TYPE_OPTION
	}
	return tradingpb.SecurityType_SECURITY_TYPE_UNSPECIFIED
}

func securityTypeFromProto(t tradingpb.SecurityType) domain.SecurityType {
	switch t {
	case tradingpb.SecurityType_SECURITY_TYPE_STOCK:
		return domain.SecurityStock
	case tradingpb.SecurityType_SECURITY_TYPE_FUTURE:
		return domain.SecurityFuture
	case tradingpb.SecurityType_SECURITY_TYPE_FOREX:
		return domain.SecurityForex
	case tradingpb.SecurityType_SECURITY_TYPE_OPTION:
		return domain.SecurityOption
	}
	return ""
}

func optionTypeToProto(t domain.OptionType) tradingpb.OptionType {
	switch t {
	case domain.OptionCall:
		return tradingpb.OptionType_OPTION_TYPE_CALL
	case domain.OptionPut:
		return tradingpb.OptionType_OPTION_TYPE_PUT
	}
	return tradingpb.OptionType_OPTION_TYPE_UNSPECIFIED
}

func optionTypeFromProto(t tradingpb.OptionType) domain.OptionType {
	switch t {
	case tradingpb.OptionType_OPTION_TYPE_CALL:
		return domain.OptionCall
	case tradingpb.OptionType_OPTION_TYPE_PUT:
		return domain.OptionPut
	}
	return ""
}

