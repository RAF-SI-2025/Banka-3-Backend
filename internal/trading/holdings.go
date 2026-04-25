package trading

import (
	"errors"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/internal/bank"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// holdingAssetKey resolves an order's asset reference to the single FK that
// belongs on a holding row. Listings are resolved through to whichever of
// stock_id / future_id is non-null on the listing — the holding tracks the
// underlying security, not the listing pairing. Options and forex map
// directly. Returns a column name and value usable in WHERE / INSERT clauses.
func holdingAssetKey(tx *gorm.DB, o *Order) (string, int64, error) {
	switch {
	case o.OptionID != nil:
		return "option_id", *o.OptionID, nil
	case o.ForexPairID != nil:
		return "forex_pair_id", *o.ForexPairID, nil
	case o.ListingID != nil:
		var l Listing
		if err := tx.Select("stock_id, future_id").First(&l, *o.ListingID).Error; err != nil {
			return "", 0, status.Errorf(codes.Internal, "%v", err)
		}
		if l.StockID != nil {
			return "stock_id", *l.StockID, nil
		}
		if l.FutureID != nil {
			return "future_id", *l.FutureID, nil
		}
		return "", 0, status.Error(codes.Internal, "listing has no underlying")
	}
	return "", 0, status.Error(codes.Internal, "order has no asset reference")
}

// upsertHoldingOnBuy applies a buy-fill to the placer's position. New rows
// land with avg_cost = unitCostAccountCcy; existing rows roll the cost into
// a quantity-weighted average so the tax basis stays accurate across many
// fills at different prices. AccountID is rewritten to the buying account
// each time — sell proceeds always follow the most recent buy.
//
// unitCostAccountCcy is the per-unit price denominated in the booking
// account's currency (already FX-converted by the caller); chunk is the
// number of units this fill adds.
func upsertHoldingOnBuy(tx *gorm.DB, placerID int64, accountID int64, assetCol string, assetID int64, chunk, unitCostAccountCcy int64, now time.Time) error {
	if chunk <= 0 {
		return nil
	}

	var h Holding
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("placer_id = ? AND "+assetCol+" = ?", placerID, assetID).
		First(&h).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		row := map[string]any{
			"placer_id":     placerID,
			assetCol:        assetID,
			"account_id":    accountID,
			"amount":        chunk,
			"avg_cost":      unitCostAccountCcy,
			"public_amount": 0,
			"last_modified": now,
		}
		if err := tx.Table("holdings").Create(row).Error; err != nil {
			return status.Errorf(codes.Internal, "%v", err)
		}
		return nil
	}
	if err != nil {
		return status.Errorf(codes.Internal, "%v", err)
	}

	newAmount := h.Amount + chunk
	// Weighted average: stays at unitCost when the previous holding was empty
	// (avg_cost on a zero-amount holding is meaningless), otherwise blends.
	var newAvg int64
	if h.Amount <= 0 {
		newAvg = unitCostAccountCcy
	} else {
		newAvg = (h.AvgCost*h.Amount + unitCostAccountCcy*chunk) / newAmount
	}

	updates := map[string]any{
		"amount":        newAmount,
		"avg_cost":      newAvg,
		"account_id":    accountID,
		"last_modified": now,
	}
	if err := tx.Model(&Holding{}).Where("id = ?", h.ID).Updates(updates).Error; err != nil {
		return status.Errorf(codes.Internal, "%v", err)
	}
	return nil
}

// deductHoldingOnSell pulls `chunk` units out of the placer's holding for the
// asset. Locks the row FOR UPDATE so concurrent fills on the same holding
// serialize. Returns FailedPrecondition when the position is missing or
// insufficient — the executor backs off and retries on the next tick, which
// is the same contract market/limit fills already use for failed settlements.
//
// public_amount tracks the OTC-discoverable share count and must never exceed
// amount; if a sell drops amount below the current public_amount, the public
// counter is clamped down so the invariant holds.
//
// The returned Holding is the pre-deduction snapshot — avg_cost and
// account_id at the moment of sale, which capital_gains.go needs to compute
// the tax row (#208). nil is returned only alongside a non-nil error.
func deductHoldingOnSell(tx *gorm.DB, placerID int64, assetCol string, assetID int64, chunk int64, now time.Time) (*Holding, error) {
	if chunk <= 0 {
		return nil, nil
	}

	var h Holding
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("placer_id = ? AND "+assetCol+" = ?", placerID, assetID).
		First(&h).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, status.Error(codes.FailedPrecondition, "no holding for sell-side fill")
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	if h.Amount < chunk {
		return nil, status.Error(codes.FailedPrecondition, "insufficient holding for sell-side fill")
	}

	newAmount := h.Amount - chunk
	updates := map[string]any{
		"amount":        newAmount,
		"last_modified": now,
	}
	if h.PublicAmount > newAmount {
		updates["public_amount"] = newAmount
	}
	if err := tx.Model(&Holding{}).Where("id = ?", h.ID).Updates(updates).Error; err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	return &h, nil
}

// findOrCreatePlacer returns the order_placers.id for the given identity,
// creating the row on first use. Holdings (#207) are keyed by placer_id, so
// the row has to be stable across orders for the same person — the partial
// unique indexes on (client_id) / (employee_id) make that safe.
//
// Exactly one of clientID / employeeID must be non-nil; the schema CHECK
// constraint enforces this on the row, the precondition lets us short-circuit
// before hitting the DB.
func findOrCreatePlacer(tx *gorm.DB, clientID, employeeID *int64) (int64, error) {
	if (clientID == nil) == (employeeID == nil) {
		return 0, status.Error(codes.Internal, "placer must reference exactly one of client / employee")
	}

	var existing int64
	q := tx.Table("order_placers").Select("id")
	if clientID != nil {
		q = q.Where("client_id = ?", *clientID)
	} else {
		q = q.Where("employee_id = ?", *employeeID)
	}
	err := q.Take(&existing).Error
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, status.Errorf(codes.Internal, "%v", err)
	}

	placer := OrderPlacer{ClientID: clientID, EmployeeID: employeeID}
	if err := tx.Create(&placer).Error; err != nil {
		return 0, status.Errorf(codes.Internal, "%v", err)
	}
	return placer.ID, nil
}

// resolvePlacerForCaller returns the placer row for the caller, creating it
// if needed. Used by RPCs that have to look up holdings (#207) for callers
// who haven't placed any orders yet — without this, ListHoldings on a fresh
// client would 404 even though "no orders" is a perfectly valid state.
func resolvePlacerForCaller(tx *gorm.DB, caller *bank.CallerIdentity) (int64, error) {
	if caller.IsClient {
		id := caller.ClientID
		return findOrCreatePlacer(tx, &id, nil)
	}
	if caller.IsEmployee {
		var empID int64
		err := tx.Table("employees").Select("id").Where("email = ?", caller.Email).Take(&empID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return 0, status.Error(codes.NotFound, "employee not found")
			}
			return 0, status.Errorf(codes.Internal, "%v", err)
		}
		return findOrCreatePlacer(tx, nil, &empID)
	}
	return 0, status.Error(codes.PermissionDenied, "caller is neither client nor employee")
}

// holdingAccountID looks up the placer's accounts.id for an account number.
// Used by the executor on buy-fills to record the booking account on the
// holding row (orders carry the number, holdings keep the id for tax joins).
func holdingAccountID(tx *gorm.DB, accountNumber string) (int64, error) {
	var id int64
	err := tx.Table("accounts").Select("id").Where("number = ?", accountNumber).Take(&id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, status.Error(codes.NotFound, "account not found")
		}
		return 0, status.Errorf(codes.Internal, "%v", err)
	}
	return id, nil
}
