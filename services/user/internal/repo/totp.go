package repo

import (
	"database/sql"
	"errors"
	"fmt"
)

// SetTempTOTPSecret stores a temporary TOTP secret for a user.
func (r *Repository) SetTempTOTPSecret(tx *sql.Tx, id uint64, secret string) error {
	_, err := tx.Exec(`
		INSERT INTO verification_codes (client_id, temp_secret, temp_created_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (client_id)
		DO UPDATE SET
			temp_secret = EXCLUDED.temp_secret,
			temp_created_at = NOW()
	`, id, secret)
	return err
}

// GetTempSecret retrieves the temporary TOTP secret for a user.
func (r *Repository) GetTempSecret(tx *sql.Tx, id uint64) (*string, error) {
	var tempSecret string
	row := tx.QueryRow(`
		SELECT temp_secret
		FROM verification_codes
		WHERE client_id = $1
		FOR UPDATE
	`, id)
	err := row.Scan(&tempSecret)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return &tempSecret, nil
}

// GetSecret returns the active TOTP secret for a user.
func (r *Repository) GetSecret(id uint64) (*string, error) {
	var secret string
	row := r.Database.QueryRow(`
		SELECT secret
		FROM verification_codes
		WHERE client_id = $1 AND enabled = TRUE
	`, id)
	err := row.Scan(&secret)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return &secret, nil
}

// EnableTOTP activates TOTP for a user and stores the permanent secret.
func (r *Repository) EnableTOTP(tx *sql.Tx, id uint64, tempSecret string) error {
	_, err := tx.Exec(`
		UPDATE verification_codes
		SET enabled = TRUE,
		    secret = $1,
		    temp_secret = NULL
		WHERE client_id = $2
	`, tempSecret, id)
	return err
}

// DisableTOTP deactivates TOTP for a user.
func (r *Repository) DisableTOTP(tx *sql.Tx, id uint64) error {
	_, err := tx.Exec(`
		UPDATE verification_codes
		SET enabled = FALSE
		WHERE client_id = $1
	`, id)
	return err
}

// DeleteOldCodes removes all backup codes for a user.
func (r *Repository) DeleteOldCodes(tx *sql.Tx, id uint64) error {
	_, err := tx.Exec(`
		DELETE FROM backup_codes
		WHERE client_id = $1
	`, id)
	return err
}

// InsertGeneratedCodes inserts new backup codes for a user.
func (r *Repository) InsertGeneratedCodes(tx *sql.Tx, id uint64, codes []string) error {
	if err := r.DeleteOldCodes(tx, id); err != nil {
		return err
	}
	query := "INSERT INTO backup_codes (client_id, token) VALUES"
	values := []any{}
	paramIdx := 1
	for _, code := range codes {
		query += fmt.Sprintf("($%d, $%d),", paramIdx, paramIdx+1)
		paramIdx += 2
		values = append(values, id, code)
	}
	query = query[:len(query)-1]
	stmt, err := tx.Prepare(query)
	if err != nil {
		return err
	}
	_, err = stmt.Exec(values...)
	return err
}

// Status returns whether TOTP is currently enabled for a user.
func (r *Repository) Status(tx *sql.Tx, id uint64) (*bool, error) {
	var active bool
	row := tx.QueryRow(`
		SELECT enabled
		FROM verification_codes
		WHERE client_id = $1
		FOR UPDATE
	`, id)
	err := row.Scan(&active)
	if err != nil {
		dummy := false
		if errors.Is(err, sql.ErrNoRows) {
			return &dummy, nil
		}
		return nil, err
	}
	return &active, nil
}

// TryBurnBackupCode attempts to use a backup code; returns true if successful.
func (r *Repository) TryBurnBackupCode(id uint64, code string) (*bool, error) {
	query := `
		UPDATE backup_codes
		SET used = TRUE
		WHERE client_id = $1 AND token = $2
	`
	res, err := r.Database.Exec(query, id, code)
	if err != nil {
		return nil, err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	success := rows == 1
	return &success, nil
}
