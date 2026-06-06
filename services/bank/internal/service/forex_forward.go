// Forex forwards (terminski valutni ugovori, todoSpec C3).
//
// A forex forward is a contract between a client and the bank that fixes
// TODAY a rate for a FUTURE currency conversion. The forward rate is
//
//   ForwardRate = SpotAskRate × (1 + (DaysToSettlement / 365) × SpreadFactor)
//
// where SpotAskRate is the spot ASK for base→RSD (spec p.26 always-ASK
// policy, reused via the menjačnica rate provider) and SpreadFactor is a
// per-pair bank parameter set by supervisors.
//
// At conclusion the bank RESERVES the quote-currency obligation
// (ForwardRate × Notional, in RSD) on the client's RSD account through
// the existing reservation primitive and charges a commission. On the
// settlement date the bank performs a DIRECT fixed-rate conversion: debit
// RSD (ForwardRate × Notional) from the client's RSD account and credit
// Notional in the base currency to the client's base-currency account.
// This is NOT a menjačnica RSD round-trip — the spread risk is already
// priced into ForwardRate — so the conversion runs commission-free at the
// locked rate, hopping through the bank's per-currency house accounts.

package service

import (
	"context"
	"errors"
	"math/big"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/money"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/schedule"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// defaultForexForwardSpread is the fallback annualised spread factor (2%)
// used when a supervisor hasn't configured the pair yet.
const defaultForexForwardSpread = "0.02"

// daysInYear is the day-count basis in the forward-rate formula.
const daysInYear = 365

// computeForwardRate applies the spec forward-rate formula:
//
//	ForwardRate = SpotAskRate × (1 + (DaysToSettlement / 365) × SpreadFactor)
//
// Pure function over *big.Rat so it's unit-testable with an exact worked
// example. spotAsk and spreadFactor are the spot ASK (quote per 1 base)
// and the annualised spread factor; days is the calendar days to
// settlement.
func computeForwardRate(spotAsk, spreadFactor *big.Rat, days int) *big.Rat {
	// fraction = days / 365
	fraction := new(big.Rat).SetFrac64(int64(days), daysInYear)
	// factor = 1 + fraction × spreadFactor
	factor := money.Add(money.MustParse("1"), money.Mul(fraction, spreadFactor))
	return money.Mul(spotAsk, factor)
}

// daysToSettlement returns the whole calendar days from now to the
// settlement date, rounded up so a same-day-plus-a-bit settlement counts
// as at least one day. Caller guarantees settlement is strictly future.
func daysToSettlement(now, settlement time.Time) int {
	d := settlement.Sub(now)
	days := int(d.Hours() / 24)
	if time.Duration(days)*24*time.Hour < d {
		days++
	}
	if days < 1 {
		days = 1
	}
	return days
}

// ForexForwardQuote is the resolved preview of a prospective forward.
type ForexForwardQuote struct {
	BaseCurrency     domain.Currency
	QuoteCurrency    domain.Currency
	Notional         string
	SpotAskRate      string
	SpreadFactor     string
	DaysToSettlement int
	ForwardRate      string
	QuoteAmount      string // forward_rate × notional, in quote currency (reserved at conclusion)
	Commission       string // in quote currency
}

// quoteCurrency is the settlement leg currency for every forward — the
// bank only holds an RSD account for the obligation leg, mirroring the
// capital-gains-tax model.
const forwardQuoteCurrency = domain.CurrencyRSD

// resolveSpreadFactor returns the configured SpreadFactor for the pair,
// or the default when unset.
func (s *Service) resolveSpreadFactor(ctx context.Context, base, quote domain.Currency) (*big.Rat, string, error) {
	sp, err := s.Store.GetForexForwardSpread(ctx, base, quote)
	if err != nil {
		if apperrIs(err, apperr.KindNotFound) {
			return money.MustParse(defaultForexForwardSpread), defaultForexForwardSpread, nil
		}
		return nil, "", err
	}
	r, perr := money.Parse(sp.SpreadFactor)
	if perr != nil {
		return nil, "", apperr.Internal("parse spread factor", perr)
	}
	return r, sp.SpreadFactor, nil
}

// quoteForward computes the forward rate + amounts without side effects.
// Shared by QuoteForexForward (preview) and CreateForexForward.
func (s *Service) quoteForward(ctx context.Context, base, quote domain.Currency, notional string, settlement time.Time) (*ForexForwardQuote, *big.Rat, error) {
	if !base.Supported() || !quote.Supported() {
		return nil, nil, apperr.Validation("unsupported currency")
	}
	if quote != forwardQuoteCurrency {
		return nil, nil, apperr.Validation("terminski ugovor se poravnava u RSD")
	}
	if base == quote {
		return nil, nil, apperr.Validation("valute terminskog ugovora moraju biti različite")
	}
	notionalR, err := parsePositive(notional)
	if err != nil {
		return nil, nil, err
	}
	if err := schedule.ValidateFuture(settlement, s.now()); err != nil {
		return nil, nil, apperr.Validation(err.Error())
	}
	if s.Rates == nil {
		return nil, nil, apperr.Internal("exchange rate provider not configured", nil)
	}

	days := daysToSettlement(s.now(), settlement)

	// Spot ASK for base→RSD per spec p.26 (always sell-side).
	_, ask, err := s.Rates.Quote(ctx, base, quote)
	if err != nil {
		return nil, nil, err
	}
	spotAsk, perr := money.Parse(ask)
	if perr != nil {
		return nil, nil, apperr.Internal("parse spot ask", perr)
	}
	if !money.IsPositive(spotAsk) {
		return nil, nil, apperr.Internal("spot ask non-positive", nil)
	}

	spreadFactor, spreadStr, err := s.resolveSpreadFactor(ctx, base, quote)
	if err != nil {
		return nil, nil, err
	}

	forwardRate := computeForwardRate(spotAsk, spreadFactor, days)
	quoteAmount := money.Mul(forwardRate, notionalR)
	commission := money.Mul(quoteAmount, s.commissionRate())

	return &ForexForwardQuote{
		BaseCurrency:     base,
		QuoteCurrency:    quote,
		Notional:         money.FormatAmount(notionalR),
		SpotAskRate:      money.FormatRate(spotAsk),
		SpreadFactor:     spreadStr,
		DaysToSettlement: days,
		ForwardRate:      money.FormatRate(forwardRate),
		QuoteAmount:      money.FormatAmount(quoteAmount),
		Commission:       money.FormatAmount(commission),
	}, quoteAmount, nil
}

// QuoteForexForward returns a side-effect-free preview of a prospective
// forward. Available to any payment-capable principal.
func (s *Service) QuoteForexForward(ctx context.Context, base, quote domain.Currency, notional string, settlement time.Time) (*ForexForwardQuote, error) {
	if err := s.requirePermission(ctx, permissions.PaymentWrite); err != nil {
		return nil, err
	}
	q, _, err := s.quoteForward(ctx, base, quote, notional, settlement)
	return q, err
}

// CreateForexForwardInput is the validated payload for concluding a
// forward.
type CreateForexForwardInput struct {
	BaseCurrency   domain.Currency
	Notional       string
	SettlementDate time.Time
}

// CreateForexForward locks the forward rate, reserves the quote-currency
// obligation (forward_rate × notional) on the client's RSD account via
// the existing reservation primitive, charges the commission, and
// persists the contract as 'active'. The client must hold both an RSD
// account (the obligation/settlement leg) and a base-currency account
// (credited at settlement).
func (s *Service) CreateForexForward(ctx context.Context, in CreateForexForwardInput) (*domain.ForexForward, error) {
	if err := s.requirePermission(ctx, permissions.PaymentWrite); err != nil {
		return nil, err
	}
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if p.UserKind != auth.KindClient {
		return nil, apperr.Validation("terminski ugovor mogu zaključiti samo klijenti")
	}

	quote := forwardQuoteCurrency
	preview, quoteAmount, err := s.quoteForward(ctx, in.BaseCurrency, quote, in.Notional, in.SettlementDate)
	if err != nil {
		return nil, err
	}
	commission := money.Mul(quoteAmount, s.commissionRate())

	// Resolve the client's RSD (obligation) account and base-currency
	// (credit) account.
	rsdAcc, err := s.clientAccountByCurrency(ctx, p.UserID, quote)
	if err != nil {
		return nil, err
	}
	baseAcc, err := s.clientAccountByCurrency(ctx, p.UserID, in.BaseCurrency)
	if err != nil {
		return nil, err
	}

	// Reserve the obligation amount on the RSD account. The reservation
	// primitive debits available_balance now; the settlement sweep commits
	// it (debits balance) at the fixed rate. Reservation is internal-only,
	// so present an admin-flavoured outgoing principal.
	opID := uuid.NewString()
	reserveCtx := s.internalCtx(ctx)
	if _, err := s.ReserveFunds(reserveCtx, ReserveFundsInput{
		AccountID: rsdAcc.ID,
		Amount:    money.FormatAmount(quoteAmount),
		Currency:  quote,
		OpID:      opID,
		OpKind:    string(domain.TxKindForexForward),
	}); err != nil {
		return nil, err
	}

	// Charge the commission from the client's RSD account into the bank's
	// RSD house account, then persist the contract — all atomic, so a
	// failed insert rolls back the commission. (The reservation already
	// committed available_balance; if the insert fails below we release
	// it.)
	var out *domain.ForexForward
	err = s.Store.ExecuteAtomic(ctx, func(tx pgx.Tx) error {
		if money.IsPositive(commission) {
			house, herr := s.Store.GetSystemAccount(ctx, quote)
			if herr != nil {
				return herr
			}
			negComm := money.FormatAmount(money.Sub(money.MustParse("0"), commission))
			if cerr := s.Store.CheckLimits(ctx, tx, rsdAcc.ID, money.FormatAmount(commission)); cerr != nil {
				return cerr
			}
			if cerr := s.Store.AdjustBalance(ctx, tx, rsdAcc.ID, negComm); cerr != nil {
				return cerr
			}
			if cerr := s.Store.AdjustBalance(ctx, tx, house.ID, money.FormatAmount(commission)); cerr != nil {
				return cerr
			}
			if _, cerr := s.Store.InsertTransaction(ctx, tx, &domain.Transaction{
				OpID: opID, Kind: domain.TxKindFee, LegIndex: 100,
				FromAccountID: rsdAcc.ID, ToAccountID: house.ID,
				FromAmount: money.FormatAmount(commission), ToAmount: money.FormatAmount(commission),
				Purpose:           "Provizija za terminski ugovor",
				InitiatorClientID: p.UserID,
				Status:            domain.TxStatusRealized,
			}); cerr != nil {
				return cerr
			}
		}
		row, ierr := s.Store.InsertForexForward(ctx, tx, &domain.ForexForward{
			ClientID:         p.UserID,
			BaseCurrency:     in.BaseCurrency,
			QuoteCurrency:    quote,
			Notional:         preview.Notional,
			ForwardRate:      preview.ForwardRate,
			SpotAskRate:      preview.SpotAskRate,
			SpreadFactor:     preview.SpreadFactor,
			DaysToSettlement: preview.DaysToSettlement,
			Commission:       money.FormatAmount(commission),
			ReservationID:    opID,
			FromAccountID:    rsdAcc.ID,
			ToAccountID:      baseAcc.ID,
			SettlementDate:   in.SettlementDate,
		})
		if ierr != nil {
			return ierr
		}
		out = row
		return nil
	})
	if err != nil {
		// Roll back the reservation we made before the tx.
		_, _ = s.ReleaseFunds(reserveCtx, opID)
		return nil, err
	}
	return out, nil
}

// ListForexForwards returns the caller's own forward contracts.
func (s *Service) ListForexForwards(ctx context.Context) ([]*domain.ForexForward, error) {
	if err := s.requirePermission(ctx, permissions.PaymentWrite); err != nil {
		return nil, err
	}
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	return s.Store.ListForexForwardsByClient(ctx, p.UserID)
}

// CancelForexForward cancels a still-'active' contract owned by the caller
// and releases the held reservation.
func (s *Service) CancelForexForward(ctx context.Context, id string) (*domain.ForexForward, error) {
	if err := s.requirePermission(ctx, permissions.PaymentWrite); err != nil {
		return nil, err
	}
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if id == "" {
		return nil, apperr.Validation("id is required")
	}
	f, err := s.Store.CancelForexForward(ctx, id, p.UserID)
	if err != nil {
		return nil, err
	}
	// Release the obligation reservation (best-effort, idempotent).
	if _, rerr := s.ReleaseFunds(s.internalCtx(ctx), f.ReservationID); rerr != nil {
		s.Log.WarnContext(ctx, "forex forward cancel: release reservation failed",
			"id", f.ID, "reservation_id", f.ReservationID, "err", rerr.Error())
	}
	return f, nil
}

// ForexForwardSettlementResult tallies one settlement-sweep pass.
type ForexForwardSettlementResult struct {
	Processed int
	Settled   int
	Failed    int
}

// RunForexForwardSettlement settles every 'active' forward whose
// settlement date has arrived. Admin-only (the scheduler presents an
// admin principal). For each due contract it commits the held reservation
// into a DIRECT fixed-rate conversion: the reserved RSD (forward_rate ×
// notional) settles against the client's base-currency account, crediting
// exactly the notional at the locked rate (commission-free — the spread
// is already in the rate). On success the row is 'settled' and the client
// notified; on failure 'failed' + release + notify. Idempotent: the
// CommitReservedFunds op_id guard means a re-swept already-committed
// reservation returns its existing legs without double-crediting.
func (s *Service) RunForexForwardSettlement(ctx context.Context) (*ForexForwardSettlementResult, error) {
	if err := s.requirePermission(ctx, permissions.Admin); err != nil {
		return nil, err
	}
	now := s.now()
	due, err := s.Store.ListDueForexForwards(ctx, now)
	if err != nil {
		return nil, err
	}

	res := &ForexForwardSettlementResult{}
	for _, f := range due {
		res.Processed++
		execErr := s.settleForexForward(ctx, f)
		at := s.now()
		switch {
		case execErr == nil:
			if merr := s.Store.MarkForexForwardSettled(ctx, f.ID, at); merr != nil {
				s.Log.WarnContext(ctx, "forex forward mark-settled failed", "id", f.ID, "err", merr.Error())
				continue
			}
			res.Settled++
			s.notifyForexForwardSettled(ctx, f)
		default:
			// Settlement could not complete (e.g. the reserved funds were
			// somehow spent, or a balance invariant tripped). Mark failed,
			// release the reservation, notify.
			reason := "poravnanje terminskog ugovora nije uspelo"
			if errors.Is(execErr, errForwardReleasedReservation) {
				reason = "rezervisana sredstva više nisu dostupna"
			}
			if merr := s.Store.MarkForexForwardFailed(ctx, f.ID, reason, at); merr != nil {
				s.Log.WarnContext(ctx, "forex forward mark-failed failed", "id", f.ID, "err", merr.Error())
				continue
			}
			_, _ = s.ReleaseFunds(s.internalCtx(ctx), f.ReservationID)
			res.Failed++
			s.notifyForexForwardFailed(ctx, f, reason)
			s.Log.WarnContext(ctx, "forex forward settlement failed",
				"id", f.ID, "client_id", f.ClientID, "err", execErr.Error())
		}
	}
	return res, nil
}

// errForwardReleasedReservation flags that the held reservation was no
// longer 'held' at settlement time (released/committed elsewhere).
var errForwardReleasedReservation = errors.New("reservation no longer held")

// settleForexForward commits the contract's reservation into the direct
// fixed-rate conversion. The reserved amount (forward_rate × notional, in
// RSD) is committed against the client's base-currency account, crediting
// exactly the contract notional. CommitReservedFunds writes the FX-leg
// ledger rows hopping through the bank's house accounts; passing
// IsActuary=true zeroes any commission so the move runs at the locked
// rate exactly.
func (s *Service) settleForexForward(ctx context.Context, f *domain.ForexForward) error {
	_, err := s.CommitReservedFunds(s.internalCtx(ctx), CommitReservedFundsInput{
		OpID:          f.ReservationID,
		DestAccountID: f.ToAccountID,
		DestAmount:    f.Notional,
		DestCurrency:  f.BaseCurrency,
		IsActuary:     true, // fixed-rate, commission-free
		Purpose:       "Poravnanje terminskog ugovora",
	})
	if err != nil {
		if apperrIs(err, apperr.KindFailedPrecondition) {
			return errForwardReleasedReservation
		}
		return err
	}
	return nil
}

// =====================================================================
// Spread factor configuration (supervisor-set)
// =====================================================================

// GetForexForwardSpreads returns every configured pair's SpreadFactor.
// Available to any payment-capable principal (clients see what they'll be
// quoted; supervisors edit).
func (s *Service) GetForexForwardSpreads(ctx context.Context) ([]*domain.ForexForwardSpread, error) {
	if err := s.requirePermission(ctx, permissions.PaymentWrite); err != nil {
		return nil, err
	}
	return s.Store.ListForexForwardSpreads(ctx)
}

// SetForexForwardSpread upserts a pair's SpreadFactor. Supervisor-only
// (spec: bank parameters are supervisor-managed). Admin also admitted.
func (s *Service) SetForexForwardSpread(ctx context.Context, base, quote domain.Currency, spreadFactor string) (*domain.ForexForwardSpread, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if !permissions.HasAny(p.Permissions, permissions.ActuarySupervisor, permissions.Admin) {
		return nil, apperr.PermissionDenied("samo supervizor može menjati parametre terminskih ugovora")
	}
	if !base.Supported() || !quote.Supported() {
		return nil, apperr.Validation("unsupported currency")
	}
	if quote != forwardQuoteCurrency {
		return nil, apperr.Validation("terminski ugovor se poravnava u RSD")
	}
	if base == quote {
		return nil, apperr.Validation("valute moraju biti različite")
	}
	sf, perr := money.Parse(spreadFactor)
	if perr != nil {
		return nil, apperr.Validation(perr.Error())
	}
	if sf.Sign() < 0 {
		return nil, apperr.Validation("spread faktor ne sme biti negativan")
	}
	return s.Store.UpsertForexForwardSpread(ctx, &domain.ForexForwardSpread{
		BaseCurrency:  base,
		QuoteCurrency: quote,
		SpreadFactor:  money.FormatRate(sf),
		UpdatedBy:     p.UserID,
	})
}

// =====================================================================
// Helpers
// =====================================================================

// clientAccountByCurrency resolves the client's single active account in
// the given currency. Forwards need exactly one RSD obligation account
// and one base-currency credit account.
func (s *Service) clientAccountByCurrency(ctx context.Context, clientID string, cur domain.Currency) (*domain.Account, error) {
	accs, _, err := s.Store.ListAccounts(ctx, domain.AccountFilter{
		OwnerClientID: clientID,
		Currency:      cur,
		Status:        domain.AccountActive,
		ExcludeKinds:  []domain.AccountKind{domain.KindSystem, domain.KindStateTax, domain.KindForexBook, domain.KindFund},
	}, 1, 2)
	if err != nil {
		return nil, err
	}
	if len(accs) == 0 {
		return nil, apperr.FailedPrecondition("klijent nema aktivan " + string(cur) + " račun")
	}
	return accs[0], nil
}

// internalCtx stamps an admin principal on the outgoing context so the
// internal-only reservation RPCs (ReserveFunds / CommitReservedFunds /
// ReleaseFunds) admit the call. These run within the same process, but
// requireInternal reads the principal from context, so we present one.
func (s *Service) internalCtx(ctx context.Context) context.Context {
	return auth.WithPrincipal(ctx, auth.Principal{
		UserID:      domain.SystemOwnerID,
		UserKind:    auth.KindEmployee,
		Permissions: []string{permissions.Admin},
	})
}

func (s *Service) notifyForexForwardSettled(ctx context.Context, f *domain.ForexForward) {
	body := "Poštovani,\n\nVaš terminski ugovor je poravnan: kupili ste " +
		f.Notional + " " + string(f.BaseCurrency) + " po kursu " + f.ForwardRate +
		" RSD.\n\nBanka 3"
	s.notify(ctx, f.ClientID, "payment", "Terminski ugovor je poravnan", body)
}

func (s *Service) notifyForexForwardFailed(ctx context.Context, f *domain.ForexForward, reason string) {
	body := "Poštovani,\n\nVaš terminski ugovor za " + f.Notional + " " +
		string(f.BaseCurrency) + " nije mogao da se poravna (" + reason + ").\n\nBanka 3"
	s.notify(ctx, f.ClientID, "payment", "Terminski ugovor nije poravnan", body)
}
