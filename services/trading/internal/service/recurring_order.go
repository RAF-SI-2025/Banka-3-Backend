package service

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/schedule"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
)

// CreateRecurringOrderInput is the create surface for a "Trajni nalog" /
// DCA recurring order (todoSpec C3 S47-S53).
type CreateRecurringOrderInput struct {
	SecurityID string
	Mode       domain.RecurringMode
	// AmountRSD is the per-cycle RSD notional (BYAMOUNT, S47).
	AmountRSD string
	// Quantity is the per-cycle share count (BYQUANTITY, S48).
	Quantity  int32
	AccountID string
	Cadence   schedule.Cadence
	// StartDate, when set in the future, anchors the first NextRun; the
	// FE may leave it empty in which case the first run is scheduled one
	// cadence interval from now.
	StartDate string
}

// CreateRecurringOrder registers a recurring Market BUY for the caller
// (S47/S48). It validates the mode-specific sizing field and the
// cadence, then sets the initial NextRun to the chosen future start (or
// one cadence interval ahead). The row is Active=true with NextRun set
// per spec ("NextRun set to next execution date").
func (s *Service) CreateRecurringOrder(ctx context.Context, in CreateRecurringOrderInput) (*domain.RecurringOrder, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	// Only recognised traders may schedule a recurring order — same gate
	// as a one-off order (S47 is a client/agent flow).
	if err := s.assertTraderRole(p); err != nil {
		return nil, err
	}
	if in.SecurityID == "" {
		return nil, apperr.Validation("security_id je obavezan")
	}
	if in.AccountID == "" {
		return nil, apperr.Validation("account_id je obavezan")
	}
	if !in.Mode.Valid() {
		return nil, apperr.Validation("mode mora biti BYAMOUNT ili BYQUANTITY")
	}
	if !in.Cadence.IsRecurring() {
		// DCA never schedules a ONCE row; only DAILY/WEEKLY/MONTHLY.
		return nil, apperr.Validation("učestalost mora biti DAILY, WEEKLY ili MONTHLY")
	}

	var amountRSD string
	var qty int32
	switch in.Mode {
	case domain.RecurringByAmount:
		a, perr := money.Parse(in.AmountRSD)
		if perr != nil {
			return nil, apperr.Validation("neispravan iznos")
		}
		if !money.IsPositive(a) {
			return nil, apperr.Validation("iznos mora biti veći od nule")
		}
		amountRSD = money.FormatAmount(a)
	case domain.RecurringByQuantity:
		if in.Quantity <= 0 {
			return nil, apperr.Validation("količina mora biti pozitivna")
		}
		qty = in.Quantity
	}

	// Confirm the security exists + the caller may trade it, so the cron
	// never wakes on a dangling/forbidden reference (S47). Clients can't
	// recur forex/options (spec p.58), same as one-off orders.
	sec, err := s.Store.GetSecurity(ctx, in.SecurityID)
	if err != nil {
		return nil, err
	}
	if p.UserKind == auth.KindClient {
		switch sec.Type {
		case domain.SecurityForex, domain.SecurityOption:
			return nil, apperr.PermissionDenied("klijenti ne mogu da trguju forex parovima ili opcijama")
		}
	}

	now := s.now()
	nextRun := schedule.Advance(now, in.Cadence)
	if in.StartDate != "" {
		start, perr := time.Parse(time.RFC3339, in.StartDate)
		if perr != nil {
			return nil, apperr.Validation("neispravan datum početka")
		}
		if !start.After(now) {
			return nil, apperr.Validation("datum početka mora biti u budućnosti")
		}
		nextRun = start
	}

	return s.Store.InsertRecurringOrder(ctx, &domain.RecurringOrder{
		UserID:     p.UserID,
		UserKind:   domain.UserKind(p.UserKind),
		SecurityID: in.SecurityID,
		Direction:  domain.DirectionBuy,
		Mode:       in.Mode,
		AmountRSD:  amountRSD,
		Quantity:   qty,
		AccountID:  in.AccountID,
		Cadence:    string(in.Cadence),
		NextRun:    nextRun,
	})
}

// ListRecurringOrders returns the caller's own recurring orders (active
// + paused), newest first.
func (s *Service) ListRecurringOrders(ctx context.Context) ([]*domain.RecurringOrder, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	return s.Store.ListRecurringOrdersByUser(ctx, p.UserID)
}

// PauseRecurringOrder flips Active=false so the cron skips the row (S51).
// Owner-scoped (admin may also act).
func (s *Service) PauseRecurringOrder(ctx context.Context, id string) (*domain.RecurringOrder, error) {
	return s.setRecurringActive(ctx, id, false)
}

// ResumeRecurringOrder flips Active=true so the cron picks the row back
// up. Owner-scoped (admin may also act).
func (s *Service) ResumeRecurringOrder(ctx context.Context, id string) (*domain.RecurringOrder, error) {
	return s.setRecurringActive(ctx, id, true)
}

func (s *Service) setRecurringActive(ctx context.Context, id string, active bool) (*domain.RecurringOrder, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	r, err := s.Store.GetRecurringOrder(ctx, id)
	if err != nil {
		return nil, err
	}
	if r.UserID != p.UserID && !permissions.Has(p.Permissions, permissions.Admin) {
		return nil, apperr.PermissionDenied("nedovoljne permisije")
	}
	if err := s.Store.SetRecurringOrderActive(ctx, id, active); err != nil {
		return nil, err
	}
	r.Active = active
	return r, nil
}

// CancelRecurringOrder deletes a recurring order permanently so it
// disappears from the active list (S52). Owner-scoped (admin may also
// act).
func (s *Service) CancelRecurringOrder(ctx context.Context, id string) error {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return err
	}
	r, err := s.Store.GetRecurringOrder(ctx, id)
	if err != nil {
		return err
	}
	if r.UserID != p.UserID && !permissions.Has(p.Permissions, permissions.Admin) {
		return apperr.PermissionDenied("nedovoljne permisije")
	}
	return s.Store.DeleteRecurringOrder(ctx, id)
}

// RunRecurringOrders is the DCA cron entrypoint (S49). For each due +
// active row it creates one Market BUY via the regular CreateOrder path
// (so an actuary over their daily limit naturally routes to supervisor
// approval — S53) under a principal scoped to the row's owner, then
// advances NextRun by the cadence. On insufficient funds it skips the
// order, notifies the client (in-app; email best-effort), and still
// advances NextRun (S50). Returns the number of orders successfully
// created (skips don't count).
//
// Advancement is unconditional per row: every DCA cadence is recurring,
// so schedule.AfterRun always returns a new NextRun and never
// deactivates here (deactivation is reserved for cancel/pause).
func (s *Service) RunRecurringOrders(ctx context.Context) (int, error) {
	now := s.now()
	rows, err := s.Store.ListDueRecurringOrders(ctx, now)
	if err != nil {
		return 0, err
	}
	created := 0
	for _, r := range rows {
		placed := s.runOneRecurringOrder(ctx, r)
		if placed {
			created++
		}
		// Advance NextRun whether the order was placed or skipped (S49/S50).
		cad := schedule.Cadence(r.Cadence)
		next, _ := schedule.AfterRun(r.NextRun, cad, now)
		if err := s.Store.UpdateRecurringOrderNextRun(ctx, r.ID, next); err != nil {
			s.Log.Warn("recurring order: next_run advance failed",
				"recurring_order_id", r.ID, "err", err.Error())
		}
	}
	return created, nil
}

// runOneRecurringOrder creates the Market BUY for one due row. Returns
// true when an order was created, false when it was skipped (e.g.
// insufficient funds — S50) or errored. Never returns an error: the
// caller advances NextRun regardless.
func (s *Service) runOneRecurringOrder(ctx context.Context, r *domain.RecurringOrder) bool {
	qty, err := s.recurringOrderQuantity(ctx, r)
	if err != nil {
		// BYAMOUNT couldn't size a whole share at the current price, or a
		// price lookup failed — treat as a skip and notify so the client
		// understands nothing happened this cycle.
		s.Log.Warn("recurring order: sizing failed; skipping cycle",
			"recurring_order_id", r.ID, "err", err.Error())
		s.notifyRecurringSkip(ctx, r, "nije bilo moguće odrediti količinu za ovaj ciklus")
		return false
	}

	owner, err := s.recurringOwnerPrincipal(ctx, r)
	if err != nil {
		s.Log.Warn("recurring order: owner principal resolution failed; skipping",
			"recurring_order_id", r.ID, "err", err.Error())
		s.notifyRecurringSkip(ctx, r, "nalog nije mogao biti izvršen ovog ciklusa")
		return false
	}
	ownerCtx := auth.WithPrincipal(ctx, owner)

	_, err = s.CreateOrder(ownerCtx, CreateOrderInput{
		SecurityID: r.SecurityID,
		OrderType:  domain.OrderMarket,
		Direction:  domain.DirectionBuy,
		Quantity:   qty,
		AccountID:  r.AccountID,
	})
	if err != nil {
		if isInsufficientFunds(err) {
			// S50: skip + notify, still advance (handled by caller).
			s.notifyRecurringSkip(ctx, r, "nedovoljno sredstava na računu — kupovina je preskočena")
			return false
		}
		// Other errors (validation, market closed handled inside, etc.)
		// also skip this cycle but are logged louder.
		s.Log.Warn("recurring order: CreateOrder failed; skipping cycle",
			"recurring_order_id", r.ID, "err", err.Error())
		s.notifyRecurringSkip(ctx, r, "nalog nije mogao biti izvršen ovog ciklusa")
		return false
	}
	return true
}

// recurringOrderQuantity returns the share quantity to BUY this cycle.
// BYQUANTITY returns the configured fixed count; BYAMOUNT divides the
// configured RSD notional by the security's per-share RSD price and
// floors to whole shares. Errors when BYAMOUNT can't afford one whole
// share at the current price (caller treats as a skip).
func (s *Service) recurringOrderQuantity(ctx context.Context, r *domain.RecurringOrder) (int32, error) {
	if r.Mode == domain.RecurringByQuantity {
		if r.Quantity <= 0 {
			return 0, apperr.Validation("količina mora biti pozitivna")
		}
		return r.Quantity, nil
	}
	// BYAMOUNT: qty = floor(amount_rsd / per_share_rsd).
	sec, err := s.Store.GetSecurity(ctx, r.SecurityID)
	if err != nil {
		return 0, err
	}
	listing, err := s.Store.GetListingBySecurityID(ctx, r.SecurityID)
	if err != nil {
		return 0, err
	}
	contractSize := listing.ContractSize
	if contractSize == "" {
		contractSize = "1"
	}
	// per-share notional in the security currency = price × contract_size.
	perShareNative, err := s.tradeValueRSD(ctx, sec, 1, listing.Price, contractSize)
	if err != nil {
		return 0, err
	}
	if perShareNative.Sign() <= 0 {
		return 0, apperr.FailedPrecondition("cena hartije je nepoznata")
	}
	amount, err := money.Parse(r.AmountRSD)
	if err != nil {
		return 0, apperr.Internal("recurring amount unparseable", err)
	}
	ratio, err := money.Div(amount, perShareNative)
	if err != nil {
		return 0, apperr.Internal("recurring qty division failed", err)
	}
	// Floor to a whole share.
	whole := new(big.Int).Quo(ratio.Num(), ratio.Denom())
	if whole.Sign() <= 0 {
		return 0, apperr.FailedPrecondition("iznos je premali za kupovinu jedne hartije")
	}
	if !whole.IsInt64() || whole.Int64() > (1<<31-1) {
		return 0, apperr.Validation("količina prevazilazi dozvoljeni maksimum")
	}
	return int32(whole.Int64()), nil
}

// recurringOwnerPrincipal builds the principal the cron re-enters
// CreateOrder under, scoped to the recurring order's owner. The owner's
// permissions are what drive the approval routing (S53: an agent over
// their daily limit goes to supervisor approval, exactly as a manual
// order would). Employee permissions come from the user service; clients
// get the standard trading permission. Falls back gracefully when no
// UserResolver is wired on a minimal dev stack.
func (s *Service) recurringOwnerPrincipal(ctx context.Context, r *domain.RecurringOrder) (auth.Principal, error) {
	p := auth.Principal{UserID: r.UserID, UserKind: auth.UserKind(r.UserKind)}
	switch r.UserKind {
	case domain.KindClient:
		p.Permissions = []string{permissions.TradingClient}
	case domain.KindEmployee:
		if s.Users == nil {
			return auth.Principal{}, apperr.FailedPrecondition("user resolver nije konfigurisan")
		}
		perms, err := s.Users.EmployeePermissions(ctx, r.UserID)
		if err != nil {
			return auth.Principal{}, err
		}
		p.Permissions = perms
	default:
		return auth.Principal{}, apperr.FailedPrecondition("nepodržan tip vlasnika trajnog naloga")
	}
	return p, nil
}

// notifyRecurringSkip tells the owner their DCA cycle didn't place an
// order (S50). In-app always; email best-effort (only when an address
// resolver is wired — this stack has none on the trading side, so email
// is skipped). Never returns an error.
func (s *Service) notifyRecurringSkip(ctx context.Context, r *domain.RecurringOrder, reason string) {
	if s.Notifier == nil {
		return
	}
	ticker := r.SecurityID
	if sec, err := s.Store.GetSecurity(ctx, r.SecurityID); err == nil && sec.Ticker != "" {
		ticker = sec.Ticker
	}
	title := "Trajni nalog preskočen"
	body := fmt.Sprintf("Trajni nalog za kupovinu hartije %s nije izvršen: %s.", ticker, reason)
	if err := s.Notifier.InApp(ctx, r.UserID, r.UserKind, "recurring_order", title, body); err != nil {
		s.Log.Warn("recurring order: in-app notify failed",
			"recurring_order_id", r.ID, "err", err.Error())
	}
	// Email is best-effort and only meaningful when the recipient address
	// is resolvable; the trading service has no user-email resolver wired,
	// so we skip it here (same posture as price-alert notifications).
}

// isInsufficientFunds reports whether err is the order-path's
// "nedovoljna sredstva" rejection (S50). The pre-fill funds check in
// CreateOrder returns a FailedPrecondition with this message; we match
// the message rather than every FailedPrecondition so genuine config
// problems (e.g. unconfigured actuary) aren't silently swallowed as a
// skip.
func isInsufficientFunds(err error) bool {
	var ae *apperr.Error
	if !errors.As(err, &ae) {
		return false
	}
	if ae.Kind != apperr.KindFailedPrecondition {
		return false
	}
	msg := strings.ToLower(ae.Message)
	return strings.Contains(msg, "nedovoljna sredstva") ||
		strings.Contains(msg, "nedovoljno sredstava")
}
