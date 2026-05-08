// Package clock abstracts time.Now for testability.
package clock

import "time"

// Clock returns the current time. Production code should accept a Clock
// rather than calling time.Now directly so tests can inject deterministic
// time.
type Clock interface {
	Now() time.Time
}

// Real returns the system clock.
type Real struct{}

// Now implements [Clock].
func (Real) Now() time.Time { return time.Now().UTC() }

// Fixed always returns t. Useful in tests.
type Fixed struct{ T time.Time }

// Now implements [Clock].
func (f Fixed) Now() time.Time { return f.T }
