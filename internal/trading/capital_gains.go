package trading

import (
	"math"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

// Capital-gains tax (#208, spec pp.63–64). Only stock sales are taxed in
// this iteration — futures/forex/options have their own lifecycles the spec
// doesn't cover under "porez na kapitalnu dobit", and exercising an option
// (#207) already pays out at intrinsic, not at a realized-sale event.
const (
	// capitalGainsTaxPermille is 15% expressed as integer permille so the
	// math sits with the rest of commission.go's permille arithmetic and
	// avoids float rounding on the dinar side.
	capitalGainsTaxPermille int64 = 150
)

// CapitalGain is one tax-owing row per sell-fill that realized positive dobit
// against the holding's blended avg_cost. RealizedProfit is in the booking
// account's currency; TaxDue is the 15% cut converted to RSD at the sale-day
// rate (spec Napomena 2 on p.63: država ima samo RSD račun, so the receipt
// currency has to be RSD regardless of where the gain was realized).
//
// PaidAt stays NULL until the monthly tax-collection cron (#209) debits the
// account and transfers to the state company; that flow is the next issue —
// this file only writes rows.
type CapitalGain struct {
	ID             int64      `gorm:"column:id;primaryKey"`
	SellerPlacerID int64      `gorm:"column:seller_placer_id;not null"`
	AccountID      int64      `gorm:"column:account_id;not null"`
	OrderFillID    *int64     `gorm:"column:order_fill_id"`
	RealizedProfit int64      `gorm:"column:realized_profit;not null"`
	TaxDue         int64      `gorm:"column:tax_due;not null"`
	Period         string     `gorm:"column:period;type:char(7);not null"`
	PaidAt         *time.Time `gorm:"column:paid_at"`
	CreatedAt      time.Time  `gorm:"column:created_at;not null;autoCreateTime"`
}

func (CapitalGain) TableName() string { return "capital_gains" }

// capitalGainsPeriod formats `now` as the monthly bucket the tax spec uses
// (YYYY-MM). Kept separate so tests can pin it without a Server dependency.
func capitalGainsPeriod(now time.Time) string {
	return now.Format("2006-01")
}

// computeCapitalGainsTax returns the tax owed on a single sell-fill, both in
// the booking account's currency and converted to RSD. rateAccRSD is the
// RSD-per-unit rate for the account currency at sale-day; callers pass 1.0
// when the account is already in RSD. A zero or negative profit returns
// (0, 0) — no dobit, no tax (spec p.62 "u slučaju gubitka").
//
// Rounding is up for tax_acc_ccy so rounding never under-taxes the placer;
// the RSD conversion then uses bankers' rounding via math.Round so a foreign
// dobit doesn't drift away from its RSD equivalent over time.
func computeCapitalGainsTax(profitAccCcy int64, rateAccRSD float64) (int64, int64) {
	if profitAccCcy <= 0 {
		return 0, 0
	}
	taxAcc := (profitAccCcy*capitalGainsTaxPermille + 999) / 1000
	taxRSD := int64(math.Round(float64(taxAcc) * rateAccRSD))
	return taxAcc, taxRSD
}

// recordCapitalGain writes a capital_gains row for a stock sell-fill when the
// sale realized positive dobit against the holding's pre-deduction avg_cost.
// Non-stock holdings and loss fills are no-ops.
//
// proceedsAccCcy is the gross sale proceeds in the booking account's currency
// (settle.accAmount from executor.go); snapshot is the holding as it stood
// before deductHoldingOnSell ran, so avg_cost and account_id reflect the
// moment of sale rather than any post-deduction re-averaging.
func recordCapitalGain(
	tx *gorm.DB,
	snapshot *Holding,
	orderFillID int64,
	chunk int64,
	proceedsAccCcy int64,
	rateAccRSD float64,
	now time.Time,
) error {
	if snapshot == nil || snapshot.StockID == nil {
		return nil
	}
	if chunk <= 0 {
		return nil
	}
	costBasis := snapshot.AvgCost * chunk
	profit := proceedsAccCcy - costBasis
	if profit <= 0 {
		return nil
	}

	_, taxRSD := computeCapitalGainsTax(profit, rateAccRSD)
	fillID := orderFillID
	row := CapitalGain{
		SellerPlacerID: snapshot.PlacerID,
		AccountID:      snapshot.AccountID,
		OrderFillID:    &fillID,
		RealizedProfit: profit,
		TaxDue:         taxRSD,
		Period:         capitalGainsPeriod(now),
	}
	if err := tx.Create(&row).Error; err != nil {
		return status.Errorf(codes.Internal, "%v", err)
	}
	return nil
}
