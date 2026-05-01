package repo

import (
	"database/sql"
)

// GetUserByEmail retrieves a user (employee or client) by email.
func (r *Repository) GetUserByEmail(email string) (*User, error) {
	query := `
		SELECT email, password, salt_password FROM employees WHERE email = $1
		UNION ALL
		SELECT email, password, salt_password FROM clients WHERE email = $1
		LIMIT 1
	`

	var user User
	err := r.Database.QueryRow(query, email).Scan(&user.Email, &user.HashedPassword, &user.Salt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// GetUserIdByEmail returns the user ID (from employees or clients) for a given email.
func (r *Repository) GetUserIdByEmail(email string) (*uint64, error) {
	query := `
		SELECT id FROM employees WHERE email = $1
		UNION ALL
		SELECT id FROM clients WHERE email = $1
		LIMIT 1
	`
	var id uint64
	err := r.Database.QueryRow(query, email).Scan(&id)
	if err == sql.ErrNoRows {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return &id, nil
}
