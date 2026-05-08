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
)

// Role bundles. The spec frames users in terms of roles; the system
// stores and checks permissions. These bundles are applied at user
// creation; later admin actions can add or remove individual permissions.
var (
	// Clients see their own accounts and read FX rates. They don't have
	// account.write — opening a new account is an employee action per
	// spec p.11 ("Račun kreira Zaposleni").
	RoleClientBasic   = []string{ClientRead, AccountRead}
	RoleClientTrading = append([]string{}, RoleClientBasic...) // c3 will append trading perms

	// Employees:
	//   basic — read-only on people and accounts; legacy from c1.
	//   agent — c2 day-to-day: opens accounts, creates clients, manages
	//     companies. Cannot manage employees or grant permissions.
	//   supervisor — agent + employee.read so they can see staff.
	//   admin — everything.
	RoleEmployeeBasic = []string{EmployeeRead, ClientRead, AccountRead, CompanyRead}
	RoleEmployeeAgent = []string{
		ClientRead, ClientWrite,
		AccountRead, AccountWrite,
		CompanyRead, CompanyWrite,
	}
	RoleEmployeeSupervisor = append([]string{EmployeeRead}, RoleEmployeeAgent...)
	RoleEmployeeAdmin      = []string{
		Admin,
		EmployeeRead, EmployeeWrite,
		ClientRead, ClientWrite,
		CompanyRead, CompanyWrite,
		AccountRead, AccountWrite,
		ExchangeWrite,
		PermissionGrant,
	}
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
