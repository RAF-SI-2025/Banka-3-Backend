package repo

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// RotateRefreshToken replaces an old refresh token hash with a new one inside a transaction.
func (r *Repository) RotateRefreshToken(tx *sql.Tx, email string, oldHash, newHash []byte, newExpiry time.Time) error {
	var storedHash []byte
	err := tx.QueryRow(`
        SELECT hashed_token FROM refresh_tokens
        WHERE email = $1 AND revoked = FALSE AND valid_until > now()
        FOR UPDATE
    `, email).Scan(&storedHash)
	if err != nil {
		return fmt.Errorf("refresh token not found or expired: %w", err)
	}

	if !bytes.Equal(storedHash, oldHash) {
		_, err := tx.Exec(`UPDATE refresh_tokens SET revoked = TRUE WHERE email = $1`, email)
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("failed to revoke tokens: %w", err)
		}
		_ = tx.Commit()
		return fmt.Errorf("token mismatch: possible reuse attack")
	}

	_, err = tx.Exec(`
        UPDATE refresh_tokens
        SET hashed_token = $1, valid_until = $2, revoked = FALSE
        WHERE email = $3
    `, newHash, newExpiry, email)
	return err
}

// InsertRefreshToken stores a new refresh token (hashed).
func (r *Repository) InsertRefreshToken(token string) error {
	parsed, _, err := jwt.NewParser().ParseUnverified(token, &jwt.RegisteredClaims{})
	if err != nil {
		return fmt.Errorf("parsing token: %w", err)
	}

	email, err := parsed.Claims.GetSubject()
	if err != nil {
		return fmt.Errorf("getting subject: %w", err)
	}

	expiry, err := parsed.Claims.GetExpirationTime()
	if err != nil {
		return fmt.Errorf("getting expiry: %w", err)
	}
	hasher := sha256.New()
	hasher.Write([]byte(token))
	hashedToken := hasher.Sum(nil)
	query := `
	INSERT INTO refresh_tokens VALUES ($1, $2, $3, FALSE)
	ON CONFLICT (email) DO UPDATE SET (hashed_token, valid_until, revoked) = (excluded.hashed_token, excluded.valid_until, excluded.revoked)
	`
	_, err = r.Database.Exec(query, email, hashedToken, expiry.Time)
	if err != nil {
		return fmt.Errorf("inserting refresh token: %w", err)
	}
	return nil
}

// RevokeRefreshTokensByEmail revokes all refresh tokens for a user.
func (r *Repository) RevokeRefreshTokensByEmail(tx *sql.Tx, email string) error {
	_, err := tx.Exec(`UPDATE refresh_tokens SET revoked = TRUE WHERE email = $1`, email)
	if err != nil {
		return fmt.Errorf("revoking refresh tokens: %w", err)
	}
	return nil
}
