package repo

import (
	"database/sql"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

// Repository holds database connections for all operations
type Repository struct {
	Database *sql.DB
	Gorm     *gorm.DB
}

// Common errors
var (
	ErrInvalidPasswordActionToken = errors.New("invalid or expired password token")
	ErrClientNotFound             = errors.New("client not found")
	ErrClientEmailExists          = errors.New("client email already exists")
	ErrClientNoFieldsToUpdate     = errors.New("no client fields to update")
	ErrEmployeeNotFound           = errors.New("employee not found")
	ErrUnknownPermission          = errors.New("unknown permissions")
	ErrUserNotFound               = errors.New("user not found")
)

// User represents a generic user (employee or client) for authentication
type User struct {
	Email          string
	HashedPassword []byte
	Salt           []byte
}

// UserRestrictions is a map for filtering users (exported)
type UserRestrictions map[string]string

// isUniqueViolation checks if a database error is a unique constraint violation.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
