package repo

import (
	"database/sql"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

// Repository holds database connections for all operations
type Repository struct {
	Database     *sql.DB
	ReadDatabase *sql.DB
	Gorm         *gorm.DB
	ReadGorm     *gorm.DB
}

func (r *Repository) readDB() *sql.DB {
	if r.ReadDatabase != nil {
		return r.ReadDatabase
	}
	return r.Database
}

func (r *Repository) readGormDB() *gorm.DB {
	if r.ReadGorm != nil {
		return r.ReadGorm
	}
	return r.Gorm
}

func (r *Repository) ReadDB() *sql.DB {
	return r.readDB()
}

func (r *Repository) ReadGormDB() *gorm.DB {
	return r.readGormDB()
}

// Common errors
var (
	ErrInvalidPasswordActionToken = errors.New("invalid or expired password token")
	ErrClientNotFound             = errors.New("client not found")
	ErrClientEmailExists          = errors.New("client email already exists")
	ErrClientNoFieldsToUpdate     = errors.New("no client fields to update")
	ErrEmployeeNotFound           = errors.New("employee not found")
	ErrEmployeeEmailExists        = errors.New("employee email or username already exists")
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
