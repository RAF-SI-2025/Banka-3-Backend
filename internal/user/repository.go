package user

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type User struct {
	email          string
	hashedPassword []byte
	salt           []byte
}

var ErrInvalidPasswordActionToken = errors.New("invalid or expired password token")

var ErrCompanyNotFound = errors.New("company not found")
var ErrCompanyRegisteredIDExists = errors.New("company with registered id already exists")
var ErrCompanyOwnerNotFound = errors.New("company owner not found")
var ErrCompanyActivityCodeNotFound = errors.New("company activity code not found")

func (s *Server) GetUserByEmail(email string) (*User, error) {
	query := `
		SELECT email, password, salt_password FROM employees WHERE email = $1
		UNION ALL
		SELECT email, password, salt_password FROM clients WHERE email = $1
		LIMIT 1
	`

	var user User

	err := s.database.QueryRow(query, email).Scan(&user.email, &user.hashedPassword, &user.salt)
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
	_, err = s.database.Exec(query, email, hashed_token, expiry.Time)
	if err != nil {
		return fmt.Errorf("inserting refresh token: %w", err)
	}

	return nil
}

func (s *Server) UpsertPasswordActionToken(email, actionType string, hashedToken []byte, validUntil time.Time) error {
	query := `
	INSERT INTO password_action_tokens (email, action_type, hashed_token, valid_until, used)
	VALUES ($1, $2, $3, $4, FALSE)
	ON CONFLICT (email, action_type)
	DO UPDATE SET
		hashed_token = excluded.hashed_token,
		valid_until = excluded.valid_until,
		used = FALSE,
		used_at = NULL
	`

	_, err := s.database.Exec(query, email, actionType, hashedToken, validUntil)
	if err != nil {
		return fmt.Errorf("upserting password action token: %w", err)
	}
	return nil
}

func (s *Server) ConsumePasswordActionToken(tx *sql.Tx, hashedToken []byte) (string, string, error) {
	var email string
	var actionType string
	err := tx.QueryRow(`
		SELECT email, action_type
		FROM password_action_tokens
		WHERE hashed_token = $1 AND used = FALSE AND valid_until > NOW()
		FOR UPDATE
	`, hashedToken).Scan(&email, &actionType)
	if err == sql.ErrNoRows {
		return "", "", ErrInvalidPasswordActionToken
	}
	if err != nil {
		return "", "", fmt.Errorf("querying password action token: %w", err)
	}

	_, err = tx.Exec(`
		UPDATE password_action_tokens
		SET used = TRUE, used_at = NOW()
		WHERE email = $1 AND action_type = $2
	`, email, actionType)
	if err != nil {
		return "", "", fmt.Errorf("marking password action token used: %w", err)
	}

	return email, actionType, nil
}

func (s *Server) UpdatePasswordByEmail(tx *sql.Tx, email string, hashedPassword []byte) error {
	employeeRes, err := tx.Exec(`
		UPDATE employees
		SET password = $1, updated_at = NOW()
		WHERE email = $2
	`, hashedPassword, email)
	if err != nil {
		return fmt.Errorf("updating employee password: %w", err)
	}
	employeeRows, err := employeeRes.RowsAffected()
	if err != nil {
		return fmt.Errorf("reading employee affected rows: %w", err)
	}
	if employeeRows > 0 {
		return nil
	}

	clientRes, err := tx.Exec(`
		UPDATE clients
		SET password = $1, updated_at = NOW()
		WHERE email = $2
	`, hashedPassword, email)
	if err != nil {
		return fmt.Errorf("updating client password: %w", err)
	}
	clientRows, err := clientRes.RowsAffected()
	if err != nil {
		return fmt.Errorf("reading client affected rows: %w", err)
	}
	if clientRows == 0 {
		return fmt.Errorf("user not found for email")
	}

	return nil
}

func (s *Server) RevokeRefreshTokensByEmail(tx *sql.Tx, email string) error {
	_, err := tx.Exec(`UPDATE refresh_tokens SET revoked = TRUE WHERE email = $1`, email)
	if err != nil {
		return fmt.Errorf("revoking refresh tokens: %w", err)
	}
	return nil
}

func scanCompany(scanner interface {
	Scan(dest ...any) error
}) (*Company, error) {
	var company Company
	var activityCodeID sql.NullInt64
	err := scanner.Scan(
		&company.Id,
		&company.Registered_id,
		&company.Name,
		&company.Tax_code,
		&activityCodeID,
		&company.Address,
		&company.Owner_id,
	)
	if err != nil {
		return nil, err
	}
	if activityCodeID.Valid {
		company.Activity_code_id = activityCodeID.Int64
	}
	return &company, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func (s *Server) CreateCompanyRecord(company Company) (*Company, error) {
	tx, err := s.database.Begin()
	if err != nil {
		return nil, fmt.Errorf("starting transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var ownerExists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM clients WHERE id = $1)`, company.Owner_id).Scan(&ownerExists); err != nil {
		return nil, fmt.Errorf("checking owner existence: %w", err)
	}
	if !ownerExists {
		return nil, ErrCompanyOwnerNotFound
	}

	if company.Activity_code_id != 0 {
		var activityCodeExists bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM activity_codes WHERE id = $1)`, company.Activity_code_id).Scan(&activityCodeExists); err != nil {
			return nil, fmt.Errorf("checking activity code existence: %w", err)
		}
		if !activityCodeExists {
			return nil, ErrCompanyActivityCodeNotFound
		}
	}

	var row *sql.Row
	if company.Activity_code_id == 0 {
		row = tx.QueryRow(`
			INSERT INTO companies (registered_id, name, tax_code, activity_code_id, address, owner_id)
			VALUES ($1, $2, $3, NULL, $4, $5)
			RETURNING id, registered_id, name, tax_code, activity_code_id, address, owner_id
		`, company.Registered_id, company.Name, company.Tax_code, company.Address, company.Owner_id)
	} else {
		row = tx.QueryRow(`
			INSERT INTO companies (registered_id, name, tax_code, activity_code_id, address, owner_id)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING id, registered_id, name, tax_code, activity_code_id, address, owner_id
		`, company.Registered_id, company.Name, company.Tax_code, company.Activity_code_id, company.Address, company.Owner_id)
	}

	created, err := scanCompany(row)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrCompanyRegisteredIDExists
		}
		return nil, fmt.Errorf("creating company: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}

	return created, nil
}

func (s *Server) GetCompanyByIDRecord(companyID int64) (*Company, error) {
	row := s.database.QueryRow(`
		SELECT id, registered_id, name, tax_code, activity_code_id, address, owner_id
		FROM companies
		WHERE id = $1
	`, companyID)

	company, err := scanCompany(row)
	if err == sql.ErrNoRows {
		return nil, ErrCompanyNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("getting company by id: %w", err)
	}

	return company, nil
}

func (s *Server) GetCompaniesRecords() ([]*Company, error) {
	rows, err := s.database.Query(`
		SELECT id, registered_id, name, tax_code, activity_code_id, address, owner_id
		FROM companies
		ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("listing companies: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var companies []*Company
	for rows.Next() {
		company, err := scanCompany(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning company: %w", err)
		}
		companies = append(companies, company)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating companies: %w", err)
	}

	return companies, nil
}

func (s *Server) UpdateCompanyRecord(company Company) (*Company, error) {
	tx, err := s.database.Begin()
	if err != nil {
		return nil, fmt.Errorf("starting transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var companyExists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM companies WHERE id = $1)`, company.Id).Scan(&companyExists); err != nil {
		return nil, fmt.Errorf("checking company existence: %w", err)
	}
	if !companyExists {
		return nil, ErrCompanyNotFound
	}

	var ownerExists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM clients WHERE id = $1)`, company.Owner_id).Scan(&ownerExists); err != nil {
		return nil, fmt.Errorf("checking owner existence: %w", err)
	}
	if !ownerExists {
		return nil, ErrCompanyOwnerNotFound
	}

	if company.Activity_code_id != 0 {
		var activityCodeExists bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM activity_codes WHERE id = $1)`, company.Activity_code_id).Scan(&activityCodeExists); err != nil {
			return nil, fmt.Errorf("checking activity code existence: %w", err)
		}
		if !activityCodeExists {
			return nil, ErrCompanyActivityCodeNotFound
		}
	}

	var row *sql.Row
	if company.Activity_code_id == 0 {
		row = tx.QueryRow(`
			UPDATE companies
			SET name = $1, activity_code_id = NULL, address = $2, owner_id = $3
			WHERE id = $4
			RETURNING id, registered_id, name, tax_code, activity_code_id, address, owner_id
		`, company.Name, company.Address, company.Owner_id, company.Id)
	} else {
		row = tx.QueryRow(`
			UPDATE companies
			SET name = $1, activity_code_id = $2, address = $3, owner_id = $4
			WHERE id = $5
			RETURNING id, registered_id, name, tax_code, activity_code_id, address, owner_id
		`, company.Name, company.Activity_code_id, company.Address, company.Owner_id, company.Id)
	}

	updated, err := scanCompany(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrCompanyNotFound
		}
		return nil, fmt.Errorf("updating company: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}

	return updated, nil
}

func create_user_from_model[T Client | Employee](user T, s *Server) error {
	result := s.db_gorm.Create(&user)
	if result.Error != nil {
		log.Printf("We got this error: %s", result.Error.Error())
		return result.Error
	}
	return nil
}

func (s *Server) getEmployeeByEmail(email string) (*Employee, error) {
	var employee Employee
	err := s.db_gorm.Preload("Permissions").Where("email = ?", email).First(&employee).Error
	if err != nil {
		return nil, err
	}
	for _, perm := range employee.Permissions {
		println(perm.Name)
	}
	return &employee, nil
}

func (s *Server) getEmployeeById(id int64) (*Employee, error) {
	var employee Employee
	err := s.db_gorm.Preload("Permissions").Where("id = ?", id).First(&employee).Error
	if err != nil {
		return nil, err
	}
	for _, perm := range employee.Permissions {
		println(perm.Name)
	}
	return &employee, nil
}

func (s *Server) GetAllEmployees(email string, name string, last_name string, position string) (*[]Get_employees, error) {
	query := `SELECT e.id, e.first_name, e.last_name, e.email, e.position, e.phone_number, e.active, p.id, p.name
	FROM employees e
	JOIN employee_permissions ep ON e.id = ep.employee_id
	JOIN permissions p ON ep.permission_id = p.id`

	var conditions []string
	// Query is variadic, and interface{}
	// is basically the most generic type
	// interface is same as any, maybe it's nicer to use any here
	var args []interface{}

	if email != "" {
		conditions = append(conditions, "e.email = $"+strconv.Itoa(len(args)+1))
		args = append(args, email)
	}
	if name != "" {
		conditions = append(conditions, "e.first_name = $"+strconv.Itoa(len(args)+1))
		args = append(args, name)
	}
	if last_name != "" {
		conditions = append(conditions, "e.last_name = $"+strconv.Itoa(len(args)+1))
		args = append(args, last_name)
	}
	if position != "" {
		conditions = append(conditions, "e.position = $"+strconv.Itoa(len(args)+1))
		args = append(args, position)
	}

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}

	var employees []Get_employees
	rows, err := s.database.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("whatever the fuck went wrong now: %w", err)
	}

	for rows.Next() {
		var emp Get_employees
		var permissionID sql.NullInt64
		var permissionName sql.NullString

		if err := rows.Scan(
			&emp.Id,
			&emp.First_name,
			&emp.Last_name,
			&emp.Email,
			&emp.Position,
			&emp.Phone_number,
			&emp.Active,
			&permissionID,
			&permissionName,
		); err != nil {
			if closeErr := rows.Close(); closeErr != nil {
				return nil, fmt.Errorf("failed reading in the values: %w; additionally failed closing rows: %v", err, closeErr)
			}
			return nil, fmt.Errorf("failed reading in the values: %w", err)
		}

		if permissionID.Valid {
			emp.Permission_id = permissionID.Int64
		}
		if permissionName.Valid {
			emp.Permission_name = permissionName.String
		}

		employees = append(employees, emp)
	}

	if err := rows.Err(); err != nil {
		if closeErr := rows.Close(); closeErr != nil {
			return nil, fmt.Errorf("error: %w; additionally failed closing rows: %v", err, closeErr)
		}
		return nil, fmt.Errorf("error: %w", err)
	}

	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("failed closing rows: %w", err)
	}

	return &employees, nil
}

func (s *Server) UpdateEmployee_(emp *Employee) error {

	updates := map[string]any{
		"last_name":    emp.Last_name,
		"gender":       emp.Gender,
		"phone_number": emp.Phone_number,
		"address":      emp.Address,
		"position":     emp.Position,
		"department":   emp.Department,
		"active":       emp.Active,
	}

	tx := s.db_gorm.Begin()

	if err := tx.Model(&Employee{}).
		Where("id = ?", emp.Id).
		Updates(updates).Error; err != nil {
		tx.Rollback()
		return err
	}

	var perms []Permission
	var names []string

	for _, p := range emp.Permissions {
		names = append(names, p.Name)
	}

	if err := tx.
		Where("name IN ?", names).
		Find(&perms).Error; err != nil {
		tx.Rollback()
		return err
	}

	if err := tx.Model(emp).
		Association("Permissions").
		Replace(&perms); err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit().Error
}
