package store

import (
	"context"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/domain"
)

// =====================================================================
// Refresh tokens
// =====================================================================

// CreateRefreshToken stores the hash and returns its ID.
func (s *Store) CreateRefreshToken(ctx context.Context, kind domain.UserKind, userID, hash string, expiresAt time.Time) error {
	const q = `
        insert into "user".refresh_tokens (user_id, user_kind, token_hash, expires_at)
        values ($1, $2, $3, $4)`
	if _, err := s.DB.Exec(ctx, q, userID, string(kind), hash, expiresAt); err != nil {
		return apperr.Internal("create refresh token", err)
	}
	return nil
}

// LookupRefreshToken returns (user_kind, user_id) for the hash if not
// revoked and not expired. Returns NotFound otherwise.
func (s *Store) LookupRefreshToken(ctx context.Context, hash string) (domain.UserKind, string, error) {
	const q = `
        select user_kind, user_id from "user".refresh_tokens
        where token_hash = $1 and revoked_at is null and expires_at > now()`
	var kind, userID string
	if err := s.DB.QueryRow(ctx, q, hash).Scan(&kind, &userID); err != nil {
		if noRows(err) {
			return "", "", apperr.Unauthenticated("invalid or expired refresh token")
		}
		return "", "", apperr.Internal("lookup refresh", err)
	}
	return domain.UserKind(kind), userID, nil
}

// RevokeRefreshToken marks one token revoked.
func (s *Store) RevokeRefreshToken(ctx context.Context, hash string) error {
	const q = `update "user".refresh_tokens set revoked_at = now() where token_hash = $1`
	if _, err := s.DB.Exec(ctx, q, hash); err != nil {
		return apperr.Internal("revoke refresh", err)
	}
	return nil
}

// RevokeAllRefreshTokens marks every active token for the user revoked.
// Used when permissions change or the user is deactivated.
func (s *Store) RevokeAllRefreshTokens(ctx context.Context, kind domain.UserKind, userID string) error {
	const q = `
        update "user".refresh_tokens set revoked_at = now()
        where user_kind = $1 and user_id = $2 and revoked_at is null`
	if _, err := s.DB.Exec(ctx, q, string(kind), userID); err != nil {
		return apperr.Internal("revoke all refresh", err)
	}
	return nil
}

// =====================================================================
// Activation tokens (employees only)
// =====================================================================

func (s *Store) CreateActivationToken(ctx context.Context, employeeID, hash string, expiresAt time.Time) error {
	const q = `
        insert into "user".activation_tokens (employee_id, token_hash, expires_at)
        values ($1, $2, $3)`
	if _, err := s.DB.Exec(ctx, q, employeeID, hash, expiresAt); err != nil {
		return apperr.Internal("create activation", err)
	}
	return nil
}

// LookupActivationToken returns the employee_id for an unused, non-expired
// token. Returns the special FailedPrecondition error for an expired or
// already-used token so the gateway can return a specific message.
func (s *Store) LookupActivationToken(ctx context.Context, hash string) (string, error) {
	const q = `
        select employee_id, used_at, expires_at from "user".activation_tokens
        where token_hash = $1`
	var employeeID string
	var usedAt, expiresAt *time.Time
	var (
		usedAtV    *time.Time
		expiresAtV time.Time
	)
	if err := s.DB.QueryRow(ctx, q, hash).Scan(&employeeID, &usedAt, &expiresAt); err != nil {
		if noRows(err) {
			return "", apperr.NotFound("activation token not found")
		}
		return "", apperr.Internal("lookup activation", err)
	}
	usedAtV = usedAt
	if expiresAt != nil {
		expiresAtV = *expiresAt
	}
	if usedAtV != nil {
		return "", apperr.FailedPrecondition("activation link already used")
	}
	if time.Now().After(expiresAtV) {
		return "", apperr.FailedPrecondition("activation link has expired")
	}
	return employeeID, nil
}

// MarkActivationTokenUsed sets used_at = now().
func (s *Store) MarkActivationTokenUsed(ctx context.Context, hash string) error {
	const q = `update "user".activation_tokens set used_at = now() where token_hash = $1`
	if _, err := s.DB.Exec(ctx, q, hash); err != nil {
		return apperr.Internal("mark activation used", err)
	}
	return nil
}

// InvalidateActivationTokens marks every outstanding activation token
// for the employee as used (so prior emails can't be replayed).
func (s *Store) InvalidateActivationTokens(ctx context.Context, employeeID string) error {
	const q = `
        update "user".activation_tokens set used_at = now()
        where employee_id = $1 and used_at is null`
	if _, err := s.DB.Exec(ctx, q, employeeID); err != nil {
		return apperr.Internal("invalidate activation", err)
	}
	return nil
}

// =====================================================================
// Password reset tokens
// =====================================================================

func (s *Store) CreatePasswordResetToken(ctx context.Context, kind domain.UserKind, userID, hash string, expiresAt time.Time) error {
	const q = `
        insert into "user".password_reset_tokens (user_id, user_kind, token_hash, expires_at)
        values ($1, $2, $3, $4)`
	if _, err := s.DB.Exec(ctx, q, userID, string(kind), hash, expiresAt); err != nil {
		return apperr.Internal("create reset", err)
	}
	return nil
}

// LookupPasswordResetToken returns (user_kind, user_id) for an unused,
// non-expired token. FailedPrecondition for expired/used; NotFound otherwise.
func (s *Store) LookupPasswordResetToken(ctx context.Context, hash string) (domain.UserKind, string, error) {
	const q = `
        select user_kind, user_id, used_at, expires_at
        from "user".password_reset_tokens where token_hash = $1`
	var kind, userID string
	var usedAt *time.Time
	var expiresAt time.Time
	if err := s.DB.QueryRow(ctx, q, hash).Scan(&kind, &userID, &usedAt, &expiresAt); err != nil {
		if noRows(err) {
			return "", "", apperr.NotFound("reset token not found")
		}
		return "", "", apperr.Internal("lookup reset", err)
	}
	if usedAt != nil {
		return "", "", apperr.FailedPrecondition("reset link already used")
	}
	if time.Now().After(expiresAt) {
		return "", "", apperr.FailedPrecondition("reset link has expired")
	}
	return domain.UserKind(kind), userID, nil
}

func (s *Store) MarkPasswordResetTokenUsed(ctx context.Context, hash string) error {
	const q = `update "user".password_reset_tokens set used_at = now() where token_hash = $1`
	if _, err := s.DB.Exec(ctx, q, hash); err != nil {
		return apperr.Internal("mark reset used", err)
	}
	return nil
}
