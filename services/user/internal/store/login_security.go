package store

import (
	"context"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/domain"
)

// tableForKind maps a user kind to its physical table name. It is a
// strict whitelist: unknown kinds error out so the caller never gets a
// chance to interpolate untrusted input into a query string.
func tableForKind(kind domain.UserKind) (string, error) {
	switch kind {
	case domain.KindEmployee:
		return "employees", nil
	case domain.KindClient:
		return "clients", nil
	default:
		return "", apperr.Internal("unknown user kind", nil)
	}
}

// IncrementFailedLogin bumps the failed-login counter for one user and
// returns the new count. Used by the login flow to drive the lockout
// threshold (todoSpec S7–S8).
func (s *Store) IncrementFailedLogin(ctx context.Context, kind domain.UserKind, userID string) (int, error) {
	tbl, err := tableForKind(kind)
	if err != nil {
		return 0, err
	}
	q := `update "user".` + tbl + `
        set failed_login_attempts = failed_login_attempts + 1, updated_at = now()
        where id = $1
        returning failed_login_attempts`
	var n int
	if err := s.DB.QueryRow(ctx, q, userID).Scan(&n); err != nil {
		return 0, apperr.Internal("increment failed login", err)
	}
	return n, nil
}

// ResetFailedLogin clears the failed-login counter and any lock. Called
// on a successful login and on password reset (todoSpec S9–S11).
func (s *Store) ResetFailedLogin(ctx context.Context, kind domain.UserKind, userID string) error {
	tbl, err := tableForKind(kind)
	if err != nil {
		return err
	}
	q := `update "user".` + tbl + `
        set failed_login_attempts = 0, locked_until = null, updated_at = now()
        where id = $1`
	if _, err := s.DB.Exec(ctx, q, userID); err != nil {
		return apperr.Internal("reset failed login", err)
	}
	return nil
}

// LockUser sets the instant the account's lock lifts (todoSpec S8).
func (s *Store) LockUser(ctx context.Context, kind domain.UserKind, userID string, until time.Time) error {
	tbl, err := tableForKind(kind)
	if err != nil {
		return err
	}
	q := `update "user".` + tbl + `
        set locked_until = $2, updated_at = now()
        where id = $1`
	if _, err := s.DB.Exec(ctx, q, userID, until); err != nil {
		return apperr.Internal("lock user", err)
	}
	return nil
}
