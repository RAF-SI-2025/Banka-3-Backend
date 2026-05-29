// Package domain holds plain-Go entities and value types for the user
// service. No I/O, no framework imports.
package domain

import "time"

// Gender mirrors the spec's three values plus an unspecified zero. The
// DB column has a check constraint matching this set.
type Gender string

const (
	GenderUnspecified Gender = ""
	GenderMale        Gender = "male"
	GenderFemale      Gender = "female"
	GenderOther       Gender = "other"
)

// UserKind discriminates the two kinds of authenticatable users. Mirrors
// pkg/auth.UserKind; kept separate so domain has no dep on auth.
type UserKind string

const (
	KindEmployee UserKind = "employee"
	KindClient   UserKind = "client"
)

// Employee is a bank staff member. Activated() is true once
// PasswordHash is set; admin creates the row with PasswordHash nil and
// the employee fills it in via the activation link.
type Employee struct {
	ID             string
	Email          string
	Username       string
	PasswordHash   string // empty until activated
	FirstName      string
	LastName       string
	DateOfBirth    time.Time
	Gender         Gender
	Phone          string
	Address        string
	Position       string
	Department     string
	Active         bool
	Permissions    []string
	SessionVersion int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Activated reports whether the employee has set their password.
func (e Employee) Activated() bool { return e.PasswordHash != "" }

// Client is a banking customer. Created by an employee in c2 (when an
// account is opened); for c1 we only read/update.
type Client struct {
	ID             string
	Email          string
	PasswordHash   string
	FirstName      string
	LastName       string
	DateOfBirth    time.Time
	Gender         Gender
	Phone          string
	Address        string
	Active         bool
	Permissions    []string
	SessionVersion int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// VerificationEvent is one row of a user's verification request
// history (spec p.84). Status is one of 'pending', 'success',
// 'failed'; ResolvedAt is nil until the code is consumed or its
// attempt budget is spent. The user service never stores the code or
// reasons about expiry — an unresolved 'pending' past the code TTL is
// projected to expired by the gateway.
type VerificationEvent struct {
	ID         string
	UserID     string
	ActionKind string
	Status     string
	CreatedAt  time.Time
	ResolvedAt *time.Time
}

// Verification status constants. Mirrors the DB check constraint in
// migration 0003.
const (
	VerificationPending = "pending"
	VerificationSuccess = "success"
	VerificationFailed  = "failed"
)

// EmployeeFilter narrows ListEmployees results. Empty fields don't filter.
type EmployeeFilter struct {
	Email    string
	Name     string // matches first or last name (case-insensitive substring)
	Position string
}

// ClientFilter narrows ListClients results.
type ClientFilter struct {
	Email string
	Name  string
}
