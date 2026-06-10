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

const employeeColumns = `
    id, email, username, coalesce(password_hash, ''),
    first_name, last_name, date_of_birth, gender, phone, address,
    position, department, active, permissions, session_version,
    created_at, updated_at, failed_login_attempts, locked_until
`

func scanEmployee(row interface{ Scan(...any) error }) (*domain.Employee, error) {
	var e domain.Employee
	var gender string
	if err := row.Scan(
		&e.ID, &e.Email, &e.Username, &e.PasswordHash,
		&e.FirstName, &e.LastName, &e.DateOfBirth, &gender, &e.Phone, &e.Address,
		&e.Position, &e.Department, &e.Active, &e.Permissions, &e.SessionVersion,
		&e.CreatedAt, &e.UpdatedAt, &e.FailedLoginAttempts, &e.LockedUntil,
	); err != nil {
		return nil, err
	}
	e.Gender = domain.Gender(gender)
	return &e, nil
}

// CreateEmployee inserts a new employee with no password hash. The
// caller (service) attaches an activation token afterward.
func (s *Store) CreateEmployee(ctx context.Context, e *domain.Employee) (*domain.Employee, error) {
	const q = `
        insert into "user".employees
            (email, username, first_name, last_name, date_of_birth,
             gender, phone, address, position, department, active, permissions)
        values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
        returning ` + employeeColumns

	row := s.DB.QueryRow(
		ctx, q,
		e.Email, e.Username, e.FirstName, e.LastName, e.DateOfBirth,
		string(e.Gender), e.Phone, e.Address, e.Position, e.Department, e.Active, e.Permissions,
	)
	out, err := scanEmployee(row)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, apperr.Conflict("an account with this email or username already exists")
		}
		logger.From(ctx).ErrorContext(ctx, "create employee failed", "err", err, "email", e.Email)
		return nil, apperr.Internal("create employee", err)
	}
	return out, nil
}

// GetEmployeeByID returns the employee or NotFound.
func (s *Store) GetEmployeeByID(ctx context.Context, id string) (*domain.Employee, error) {
	const q = `select ` + employeeColumns + ` from "user".employees where id = $1`
	out, err := scanEmployee(s.DB.QueryRow(postgres.WithRead(ctx), q, id))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("employee not found")
		}
		logger.From(ctx).ErrorContext(ctx, "get employee failed", "err", err, "employee_id", id)
		return nil, apperr.Internal("get employee", err)
	}
	return out, nil
}

// GetEmployeeByEmail returns the employee or NotFound.
func (s *Store) GetEmployeeByEmail(ctx context.Context, email string) (*domain.Employee, error) {
	const q = `select ` + employeeColumns + ` from "user".employees where lower(email) = lower($1)`
	out, err := scanEmployee(s.DB.QueryRow(ctx, q, email))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("employee not found")
		}
		logger.From(ctx).ErrorContext(ctx, "get employee by email failed", "err", err, "email", email)
		return nil, apperr.Internal("get employee by email", err)
	}
	return out, nil
}

// UpdateEmployeeProfile updates the editable profile fields (everything
// except id, password_hash, active, permissions, session_version).
func (s *Store) UpdateEmployeeProfile(ctx context.Context, e *domain.Employee) (*domain.Employee, error) {
	const q = `
        update "user".employees set
            email = $2, username = $3, first_name = $4, last_name = $5,
            date_of_birth = $6, gender = $7, phone = $8, address = $9,
            position = $10, department = $11
        where id = $1
        returning ` + employeeColumns

	row := s.DB.QueryRow(
		ctx, q,
		e.ID, e.Email, e.Username, e.FirstName, e.LastName, e.DateOfBirth,
		string(e.Gender), e.Phone, e.Address, e.Position, e.Department,
	)
	out, err := scanEmployee(row)
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("employee not found")
		}
		if isUniqueViolation(err) {
			return nil, apperr.Conflict("email or username already in use")
		}
		logger.From(ctx).ErrorContext(ctx, "update employee failed", "err", err, "employee_id", e.ID)
		return nil, apperr.Internal("update employee", err)
	}
	return out, nil
}

// SetEmployeePasswordHash also bumps session_version. Used by the
// activation flow and password reset.
func (s *Store) SetEmployeePasswordHash(ctx context.Context, id, hash string) error {
	const q = `
        update "user".employees set password_hash = $2, session_version = session_version + 1
        where id = $1`
	tag, err := s.DB.Exec(ctx, q, id, hash)
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "set employee password failed", "err", err, "employee_id", id)
		return apperr.Internal("set password", err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("employee not found")
	}
	return nil
}

// SetEmployeeActive toggles the active flag. Bumping session_version on
// deactivation is the caller's responsibility (service layer).
func (s *Store) SetEmployeeActive(ctx context.Context, id string, active bool) (*domain.Employee, error) {
	const q = `
        update "user".employees set active = $2
        where id = $1
        returning ` + employeeColumns

	out, err := scanEmployee(s.DB.QueryRow(ctx, q, id, active))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("employee not found")
		}
		logger.From(ctx).ErrorContext(ctx, "set employee active failed", "err", err, "employee_id", id, "active", active)
		return nil, apperr.Internal("set active", err)
	}
	return out, nil
}

// SetEmployeePermissions replaces the permissions list and bumps
// session_version so existing tokens revalidate against the new set.
func (s *Store) SetEmployeePermissions(ctx context.Context, id string, perms []string) (*domain.Employee, error) {
	const q = `
        update "user".employees set
            permissions = $2,
            session_version = session_version + 1
        where id = $1
        returning ` + employeeColumns

	out, err := scanEmployee(s.DB.QueryRow(ctx, q, id, perms))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("employee not found")
		}
		logger.From(ctx).ErrorContext(ctx, "set employee permissions failed", "err", err, "employee_id", id)
		return nil, apperr.Internal("set permissions", err)
	}
	return out, nil
}

// IncrementEmployeeSessionVersion bumps the version, used on
// deactivation. Returns the new value.
func (s *Store) IncrementEmployeeSessionVersion(ctx context.Context, id string) (int64, error) {
	const q = `
        update "user".employees set session_version = session_version + 1
        where id = $1
        returning session_version`
	var v int64
	if err := s.DB.QueryRow(ctx, q, id).Scan(&v); err != nil {
		if noRows(err) {
			return 0, apperr.NotFound("employee not found")
		}
		logger.From(ctx).ErrorContext(ctx, "bump employee session version failed", "err", err, "employee_id", id)
		return 0, apperr.Internal("bump session version", err)
	}
	return v, nil
}

// ListEmployees returns a page of employees plus the total row count.
func (s *Store) ListEmployees(ctx context.Context, f domain.EmployeeFilter, page, pageSize int) ([]*domain.Employee, int64, error) {
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
	if f.Position != "" {
		args = append(args, f.Position)
		conds = append(conds, fmt.Sprintf("lower(position) like '%%' || lower($%d) || '%%'", len(args)))
	}

	where := ""
	if len(conds) > 0 {
		where = " where " + strings.Join(conds, " and ")
	}

	countQ := `select count(*) from "user".employees` + where
	var total int64
	if err := s.DB.QueryRow(postgres.WithRead(ctx), countQ, args...).Scan(&total); err != nil {
		logger.From(ctx).ErrorContext(ctx, "count employees failed", "err", err)
		return nil, 0, apperr.Internal("count employees", err)
	}

	listArgs := append([]any{}, args...)
	listArgs = append(listArgs, pageSize, (page-1)*pageSize)
	listQ := `select ` + employeeColumns + ` from "user".employees` + where +
		fmt.Sprintf(" order by last_name, first_name limit $%d offset $%d", len(args)+1, len(args)+2)

	rows, err := s.DB.Query(postgres.WithRead(ctx), listQ, listArgs...)
	if err != nil {
		logger.From(ctx).ErrorContext(ctx, "list employees failed", "err", err)
		return nil, 0, apperr.Internal("list employees", err)
	}
	defer rows.Close()

	var out []*domain.Employee
	for rows.Next() {
		e, err := scanEmployee(rows)
		if err != nil {
			logger.From(ctx).ErrorContext(ctx, "scan employee failed", "err", err)
			return nil, 0, apperr.Internal("scan employee", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		logger.From(ctx).ErrorContext(ctx, "rows employees failed", "err", err)
		return nil, 0, apperr.Internal("rows employees", err)
	}
	return out, total, nil
}
