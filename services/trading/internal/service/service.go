// Package service holds the trading service's business logic.
package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/auth"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/clock"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/permissions"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/domain"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/saga"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/trading/internal/store"
)

// Config carries trading-service knobs not covered by infra config.
type Config struct {
	// Belgrade is the wall-clock timezone used to anchor "after-hours"
	// computations and the daily limit-reset cron. Defaults to
	// Europe/Belgrade.
	Belgrade *time.Location

	// FXCommission is the menjačnica fee rate as a decimal string
	// ("0.005" = 0.5%). Used when bank-side conversions of trade-RSD
	// equivalents go through the menjačnica formula. The trading
	// service does not collect it directly — the bank service does on
	// the FX leg — but we mirror the value so unit tests pin behaviour.
	FXCommission string

	// TickRetry is how long the execution worker waits before re-checking
	// an order that wasn't ready to fill this tick. Defaults to 5s.
	TickRetry time.Duration

	// ExecutionTickInterval is how often the worker wakes up to walk
	// every active order. Defaults to 10s.
	ExecutionTickInterval time.Duration

	// SagaDebugFaultInjection lets requests force-fail named SAGA steps
	// via the X-Saga-Force-Fail / -Kind / -Compensate-Fail headers (see
	// pkg saga FaultsFromMetadata). Off in production; the c4-tests
	// cypress spec turns this on locally to drive scenarios 4/6/7/9 that
	// have no FE-natural failure mode.
	SagaDebugFaultInjection bool

	// BankName is this bank's display name (env BANK_NAME). Spec p.67:
	// on the OTC discovery board an actuary holding is owned "in the
	// name of the bank", so the supervisor view renders the bank here
	// as Owner instead of the individual actuary's name (the
	// "Za supervizore" table shows "Banka 1", not a person). Defaults
	// to "Banka 3".
	BankName string

	// OwnRoutingNumber identifies this bank in the celina-5 inter-bank
	// protocol (spec p.77+). Stamped into PreparePayment.sender_routing_number
	// on outbound cross-bank legs. Defaults to 333 ("Banka 3") via
	// service.New; production loads it from BANK_ROUTING_NUMBER.
	OwnRoutingNumber int
}

// RateProvider returns raw FX bid/ask between two currencies. Used by
// the order/limit math to convert security-currency amounts into RSD
// without going through the bank's commission-charging menjačnica.
//
// The adapter in services/trading/internal/app dials the exchange
// service. Tests inject a stub.
type RateProvider interface {
	Quote(ctx context.Context, from, to domain.Currency) (bid, ask string, err error)
}

// UserResolver resolves cross-service user details that the trading
// service needs but does not own — currently just the supervisor tax
// dashboard's "Ime i prezime" column + name filter (spec p.63). The
// adapter in services/trading/internal/app dials the user service.
// Tests inject a stub. May be nil on a minimal dev stack: in that case
// display_name comes back empty and the name_query filter degrades to
// a UUID substring match.
type UserResolver interface {
	DisplayName(ctx context.Context, userID string, kind domain.UserKind) (string, error)
	// EmployeePermissions returns the permission strings for an
	// employee. Used by CreateFund to validate an admin-supplied
	// manager override is actually a supervisor (spec p.74). Errors
	// when the id does not resolve to an employee.
	EmployeePermissions(ctx context.Context, userID string) ([]string, error)
	// RecordAudit appends one entry to the cross-cutting audit log
	// owned by user-svc (todoSpec S40/S41/S43 trading write sites).
	// actorID/actorKind identify the real caller (supervisor/admin)
	// resolved from the request principal, so the audit shows the real
	// person rather than the admin sentinel the adapter uses for
	// transport. Best-effort: implementations must not fail the
	// underlying trading operation on a delivery error.
	RecordAudit(ctx context.Context, action, actorID, actorKind, targetID, targetLabel, oldVal, newVal, note string) error
	// Email resolves a user's email address so the trading service can
	// add an email leg to its order/price-alert notifications (the
	// in-app feed only carries a user_id, not an address). For
	// kind=client it dials user-svc GetClient; for kind=employee it
	// dials GetEmployee. Returns the address, or "" + error when the id
	// does not resolve. Callers treat any error as "no address" and fall
	// back to in-app only.
	Email(ctx context.Context, userID string, kind domain.UserKind) (string, error)
}

// MarginChecker reads the funding-source state needed by spec p.55
// margin-eligibility checks: the source account's balance and (for
// clients only) their largest active loan principal. The trading
// service does the comparison itself; the bank-side adapter is just a
// data accessor. Tests inject a stub.
type MarginChecker interface {
	// AccountAvailable returns the currency and *available* balance of
	// the named account. Errors propagate as-is so the caller can
	// surface NotFound / PermissionDenied to the user.
	AccountAvailable(ctx context.Context, accountID string) (currency domain.Currency, available string, err error)
	// ClientLargestActiveLoan returns the largest currently-active loan
	// for the client (currency + remaining_principal). Returns ("","",nil)
	// when the client has no active loans.
	ClientLargestActiveLoan(ctx context.Context, clientID string) (currency domain.Currency, amount string, err error)
}

// Notifier fans a trading event out to the recipient's email and in-app
// feed (todoSpec C3 order/price-alert notifications). The caller builds
// the Serbian copy and supplies the recipient; the adapter in
// internal/app dials notification-svc (SendEmail + CreateNotification).
// May be nil on a dev stack without notification-svc wired — every call
// site must nil-check, the same as the other optional dependencies on
// this struct. Best-effort: a delivery failure must never fail the
// underlying trading operation.
type Notifier interface {
	// InApp writes one in-app notification row for (userID, kind).
	// eventKind tags the row for FE grouping (e.g. "order", "price_alert").
	InApp(ctx context.Context, userID string, kind domain.UserKind, eventKind, title, body string) error
	// Email sends a rendered message. A "" recipient is a no-op.
	Email(ctx context.Context, to, subject, body string) error
}

// Service is the trading aggregate. Sub-aggregates are split per file
// (actuaries, exchanges, securities, listings, orders, portfolio,
// tax) but share this struct so cross-aggregate methods (e.g. order
// approval bumping the actuary's used_limit) stay in-package.
type Service struct {
	Store *store.Store
	Cfg   Config
	Log   *slog.Logger
	// Rates converts foreign currency amounts to RSD for the
	// agent-limit check and capital-gains-tax math. May be nil on a
	// minimal dev stack — callers must tolerate that.
	Rates RateProvider
	// Settler executes the bank-side cash leg of every fill. Must be
	// wired before the execution worker runs; tests inject a stub.
	Settler TradeSettler
	// TaxSettler executes the bank-side debit for the capital-gains
	// tax cron (spec p.62). Must be wired before RunTax is called.
	TaxSettler TaxSettler
	// MarginChecker is consulted when a margin-flagged order is
	// created (spec p.55). May be nil on a minimal dev stack — in that
	// case the trading service skips the balance/loan check and only
	// enforces the permission gate. Production must wire this.
	MarginChecker MarginChecker
	// ForexSettler executes the paired cash legs of a forex pair fill
	// (spec p.42). May be nil on a minimal dev stack; forex orders
	// then skip the cash leg with a logged warning.
	ForexSettler ForexSettler
	// Users resolves display names for the supervisor tax dashboard
	// (spec p.63). May be nil on a minimal dev stack; display_name
	// then comes back empty.
	Users UserResolver
	// Notifier fans order/price-alert events out to email + the in-app
	// feed (todoSpec C3). May be nil on a dev stack without
	// notification-svc; call sites nil-check. Wired in internal/app.
	Notifier Notifier
	// MarketData refreshes stock + forex listings against an upstream
	// quote feed (spec p.40, p.42). Nil when the feed isn't wired —
	// the cron then no-ops.
	MarketData *MarketData
	// Options synthesises Black-Scholes option chains (spec x.43
	// Pristup 2). Always wired in production; nil in unit tests that
	// don't exercise the generator.
	Options *OptionGenerator
	// SagaOrch drives c4 multi-step intra-bank operations (OTC premium
	// transfer, OTC exercise, fund invest/withdraw). May be nil in unit
	// tests that don't exercise the saga path. Production must wire
	// this; the recovery worker also depends on it.
	SagaOrch *saga.Orchestrator
	// SagaStore is the orchestrator's persistence adapter. Held on the
	// service so the recovery worker can scan due rows without going
	// through the orchestrator.
	SagaStore *store.SagaStore
	// Reservations is the bank-side reservation surface — Reserve,
	// Release, Commit, TransferBetweenClients. SAGA step handlers dial
	// this instead of the lower-level Settler. May be nil on a dev
	// stack that doesn't run the bank reservation RPCs.
	Reservations BankReservations
	// PartnerOTC is the outbound side of the celina-5 cross-bank OTC
	// adapter (BE-4). May be nil — the service nil-checks before
	// dialing so the trading binary boots without partner config.
	PartnerOTC PartnerOTC
	// InterbankPayer is the bank-side 2PC primitive (BE-5). SAGA step
	// handlers in external_otc_*_saga.go dial this to drive cross-bank
	// cash legs. May be nil on a minimal dev stack — the AcceptExternal/
	// ExerciseExternal entrypoints nil-check before starting the saga.
	InterbankPayer InterbankPayer
	// PartnerPayer is the outbound HTTP 2PC client — calls the remote
	// partner bank's /interbank surface in either dialect. Implemented
	// by the same interbank.Client that powers PartnerOTC; one
	// connection-pooled object handles both. Nil-safe.
	PartnerPayer PartnerPayer
	// OTCNotifier sends counterparty-facing emails on OTC events
	// (counter-offer, withdraw, accept, contract expired). Wired to an
	// email.Sender — either pkg/email directly or notification-svc,
	// decided by app wiring. Nil-safe (no email on dev stacks without
	// SMTP wired).
	OTCNotifier OTCNotifier
	// Now is the legacy wall-clock seam. Tests still pin it
	// (`s.Now = func() time.Time { return fixed }`). Production
	// leaves it nil and now() falls through to Clock, then time.Now
	// as a last resort. Newer callers should prefer Clock.
	Now func() time.Time

	// Clock is the QA-adjustable business-time provider (pkg/clock).
	// app/ constructs it as a *clock.Adjustable wired to Redis when
	// CLOCK_DEBUG=true so the gateway debug endpoint can advance time
	// uniformly across services. Nil-safe (now() falls back).
	Clock clock.Clock
}

// OTCNotifier is the trading-service view of the notification surface
// for OTC events. Adapter implementations live in app/. Nil-safe at
// every call site.
type OTCNotifier interface {
	OnOTCCounterOffer(ctx context.Context, offer *domain.OTCOffer, recipientID string, recipientKind domain.UserKind)
	OnOTCAccepted(ctx context.Context, contract *domain.OTCContract, recipientID string, recipientKind domain.UserKind)
	OnOTCWithdrawn(ctx context.Context, offer *domain.OTCOffer, recipientID string, recipientKind domain.UserKind)
	OnOTCContractExpired(ctx context.Context, contract *domain.OTCContract, recipientID string, recipientKind domain.UserKind)
	// OnOTCContractExpiringSoon warns the buyer that their contract
	// expires in daysLeft calendar days (scenario S63). Called once per
	// contract when daysLeft == 3 so the holder can act before expiry.
	OnOTCContractExpiringSoon(ctx context.Context, contract *domain.OTCContract, recipientID string, recipientKind domain.UserKind, daysLeft int)
}

// BankReservations is the trading-service view of bank's c4 reservation
// surface. The app layer wires this to a bank gRPC client; tests stub
// it. SAGA step handlers dial these instead of the lower-level Settler.
type BankReservations interface {
	Reserve(ctx context.Context, in ReserveInput) (string, error)
	Release(ctx context.Context, opID string) (released bool, err error)
	Commit(ctx context.Context, in CommitInput) (string, error)
	Transfer(ctx context.Context, in TransferInput) (string, error)
	// AccountAvailable returns (currency, available_balance) for an
	// account. Re-exposed on this interface (also on MarginChecker) so
	// fund flows don't need to depend on the c3 margin surface.
	AccountAvailable(ctx context.Context, accountID string) (domain.Currency, string, error)
	// CreateFundAccount mints the bank-side liquidity account for a
	// fund. Called from CreateFund. Returns the new account's id.
	CreateFundAccount(ctx context.Context, name string, currency domain.Currency) (accountID string, err error)
	// AccountNumber returns the 18-digit number of a bank account by id.
	// Used cosmetically by GetFund to decorate the fund's RSD liquidity
	// account number for the FE detail page.
	AccountNumber(ctx context.Context, accountID string) (string, error)
	// SettleDividend credits the destination account by `amount` worth of
	// `currency` from the bank's per-currency house account (the
	// quarterly dividend payout, todoSpec C3 S54-S59). Idempotent on
	// OpID. Bank converts (commission-free) when the destination account
	// is in a different currency (S56).
	SettleDividend(ctx context.Context, in DividendSettleInput) (string, error)
	// ListClientAccounts returns the holder's bank accounts, optionally
	// filtered to one currency. Used by the dividend cron's account-
	// routing fallback (S55/S56) to find a default-currency or RSD
	// account when the original purchase account is gone. Returns active
	// personal accounts only.
	ListClientAccounts(ctx context.Context, ownerID string, currency domain.Currency) ([]BankAccount, error)
}

// DividendSettleInput mirrors bank.SettleDividend.
type DividendSettleInput struct {
	AccountID string
	Amount    string
	Currency  domain.Currency
	OpID      string
	Purpose   string
	// InitiatorClientID/Kind let the bank stamp the holder as the
	// transaction initiator so the credit shows on the client's own
	// statement (same BE-16 pattern as the tax path).
	InitiatorClientID   string
	InitiatorClientKind domain.UserKind
}

// BankAccount is the trading-service view of a bank account row — just
// the fields the dividend account-routing fallback needs.
type BankAccount struct {
	ID       string
	Currency domain.Currency
}

// ReserveInput mirrors the bank.ReserveFunds RPC fields the SAGA needs.
type ReserveInput struct {
	AccountID string
	Amount    string
	Currency  domain.Currency
	OpID      string
	OpKind    string
}

// CommitInput mirrors bank.CommitReservedFunds.
type CommitInput struct {
	OpID          string
	DestAccountID string
	DestAmount    string
	DestCurrency  domain.Currency
	IsActuary     bool
	Purpose       string
}

// TransferInput mirrors bank.TransferBetweenClients.
type TransferInput struct {
	FromAccountID string
	ToAccountID   string
	Amount        string
	OpID          string
	OpKind        string
	IsActuary     bool
	Purpose       string
}

// PartnerPayer is the outbound HTTP side of the celina-5 2PC primitive
// — calls the remote partner bank to coordinate prepare/commit/rollback.
// Implemented by the trading-service interbank.Client. Nil-safe.
type PartnerPayer interface {
	PreparePayment(ctx context.Context, in PartnerPaymentInput) (*PartnerPaymentResult, error)
	CommitPayment(ctx context.Context, remoteBankCode, txID string) error
	RollbackPayment(ctx context.Context, remoteBankCode, txID, reason string) error
}

// PartnerPaymentInput is the outbound 2PC NEW_TX payload.
type PartnerPaymentInput struct {
	RemoteBankCode      string
	TransactionID       string
	LocalAccountNumber  string
	RemoteAccountNumber string
	Currency            string
	Amount              string
	Purpose             string
}

// PartnerPaymentResult — Accepted false on partner refusal (4xx or NO
// vote). NoReasons is populated only when the partner speaks Banka-2.
type PartnerPaymentResult struct {
	Accepted  bool
	NoReasons []string
}

// InterbankPayer is trading's view of bank's celina-5 2PC primitive
// (BankInterbankProtocolService). SAGA step handlers dial these to
// drive cross-bank cash legs.
type InterbankPayer interface {
	PreparePayment(ctx context.Context, in PrepareInterbankInput) (PrepareInterbankResult, error)
	CommitPayment(ctx context.Context, senderRouting int, txID string) (CommitInterbankResult, error)
	RollbackPayment(ctx context.Context, senderRouting int, txID, reason string) error
}

// PrepareInterbankInput mirrors bank.PreparePaymentRequest for the
// outbound (we-are-sending) flow.
type PrepareInterbankInput struct {
	SenderRoutingNumber int
	TransactionID       string
	LocalAccountNumber  string
	RemoteAccountNumber string
	Currency            domain.Currency
	Amount              string
	Purpose             string
}

// PrepareInterbankResult is what bank gives back after Prepare.
type PrepareInterbankResult struct {
	TransactionID string
	ReservationID string
	Status        string
}

// CommitInterbankResult.
type CommitInterbankResult struct {
	TransactionID string
	OpID          string
	Status        string
}

// New constructs a Service with sane defaults. The app layer fills in
// gRPC clients and other dependencies via direct field assignment.
func New(st *store.Store, cfg Config, log *slog.Logger) *Service {
	if cfg.Belgrade == nil {
		loc, err := time.LoadLocation("Europe/Belgrade")
		if err != nil {
			loc = time.UTC
		}
		cfg.Belgrade = loc
	}
	if cfg.OwnRoutingNumber == 0 {
		cfg.OwnRoutingNumber = 333
	}
	if cfg.BankName == "" {
		cfg.BankName = "Banka 3"
	}
	return &Service{Store: st, Cfg: cfg, Log: log}
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	if s.Clock != nil {
		return s.Clock.Now()
	}
	return time.Now()
}

// requirePrincipal returns the request's authenticated principal or
// Unauthenticated.
func (s *Service) requirePrincipal(ctx context.Context) (auth.Principal, error) {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return auth.Principal{}, apperr.Unauthenticated("not authenticated")
	}
	return p, nil
}

// requirePermission errors unless principal has perm or admin.
func (s *Service) requirePermission(ctx context.Context, perm string) error {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return apperr.Unauthenticated("not authenticated")
	}
	if permissions.Has(p.Permissions, perm) || permissions.Has(p.Permissions, permissions.Admin) {
		return nil
	}
	return apperr.PermissionDenied("nedovoljne permisije")
}

// requireSupervisor errors unless principal is admin or actuary
// supervisor. Spec p.38: every admin is implicitly a supervisor.
func (s *Service) requireSupervisor(ctx context.Context) (auth.Principal, error) {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return auth.Principal{}, apperr.Unauthenticated("not authenticated")
	}
	if permissions.HasAny(p.Permissions, permissions.Admin, permissions.ActuarySupervisor) {
		return p, nil
	}
	return auth.Principal{}, apperr.PermissionDenied("nedovoljne permisije")
}

// recordAudit best-effort records one audit entry against user-svc for a
// trading write action (todoSpec S40/S41/S43). The actor is the real
// request principal (the supervisor/admin who performed the action),
// resolved from ctx and passed explicitly so the audit shows the real
// person, not the admin sentinel the transport adapter attaches. Nil-safe
// (s.Users may be unset on a minimal dev stack) and never fails the
// underlying operation — delivery errors are logged and swallowed.
func (s *Service) recordAudit(ctx context.Context, action, targetID, targetLabel, oldVal, newVal, note string) {
	if s.Users == nil {
		return
	}
	var actorID, actorKind string
	if p, ok := auth.PrincipalFrom(ctx); ok {
		actorID = p.UserID
		actorKind = string(p.UserKind)
	}
	if err := s.Users.RecordAudit(ctx, action, actorID, actorKind, targetID, targetLabel, oldVal, newVal, note); err != nil {
		s.Log.Warn("audit-log write failed", "action", action, "target_id", targetID, "err", err.Error())
	}
}

// recipientEmail best-effort resolves a user's email address for the
// email leg of an order/price-alert notification. Nil-safe (s.Users may
// be unset on a minimal dev stack) and never fails the underlying
// operation: any resolution error returns "" so the caller delivers the
// in-app notification regardless. A "" return means "send no email".
func (s *Service) recipientEmail(ctx context.Context, userID string, kind domain.UserKind) string {
	if s.Users == nil {
		return ""
	}
	addr, err := s.Users.Email(ctx, userID, kind)
	if err != nil {
		s.Log.Warn("notification: email lookup failed",
			"user_id", userID, "kind", string(kind), "err", err.Error())
		return ""
	}
	return addr
}
