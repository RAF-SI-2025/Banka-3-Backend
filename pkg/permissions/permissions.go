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

// Role bundles. The spec frames users in terms of roles; the system
// stores and checks permissions. These bundles are applied at user
// creation; later admin actions can add or remove individual permissions.
var (
	RoleClientBasic    = []string{ClientRead}
	RoleClientTrading  = append([]string{}, RoleClientBasic...) // c3 will append trading perms

	RoleEmployeeBasic      = []string{EmployeeRead, ClientRead, ClientWrite}
	RoleEmployeeAgent      = append([]string{}, RoleEmployeeBasic...) // c3 appends actuary perms
	RoleEmployeeSupervisor = append([]string{}, RoleEmployeeBasic...) // c3 appends actuary + supervisor perms
	RoleEmployeeAdmin      = []string{Admin, EmployeeRead, EmployeeWrite, ClientRead, ClientWrite, PermissionGrant}
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
