// Investment-fund gRPC handlers (c4 PR3). Translate the proto surface
// to the service layer and back. Pattern mirrors server/otc.go.

package server

import (
	"context"

	tradingpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/trading/v1"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/service"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *Server) ListFunds(ctx context.Context, in *tradingpb.ListFundsRequest) (*tradingpb.ListFundsResponse, error) {
	rows, err := s.Svc.ListFunds(ctx, service.ListFundsInput{
		Status:                 in.GetStatus(),
		ManagerUserID:          in.GetManagerUserId(),
		MinContributionAtLeast: in.GetMinContributionAtLeast(),
		MinContributionAtMost:  in.GetMinContributionAtMost(),
		Sort:                   in.GetSort(),
		Order:                  in.GetOrder(),
	})
	if err != nil {
		return nil, err
	}
	out := &tradingpb.ListFundsResponse{Funds: make([]*tradingpb.Fund, 0, len(rows))}
	for _, d := range rows {
		out.Funds = append(out.Funds, decoratedFundToProto(d))
	}
	return out, nil
}

func (s *Server) GetFund(ctx context.Context, in *tradingpb.GetFundRequest) (*tradingpb.GetFundResponse, error) {
	res, err := s.Svc.GetFund(ctx, in.GetId())
	if err != nil {
		return nil, err
	}
	out := &tradingpb.GetFundResponse{
		Fund:     decoratedFundToProto(res.Fund),
		Holdings: make([]*tradingpb.FundHolding, 0, len(res.Holdings)),
	}
	for _, h := range res.Holdings {
		out.Holdings = append(out.Holdings, fundHoldingToProto(h))
	}
	if res.Position != nil {
		out.Position = fundPositionToProto(res.Position, res.Fund.Fund.Name, res.PositionSharePct, res.PositionValueRSD, res.PositionProfitRSD)
	}
	return out, nil
}

func (s *Server) CreateFund(ctx context.Context, in *tradingpb.CreateFundRequest) (*tradingpb.Fund, error) {
	f, err := s.Svc.CreateFund(ctx, service.CreateFundInput{
		Name:                in.GetName(),
		Description:         in.GetDescription(),
		MinimumContribution: in.GetMinimumContribution(),
		ManagerUserID:       in.GetManagerUserId(),
	})
	if err != nil {
		return nil, err
	}
	dec := &service.DecoratedFund{
		Fund:             f,
		LiquidRSD:        "0",
		HoldingsValueRSD: "0",
		TotalValueRSD:    "0",
		ProfitRSD:        "0",
		UnitPriceRSD:     "1",
	}
	return decoratedFundToProto(dec), nil
}

func (s *Server) InvestInFund(ctx context.Context, in *tradingpb.InvestInFundRequest) (*tradingpb.FundTransactionResponse, error) {
	res, err := s.Svc.InvestInFund(ctx, service.InvestInFundInput{
		FundID:           in.GetId(),
		AmountRSD:        in.GetAmountRsd(),
		SourceAccountID:  in.GetSourceAccountId(),
		OnBehalfClientID: in.GetOnBehalfClientId(),
	})
	if err != nil {
		return nil, err
	}
	return &tradingpb.FundTransactionResponse{
		Transaction: fundTxToProto(res.Transaction),
		SagaId:      res.SagaID,
		Pending:     res.Pending,
	}, nil
}

func (s *Server) WithdrawFromFund(ctx context.Context, in *tradingpb.WithdrawFromFundRequest) (*tradingpb.FundTransactionResponse, error) {
	res, err := s.Svc.WithdrawFromFund(ctx, service.WithdrawFromFundInput{
		FundID:           in.GetId(),
		AmountRSD:        in.GetAmountRsd(),
		DestAccountID:    in.GetDestAccountId(),
		OnBehalfClientID: in.GetOnBehalfClientId(),
		WithdrawAll:      in.GetWithdrawAll(),
	})
	if err != nil {
		return nil, err
	}
	return &tradingpb.FundTransactionResponse{
		Transaction: fundTxToProto(res.Transaction),
		SagaId:      res.SagaID,
		Pending:     res.Pending,
	}, nil
}

func (s *Server) ListFundPositions(ctx context.Context, in *tradingpb.ListFundPositionsRequest) (*tradingpb.ListFundPositionsResponse, error) {
	rows, err := s.Svc.ListFundPositions(ctx, service.ListFundPositionsInput{
		ClientID: in.GetClientId(),
		Status:   in.GetStatus(),
	})
	if err != nil {
		return nil, err
	}
	out := &tradingpb.ListFundPositionsResponse{
		Positions: make([]*tradingpb.FundPosition, 0, len(rows)),
	}
	for _, d := range rows {
		out.Positions = append(out.Positions, fundPositionToProto(
			d.Position, d.FundName, d.SharePct, d.CurrentValueRSD, d.ProfitRSD,
		))
	}
	return out, nil
}

func (s *Server) GetFundPerformance(ctx context.Context, in *tradingpb.GetFundPerformanceRequest) (*tradingpb.GetFundPerformanceResponse, error) {
	snaps, err := s.Svc.GetFundPerformance(ctx, in.GetId(), int(in.GetDays()))
	if err != nil {
		return nil, err
	}
	out := &tradingpb.GetFundPerformanceResponse{Snapshots: make([]*tradingpb.FundPerformanceSnapshot, 0, len(snaps))}
	for _, snap := range snaps {
		out.Snapshots = append(out.Snapshots, snapshotToProto(snap))
	}
	return out, nil
}

func (s *Server) ListFundTransactions(ctx context.Context, in *tradingpb.ListFundTransactionsRequest) (*tradingpb.ListFundTransactionsResponse, error) {
	rows, total, err := s.Svc.ListFundTransactions(ctx, service.ListFundTransactionsInput{
		FundID:   in.GetId(),
		ClientID: in.GetClientId(),
		Status:   in.GetStatus(),
		Page:     int(in.GetPage()),
		PageSize: int(in.GetPageSize()),
	})
	if err != nil {
		return nil, err
	}
	page := int(in.GetPage())
	if page < 1 {
		page = 1
	}
	pageSize := int(in.GetPageSize())
	if pageSize < 1 {
		pageSize = 50
	}
	out := &tradingpb.ListFundTransactionsResponse{
		Transactions: make([]*tradingpb.FundTransaction, 0, len(rows)),
		Page:         int32(page),
		PageSize:     int32(pageSize),
		Total:        total,
	}
	for _, t := range rows {
		out.Transactions = append(out.Transactions, fundTxToProto(t))
	}
	return out, nil
}

// =====================================================================
// proto conversion helpers
// =====================================================================

func decoratedFundToProto(d *service.DecoratedFund) *tradingpb.Fund {
	if d == nil || d.Fund == nil {
		return nil
	}
	f := d.Fund
	return &tradingpb.Fund{
		Id:                  f.ID,
		Name:                f.Name,
		Description:         f.Description,
		ManagerUserId:       f.ManagerUserID,
		ManagerDisplayName:  d.ManagerDisplayName,
		BankAccountId:       f.BankAccountID,
		BankAccountNumber:   d.BankAccountNumber,
		MinimumContribution: f.MinimumContribution,
		TotalUnits:          f.TotalUnits,
		LiquidRsd:           d.LiquidRSD,
		HoldingsValueRsd:    d.HoldingsValueRSD,
		TotalValueRsd:       d.TotalValueRSD,
		ProfitRsd:           d.ProfitRSD,
		UnitPriceRsd:        d.UnitPriceRSD,
		Status:              fundStatusToProto(f.Status),
		CreatedAt:           timestamppb.New(f.CreatedAt),
		UpdatedAt:           timestamppb.New(f.UpdatedAt),
	}
}

func fundPositionToProto(p *domain.FundPosition, fundName, share, value, profit string) *tradingpb.FundPosition {
	if p == nil {
		return nil
	}
	return &tradingpb.FundPosition{
		Id:               p.ID,
		FundId:           p.FundID,
		FundName:         fundName,
		ClientId:         p.ClientID,
		Units:            p.Units,
		TotalInvestedRsd: p.TotalInvestedRSD,
		CurrentValueRsd:  value,
		ProfitRsd:        profit,
		SharePct:         share,
		CreatedAt:        timestamppb.New(p.CreatedAt),
		UpdatedAt:        timestamppb.New(p.UpdatedAt),
	}
}

func fundHoldingToProto(v *service.HoldingView) *tradingpb.FundHolding {
	if v == nil || v.Holding == nil {
		return nil
	}
	out := &tradingpb.FundHolding{
		HoldingId:        v.Holding.ID,
		Quantity:         v.Holding.Quantity,
		WeightedAvgPrice: v.Holding.WeightedAvgPrice,
		CurrentPrice:     v.CurrentPrice,
		MarketValue:      v.MarketValue,
		ProfitNative:     v.ProfitNative,
		AcquiredAt:       timestamppb.New(v.Holding.AcquiredAt),
		UpdatedAt:        timestamppb.New(v.Holding.UpdatedAt),
	}
	if v.Security != nil {
		out.Security = securityToProto(v.Security)
		out.Currency = currencyToProto(v.Security.Currency)
	}
	return out
}

func fundTxToProto(t *domain.FundTransaction) *tradingpb.FundTransaction {
	if t == nil {
		return nil
	}
	return &tradingpb.FundTransaction{
		Id:                    t.ID,
		FundId:                t.FundID,
		ClientId:              t.ClientID,
		InitiatorEmployeeId:   t.InitiatorEmployeeID,
		AmountRsd:             t.AmountRSD,
		UnitsDelta:            t.UnitsDelta,
		SourceOrDestAccountId: t.SourceOrDestAccountID,
		IsInflow:              t.IsInflow,
		Status:                fundTxStatusToProto(t.Status),
		SagaId:                t.SagaID,
		FailureReason:         t.FailureReason,
		CreatedAt:             timestamppb.New(t.CreatedAt),
		UpdatedAt:             timestamppb.New(t.UpdatedAt),
	}
}

func snapshotToProto(s *domain.FundPerformanceSnapshot) *tradingpb.FundPerformanceSnapshot {
	if s == nil {
		return nil
	}
	// total_value_rsd = liquid + holdings (client convenience).
	return &tradingpb.FundPerformanceSnapshot{
		SnapshotAt:       timestamppb.New(s.SnapshotAt),
		LiquidRsd:        s.LiquidRSD,
		HoldingsValueRsd: s.HoldingsValueRSD,
		TotalValueRsd:    addDecimals(s.LiquidRSD, s.HoldingsValueRSD),
	}
}

func fundStatusToProto(s domain.FundStatus) tradingpb.FundStatus {
	switch s {
	case domain.FundActive:
		return tradingpb.FundStatus_FUND_STATUS_ACTIVE
	case domain.FundClosed:
		return tradingpb.FundStatus_FUND_STATUS_CLOSED
	}
	return tradingpb.FundStatus_FUND_STATUS_UNSPECIFIED
}

func fundTxStatusToProto(s domain.FundTransactionStatus) tradingpb.FundTransactionStatus {
	switch s {
	case domain.FundTxPending:
		return tradingpb.FundTransactionStatus_FUND_TX_STATUS_PENDING
	case domain.FundTxCompleted:
		return tradingpb.FundTransactionStatus_FUND_TX_STATUS_COMPLETED
	case domain.FundTxFailed:
		return tradingpb.FundTransactionStatus_FUND_TX_STATUS_FAILED
	}
	return tradingpb.FundTransactionStatus_FUND_TX_STATUS_UNSPECIFIED
}

// addDecimals returns the decimal sum a + b as a formatted string.
// Falls back to "0" on parse errors — snapshots are a non-critical
// read path.
func addDecimals(a, b string) string {
	pa, ea := money.Parse(a)
	if ea != nil {
		pa = money.MustParse("0")
	}
	pb, eb := money.Parse(b)
	if eb != nil {
		pb = money.MustParse("0")
	}
	return money.FormatAmount(money.Add(pa, pb))
}
