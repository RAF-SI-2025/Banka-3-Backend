package store

import (
	"context"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/apperr"
	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/postgres"
	"github.com/RAF-SI-2025/Banka-3-Backend/services/bank/internal/domain"
)

const authorizedPersonColumns = `
    id, company_id, first_name, last_name, date_of_birth, gender,
    email, phone, address, created_at, updated_at
`

func scanAuthorizedPerson(row interface{ Scan(...any) error }) (*domain.AuthorizedPerson, error) {
	var p domain.AuthorizedPerson
	var gender string
	if err := row.Scan(
		&p.ID, &p.CompanyID, &p.FirstName, &p.LastName, &p.DateOfBirth, &gender,
		&p.Email, &p.Phone, &p.Address, &p.CreatedAt, &p.UpdatedAt,
	); err != nil {
		return nil, err
	}
	p.Gender = domain.Gender(gender)
	return &p, nil
}

func (s *Store) CreateAuthorizedPerson(ctx context.Context, p *domain.AuthorizedPerson) (*domain.AuthorizedPerson, error) {
	const q = `
        insert into "bank".authorized_persons
            (company_id, first_name, last_name, date_of_birth, gender,
             email, phone, address)
        values ($1,$2,$3,$4,$5,$6,$7,$8)
        returning ` + authorizedPersonColumns
	out, err := scanAuthorizedPerson(s.DB.QueryRow(
		ctx, q,
		p.CompanyID, p.FirstName, p.LastName, p.DateOfBirth, string(p.Gender),
		p.Email, p.Phone, p.Address,
	))
	if err != nil {
		return nil, apperr.Internal("create authorized person", err)
	}
	return out, nil
}

func (s *Store) GetAuthorizedPersonByID(ctx context.Context, id string) (*domain.AuthorizedPerson, error) {
	const q = `select ` + authorizedPersonColumns + ` from "bank".authorized_persons where id = $1`
	out, err := scanAuthorizedPerson(s.DB.QueryRow(postgres.WithRead(ctx), q, id))
	if err != nil {
		if noRows(err) {
			return nil, apperr.NotFound("ovlašćeno lice ne postoji")
		}
		return nil, apperr.Internal("get authorized person", err)
	}
	return out, nil
}

func (s *Store) ListAuthorizedPersonsByCompany(ctx context.Context, companyID string) ([]*domain.AuthorizedPerson, error) {
	const q = `select ` + authorizedPersonColumns + ` from "bank".authorized_persons where company_id = $1 order by last_name, first_name`
	rows, err := s.DB.Query(postgres.WithRead(ctx), q, companyID)
	if err != nil {
		return nil, apperr.Internal("list authorized persons", err)
	}
	defer rows.Close()
	var out []*domain.AuthorizedPerson
	for rows.Next() {
		p, err := scanAuthorizedPerson(rows)
		if err != nil {
			return nil, apperr.Internal("scan authorized person", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
