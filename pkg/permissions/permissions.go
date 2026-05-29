// Package permissions is the canonical catalog of permission strings used
// across the system. Permissions are dot-namespaced (`<domain>.<action>`)
// and frozen per celina — never rename, never repurpose. New celine
// append.
package permissions

// Celina 1 — user management.
const (
	// Admin grants the ability to manage employees (including granting
	// the admin permission itself). Implies all employee.* permissions.
	Admin = "admin"

	EmployeeRead  = "employee.read"
	EmployeeWrite = "employee.write"

	ClientRead  = "client.read"
	ClientWrite = "client.write"

	// PermissionGrant lets the holder change another user's permissions.
	// Distinct from Admin so a future supervisor role can grant a
	// narrower subset without becoming a full admin.
	PermissionGrant = "permission.grant"
)

// Actuary marks a holder as a bank actuary (spec p.5: agent or
// supervisor variant). Carried across c2 + c3 so the FX-commission
// branch can already short-circuit to zero on actuary trades when
// c3 lands; in c2 no role bundle assigns it, so all FX moves still
// charge the configured commission.
const Actuary = "actuary"

// Celina 2 — basic banking. Companies, accounts, FX rates, payments.
// (Card/payment/loan permissions append in subsequent slices.)
const (
	CompanyRead  = "company.read"
	CompanyWrite = "company.write"

	AccountRead  = "account.read"
	AccountWrite = "account.write"

	// ExchangeWrite gates upserting FX rates. Reads are open to any
	// authenticated user (clients see the menjačnica board).
	ExchangeWrite = "exchange.write"

	// PaymentWrite lets a holder initiate a payment/transfer from an
	// account they own (clients) or — for employees — on behalf of an
	// account they have AccountWrite over. Read of own transactions is
	// implicit (no separate payment.read perm).
	PaymentWrite = "payment.write"

	// CardRead/CardWrite gate card management. Clients have read+write
	// on their own cards (block at least); employees with CardWrite
	// can manage any card (the spec p.29 unblock-only-by-employee
	// behaviour is enforced in the service layer, not by perm split).
	CardRead  = "card.read"
	CardWrite = "card.write"

	// LoanRead/LoanWrite gate loan operations. Clients can submit
	// requests and read their own loans (LoanRead+LoanWrite for their
	// own row); employees with LoanWrite approve/reject and run the
	// installment / variable-rate jobs.
	LoanRead  = "loan.read"
	LoanWrite = "loan.write"
)

// Celina 3 — trading.
//
// Two distinct axes:
//
//   - Actuary role (employees only): supervisor vs agent. Both bundle
//     the [Actuary] marker so the FX-commission branch zeroes out for
//     trades on behalf of the bank. Supervisors approve orders, manage
//     agents, and run the capital-gains tax cron. Agents are subject
//     to a daily RSD limit.
//   - Client trading (clients only): clients with [TradingClient] see
//     stocks + futures (not options) and may submit orders that auto-
//     approve. [TradingMargin] is granted to clients with an approved
//     loan and to employees who have been explicitly enabled.
const (
	ActuarySupervisor = "actuary.supervisor"
	ActuaryAgent      = "actuary.agent"

	TradingClient = "trading.client"
	TradingMargin = "trading.margin"
)

// Celina 4 — OTC trading + investment funds + Profit Banke (spec
// p.64-76).
//
// Spec p.4 defines client OTC + funds access as part of the single
// "ClientTrading" capability: a client either has the whole trading
// bundle (stocks + OTC + funds) or none of it. The earlier split into
// otc.read / otc.trade.client / funds.read.client / funds.invest.client
// modelled a fineness the spec doesn't sanction and allowed a stripped
// client to retain OTC/funds access. The client-side gate is now
// TradingClient (c3 const) for all four c4 portals.
//
// The supervisor namespace stays granular because spec p.4 lists the
// employee-side capabilities separately ("upravljanje OTC trgovinom,
// fondovima i agentima") and there are admin-only flows that grant
// a subset.
//
//   - OTC: supervisors negotiate with other supervisors (spec p.79
//     forbids mixed-kind counterparties). Clients on the same flow
//     use TradingClient.
//   - Funds: supervisors create funds, place orders as the fund actor,
//     and act "in name of the bank" (spec p.75 Napomena 2). Read on
//     the supervisor side is split from manage so a non-manager
//     supervisor still sees the discovery surface.
//   - Profit Banke (spec p.76): the supervisor dashboard reading
//     actuary performance + bank's positions in funds.
const (
	OTCTradeSupervisor    = "otc.trade.supervisor"
	FundsReadSupervisor   = "funds.read.supervisor"
	FundsManageSupervisor = "funds.manage.supervisor"
	BankProfitRead        = "bank.profit.read"
)

// Role bundles. The spec frames users in terms of roles; the system
// stores and checks permissions. These bundles are applied at user
// creation; later admin actions can add or remove individual permissions.
var (
	// Clients see their own accounts and read FX rates, manage their
	// own cards, and initiate payments. They don't have account.write
	// — opening a new account is an employee action per spec p.11
	// ("Račun kreira Zaposleni").
	RoleClientBasic = []string{ClientRead, AccountRead, CardRead, CardWrite, PaymentWrite, LoanRead, LoanWrite}
	// RoleClientTrading is one capability per spec p.4: stocks + OTC +
	// funds gated by a single TradingClient flag. The c4 portals
	// (OTC, OTC Ponude i Ugovori, Investicioni fondovi, Moji fondovi)
	// all check TradingClient on the client side.
	RoleClientTrading = append(append([]string{}, RoleClientBasic...), TradingClient)

	// Employees:
	//   basic — read-only on people and accounts; legacy from c1.
	//   agent — c2 day-to-day: opens accounts, creates clients, manages
	//     companies, blocks/unblocks cards. Cannot manage employees or
	//     grant permissions.
	//   supervisor — agent + employee.read so they can see staff.
	//   admin — everything.
	RoleEmployeeBasic = []string{EmployeeRead, ClientRead, AccountRead, CompanyRead, CardRead, LoanRead}
	RoleEmployeeAgent = []string{
		ClientRead, ClientWrite,
		AccountRead, AccountWrite,
		CompanyRead, CompanyWrite,
		CardRead, CardWrite,
		LoanRead, LoanWrite,
	}
	RoleEmployeeSupervisor = append([]string{EmployeeRead}, RoleEmployeeAgent...)
	RoleEmployeeAdmin      = []string{
		Admin,
		EmployeeRead, EmployeeWrite,
		ClientRead, ClientWrite,
		CompanyRead, CompanyWrite,
		AccountRead, AccountWrite,
		CardRead, CardWrite,
		LoanRead, LoanWrite,
		PaymentWrite,
		ExchangeWrite,
		PermissionGrant,

		// Spec p.38: every admin is implicitly a supervisor on the
		// trading side. We carry the marker permission too so the
		// FX-commission branch and "is on behalf of the bank" check
		// short-circuit even before the supervisor opens the trading
		// portal for the first time.
		Actuary, ActuarySupervisor,
		TradingMargin,
	}

	// RoleEmployeeActuarySupervisor and RoleEmployeeActuaryAgent are
	// applied on top of an existing employee bundle when an admin
	// promotes a user to actuary status. The trading service expects a
	// matching trading.actuary_info row with the same `type`.
	// Spec p.38: a supervisor manages agents — needs to read employee
	// rows to render their name/email/position on the actuari portal.
	RoleEmployeeActuarySupervisor = []string{
		Actuary, ActuarySupervisor, TradingMargin, EmployeeRead,
		// c4: supervisors negotiate OTC with other supervisors, manage
		// investment funds, and see the Profit Banke dashboard.
		OTCTradeSupervisor,
		FundsReadSupervisor, FundsManageSupervisor,
		BankProfitRead,
	}
	RoleEmployeeActuaryAgent = []string{Actuary, ActuaryAgent}
)

// Has reports whether the holder set contains target.
func Has(holder []string, target string) bool {
	for _, p := range holder {
		if p == target {
			return true
		}
	}
	return false
}

// HasAny reports whether holder contains any of targets.
func HasAny(holder []string, targets ...string) bool {
	for _, t := range targets {
		if Has(holder, t) {
			return true
		}
	}
	return false
}

// HasAll reports whether holder contains every target.
func HasAll(holder []string, targets ...string) bool {
	for _, t := range targets {
		if !Has(holder, t) {
			return false
		}
	}
	return true
}

// IsActuary reports whether the holder set marks the principal as a
// bank actuary. Trade-on-behalf-of-the-bank flows skip per-client
// fees (e.g. menjačnica commission) when this returns true.
func IsActuary(holder []string) bool { return Has(holder, Actuary) }
