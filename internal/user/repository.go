package user

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type User struct {
	email          string
	hashedPassword []byte
}

func (s *Server) GetUserByEmail(email string) (*User, error) {
	query := `
		SELECT email, password FROM employees WHERE email = $1
		UNION ALL
		SELECT email, password FROM clients WHERE email = $1
		LIMIT 1
	`

	var user User

	err := s.database.QueryRow(query, email).Scan(&user.email, &user.hashedPassword)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *Server) rotateRefreshToken(tx *sql.Tx, email string, oldHash, newHash []byte, newExpiry time.Time) error {
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
			tx.Rollback()
			return fmt.Errorf("failed to revoke tokens: %w", err)
		}
		tx.Commit()
		return fmt.Errorf("token mismatch: possible reuse attack")
	}

	_, err = tx.Exec(`
        UPDATE refresh_tokens
        SET hashed_token = $1, valid_until = $2, revoked = FALSE
        WHERE email = $3
    `, newHash, newExpiry, email)
	return err
}

func (s *Server) InsertRefreshToken(token string) error {
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
	hashed_token := hasher.Sum(nil)
	query := `
	INSERT INTO refresh_tokens VALUES ($1, $2, $3, FALSE)
	ON CONFLICT (email) DO UPDATE SET (hashed_token, valid_until, revoked) = (excluded.hashed_token, excluded.valid_until, excluded.revoked)
	`
	s.database.Exec(query, email, hashed_token, expiry.Time)

	return nil
}
