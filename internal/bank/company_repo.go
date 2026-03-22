package bank

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
)

var ErrCompanyNotFound = errors.New("company not found")
var ErrCompanyRegisteredIDExists = errors.New("company with registered id already exists")
var ErrCompanyOwnerNotFound = errors.New("company owner not found")
var ErrCompanyActivityCodeNotFound = errors.New("company activity code not found")

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
