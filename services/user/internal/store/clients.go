package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/logger"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/postgres"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/user/internal/domain"
)

const clientColumns = `
    id, email, coalesce(password_hash, ''),
    first_name, last_name, date_of_birth, gender, phone, address,
    active, permissions, session_version, created_at, updated_at,
    failed_login_attempts, locked_until
`

func scanClient(row interface{ Scan(...any) error }) (*domain.Client, error) {
	var c domain.Client
	var gender string
	if err := row.Scan(
		&c.ID, &c.Email, &c.PasswordHash,
		&c.FirstName, &c.LastName, &c.DateOfBirth, &gender, &c.Phone, &c.Address,
		&c.Active, &c.Permissions, &c.SessionVersion, &c.CreatedAt, &c.UpdatedAt,
		&c.FailedLoginAttempts, &c.LockedUntil,
	); err != nil {
		return nil, err
	}
	c.Gender = domain.Gender(gender)
	return &c, nil
}

// CreateClient inserts a new client with no password hash. The caller
// (service layer) emails an initial-password link via the password
// reset machinery — clients use the same path employees use for forgot
// password, but with welcome wording.
func (s *Store) CreateClient(ctx context.Context, c *domain.Client) (*domain.Client, error) {
	const q = `
        insert into "user".clients
            (email, first_name, last_name, date_of_birth, gender,
             phone, address, active, permissions)
        values ($1,$2,$3,$4,$5,$6,$7,$8,$9)
        returning ` + clientColumns

	out, err := scanClient(s.DB.QueryRow(
		ctx, q,
		c.Email, c.FirstName, c.LastName, c.DateOfBirth, string(c.Gender),
		c.Phone, c.Address, c.Active, c.Permissions,
	))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, apperr.Conflict("an account with this email already exists")
		}
		logger.From(ctx).ErrorContext(ctx, "create client failed", "err", err, "email", c.Email)
		return nil, apperr.Internal("create client", err)
	}
	return out, nil
}

// GetClientByID returns the client or NotFound.
func (s *Store) GetClientByID(ctx context.Context, id string) (*domain.Client, error) {
	const q = `select ` + clientColumns + ` from "user".clients where id = $1`
	out, err := scanClient(s.DB.QueryRow(postgres.WithRead(ctx), q, id))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("client not found")
		}
		logger.From(ctx).ErrorContext(ctx, "get client failed", "err", err, "client_id", id)
		return nil, apperr.Internal("get client", err)
	}
	return out, nil
}

// GetClientByEmail returns the client or NotFound.
func (s *Store) GetClientByEmail(ctx context.Context, email string) (*domain.Client, error) {
	const q = `select ` + clientColumns + ` from "user".clients where lower(email) = lower($1)`
	out, err := scanClient(s.DB.QueryRow(ctx, q, email))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("client not found")
		}
		logger.From(ctx).ErrorContext(ctx, "get client by email failed", "err", err, "email", email)
		return nil, apperr.Internal("get client by email", err)
	}
	return out, nil
}

// UpdateClientProfile updates the editable profile fields.
func (s *Store) UpdateClientProfile(ctx context.Context, c *domain.Client) (*domain.Client, error) {
	const q = `
        update "user".clients set
            email = $2, first_name = $3, last_name = $4,
            date_of_birth = $5, gender = $6, phone = $7, address = $8
        where id = $1
        returning ` + clientColumns

	out, err := scanClient(s.DB.QueryRow(
		ctx, q,
		c.ID, c.Email, c.FirstName, c.LastName, c.DateOfBirth,
		string(c.Gender), c.Phone, c.Address,
	))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("client not found")
		}
		if isUniqueViolation(err) {
			return nil, apperr.Conflict("email already in use")
		}
		logger.From(ctx).ErrorContext(ctx, "update client failed", "err", err, "client_id", c.ID)
		return nil, apperr.Internal("update client", err)
	}
	return out, nil
}

// SetClientPasswordHash sets the password and bumps session_version.
func (s *Store) SetClientPasswordHash(ctx context.Context, id, hash string) error {
	const q = `
        update "user".clients set password_hash = $2, session_version = session_version + 1
        where id = $1`
	tag, err := s.DB.Exec(ctx, q, id, hash)
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "set client password failed", "err", err, "client_id", id)
		return apperr.Internal("set client password", err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("client not found")
	}
	return nil
}

// IncrementClientSessionVersion bumps the version. Returns the new value.
func (s *Store) IncrementClientSessionVersion(ctx context.Context, id string) (int64, error) {
	const q = `
        update "user".clients set session_version = session_version + 1
        where id = $1
        returning session_version`
	var v int64
	if err := s.DB.QueryRow(ctx, q, id).Scan(&v); err != nil {
		if noRows(err) {
			return 0, apperr.NotFound("client not found")
		}
		logger.From(ctx).ErrorContext(ctx, "bump client session version failed", "err", err, "client_id", id)
		return 0, apperr.Internal("bump client session version", err)
	}
	return v, nil
}

// ListClients returns a page of clients plus total.
func (s *Store) ListClients(ctx context.Context, f domain.ClientFilter, page, pageSize int) ([]*domain.Client, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 50
	}

	var conds []string
	var args []any
	if f.Email != "" {
		args = append(args, f.Email)
		conds = append(conds, fmt.Sprintf("lower(email) like '%%' || lower($%d) || '%%'", len(args)))
	}
	if f.Name != "" {
		args = append(args, f.Name)
		idx := len(args)
		conds = append(conds, fmt.Sprintf(
			"(lower(first_name) like '%%' || lower($%d) || '%%' or lower(last_name) like '%%' || lower($%d) || '%%')",
			idx, idx,
		))
	}

	where := ""
	if len(conds) > 0 {
		where = " where " + strings.Join(conds, " and ")
	}

	var total int64
	if err := s.DB.QueryRow(postgres.WithRead(ctx), `select count(*) from "user".clients`+where, args...).Scan(&total); err != nil {
		logger.From(ctx).ErrorContext(ctx, "count clients failed", "err", err)
		return nil, 0, apperr.Internal("count clients", err)
	}

	listArgs := append([]any{}, args...)
	listArgs = append(listArgs, pageSize, (page-1)*pageSize)
	listQ := `select ` + clientColumns + ` from "user".clients` + where +
		fmt.Sprintf(" order by last_name, first_name limit $%d offset $%d", len(args)+1, len(args)+2)

	rows, err := s.DB.Query(postgres.WithRead(ctx), listQ, listArgs...)
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "list clients failed", "err", err)
		return nil, 0, apperr.Internal("list clients", err)
	}
	defer rows.Close()

	var out []*domain.Client
	for rows.Next() {
		c, err := scanClient(rows)
		if err != nil {
			logger.From(ctx).ErrorContext(ctx, "scan client failed", "err", err)
			return nil, 0, apperr.Internal("scan client", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		logger.From(ctx).ErrorContext(ctx, "rows clients failed", "err", err)
		return nil, 0, apperr.Internal("rows clients", err)
	}
	return out, total, nil
}
